//go:build integration

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/project"
)

// stubAgentPath replaces PATH with a directory holding tmux and a stub
// for each named agent binary (plus /usr/bin:/bin), so no real agent
// the machine has installed can run. A stub records that it ran by
// creating "<name>-ran" in its working directory, then stays up. Call it
// before the test's first tmux command: the tmux server, and so every
// pane, keeps the PATH it started with.
func stubAgentPath(t *testing.T, names ...string) {
	t.Helper()
	tmuxBin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not installed")
	}
	dir := t.TempDir()
	if err := os.Symlink(tmuxBin, filepath.Join(dir, "tmux")); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		script := "#!/bin/sh\ntouch \"$PWD/" + name + "-ran\"\nexec sleep 300\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+":/usr/bin:/bin")
}

// newAPIServer serves the daemon's tailnet-safe routes for dir's sandbox.
func newAPIServer(t *testing.T, dir string) *httptest.Server {
	t.Helper()
	srv := newServer(testDaemonCfg(dir))
	mux := http.NewServeMux()
	srv.routes(mux)
	httpSrv := httptest.NewServer(mux)
	t.Cleanup(httpSrv.Close)
	return httpSrv
}

func postJSON(t *testing.T, srv *httptest.Server, path string, body any) (int, string) {
	t.Helper()
	raw, _ := json.Marshal(body)
	resp := mustPost(t, srv, path, raw)
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

// waitForFile waits up to 3s for path to exist.
func waitForFile(path string) bool {
	for range 60 {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func sessionAgentTag(t *testing.T, name string) string {
	t.Helper()
	out, _ := exec.Command("tmux", "show-options", "-v", "-t", "="+name+":", "@ccmux_agent").Output()
	return strings.TrimSpace(string(out))
}

// TestCreateEndpoints_MissingAgentIsAFailedStart — with the agent's
// binary missing and no --continue shell fallback, the session exits at
// once. Every create endpoint answered 200 for it (and no event ever
// followed); they now answer 502 naming the binary.
func TestCreateEndpoints_MissingAgentIsAFailedStart(t *testing.T) {
	dir := pollSandbox(t)
	stubAgentPath(t) // no agents at all
	projDir := filepath.Join(dir, "proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustTmux(t, "new-session", "-d", "-s", "c-anchor", "-c", dir, "sleep 300")
	httpSrv := newAPIServer(t, dir)

	for path, body := range map[string]any{
		"/v1/sessions":      daemon.NewSessionRequest{Project: "proj", Agent: "codex"},
		"/v1/sessions/bare": daemon.NewBareSessionRequest{Name: "c-bare-codex", Path: dir, Agent: "codex"},
		"/v1/projects":      daemon.NewProjectRequest{Name: "fresh", Agent: "codex"},
	} {
		code, msg := postJSON(t, httpSrv, path, body)
		if code != http.StatusBadGateway || !strings.Contains(msg, "codex") {
			t.Errorf("POST %s: status %d %q, want 502 naming codex", path, code, msg)
		}
	}
	for _, name := range []string{"c-proj", "c-bare-codex", "c-fresh"} {
		if sessionExists(t, name) {
			t.Errorf("session %s outlived its failed start", name)
		}
	}
	if got := project.ReadAgent(projDir); got != agent.IDClaude {
		t.Errorf("a failed start recorded the project's agent as %q", got)
	}
}

// TestCreateSession_UnwritableSidecarRunsRequestedAgent — the launch
// command was built by writing the requested agent to .ccmux/agent and
// reading it back, so with an unwritable sidecar {"agent":"codex"} ran
// claude. The requested agent must run, tagged on the session.
func TestCreateSession_UnwritableSidecarRunsRequestedAgent(t *testing.T) {
	dir := pollSandbox(t)
	stubAgentPath(t, "codex", "claude")
	projDir := filepath.Join(dir, "proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, ".ccmux"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	httpSrv := newAPIServer(t, dir)

	if code, msg := postJSON(t, httpSrv, "/v1/sessions", daemon.NewSessionRequest{Project: "proj", Agent: "codex"}); code != http.StatusOK {
		t.Fatalf("status %d: %s", code, msg)
	}
	if !waitForFile(filepath.Join(projDir, "codex-ran")) {
		t.Errorf("codex never ran in the session (claude ran: %v)", waitForFile(filepath.Join(projDir, "claude-ran")))
	}
	if tag := sessionAgentTag(t, "c-proj"); tag != "codex" {
		t.Errorf("@ccmux_agent = %q, want codex", tag)
	}
}

// TestCreateProject_ExistingAndHashNames — POST /v1/projects for a
// project whose session already runs answered 500 "duplicate session";
// it is idempotent now. A `#` in the name reached tmux's -c unescaped,
// so the session recorded (and the agent ran in) a format-expanded
// directory that didn't exist.
func TestCreateProject_ExistingAndHashNames(t *testing.T) {
	dir := pollSandbox(t)
	stubAgentPath(t, "claude")
	httpSrv := newAPIServer(t, dir)

	for _, name := range []string{"proj", "with#hash"} {
		var first daemon.NewProjectResponse
		for i := range 2 {
			code, msg := postJSON(t, httpSrv, "/v1/projects", daemon.NewProjectRequest{Name: name})
			if code != http.StatusOK {
				t.Fatalf("%s, call %d: status %d: %s", name, i+1, code, msg)
			}
			var got daemon.NewProjectResponse
			if err := json.Unmarshal([]byte(msg), &got); err != nil {
				t.Fatal(err)
			}
			if i == 0 {
				first = got
			} else if got.Session != first.Session || got.Path != first.Path {
				t.Errorf("%s: second call answered %+v, first %+v", name, got, first)
			}
		}
		want := filepath.Join(dir, name)
		out, err := exec.Command("tmux", "display-message", "-p", "-t", "="+first.Session+":", "#{session_path}").Output()
		if err != nil || strings.TrimSpace(string(out)) != want {
			t.Errorf("%s: session_path = %q (%v), want %q", name, out, err, want)
		}
		if !waitForFile(filepath.Join(want, "claude-ran")) {
			t.Errorf("%s: the agent didn't run in the project directory", name)
		}
	}
}
