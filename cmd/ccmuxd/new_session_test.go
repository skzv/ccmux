//go:build integration

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/daemon"
)

// TestCreateSession_NameOverride covers the mobile UX where the user
// types a custom session name in the New Session form. The daemon
// creates the tmux session under that exact name instead of the
// derived c-<project>.
func TestCreateSession_NameOverride(t *testing.T) {
	dir := pollSandbox(t)
	projDir := filepath.Join(dir, "proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	srv := newServer(testDaemonCfg(dir))
	mux := http.NewServeMux()
	srv.routes(mux)
	httpSrv := httptest.NewServer(mux)
	defer httpSrv.Close()

	body, _ := json.Marshal(daemon.NewSessionRequest{
		Project: "proj",
		Name:    "my-custom-name",
		// Continue=true → launch is "claude --continue || claude || zsh",
		// so CI runners without claude still keep the session alive
		// via the zsh fallback. The test only cares about the session
		// name, not which command ended up running.
		Continue: true,
	})
	resp := mustPost(t, httpSrv, "/v1/sessions", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}

	// The session must exist by the requested name, not the derived one.
	if !sessionExists(t, "my-custom-name") {
		t.Errorf("expected tmux session 'my-custom-name' to exist")
	}
	if sessionExists(t, "c-proj") {
		t.Errorf("did not expect derived session 'c-proj' to also exist")
	}
}

// TestCreateSession_DefaultDerivedName covers the no-name path — the
// daemon still derives c-<sanitized project> when Name is empty.
func TestCreateSession_DefaultDerivedName(t *testing.T) {
	dir := pollSandbox(t)
	projDir := filepath.Join(dir, "proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	srv := newServer(testDaemonCfg(dir))
	mux := http.NewServeMux()
	srv.routes(mux)
	httpSrv := httptest.NewServer(mux)
	defer httpSrv.Close()

	body, _ := json.Marshal(daemon.NewSessionRequest{
		Project: "proj",
		// See note in NameOverride — Continue:true keeps the session
		// alive in CI environments without claude installed.
		Continue: true,
	})
	resp := mustPost(t, httpSrv, "/v1/sessions", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}

	if !sessionExists(t, "c-proj") {
		t.Errorf("expected derived session 'c-proj' to exist")
	}
}

// TestCreateSession_AgentPersistsSidecar covers the mobile UX where
// the user picks an agent in the New Session form. The daemon writes
// the choice to .ccmux/agent so the launch command and every future
// attach pick the same agent.
func TestCreateSession_AgentPersistsSidecar(t *testing.T) {
	dir := pollSandbox(t)
	projDir := filepath.Join(dir, "proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	srv := newServer(testDaemonCfg(dir))
	mux := http.NewServeMux()
	srv.routes(mux)
	httpSrv := httptest.NewServer(mux)
	defer httpSrv.Close()

	body, _ := json.Marshal(daemon.NewSessionRequest{
		Project: "proj",
		Agent:   "codex",
	})
	resp := mustPost(t, httpSrv, "/v1/sessions", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}

	sidecar := filepath.Join(projDir, ".ccmux", "agent")
	got, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	if !strings.HasPrefix(string(got), "codex") {
		t.Errorf("sidecar = %q, want 'codex'", got)
	}
}

// TestCreateSession_InvalidNameRejected covers the path-segment safety
// check — tmux uses the name as -s, so /, \, and : must be rejected
// rather than passed through and silently mangled.
func TestCreateSession_InvalidNameRejected(t *testing.T) {
	dir := pollSandbox(t)
	projDir := filepath.Join(dir, "proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	srv := newServer(testDaemonCfg(dir))
	mux := http.NewServeMux()
	srv.routes(mux)
	httpSrv := httptest.NewServer(mux)
	defer httpSrv.Close()

	for _, name := range []string{"bad/name", "bad\\name", "bad:name"} {
		body, _ := json.Marshal(daemon.NewSessionRequest{
			Project: "proj",
			Name:    name,
		})
		resp := mustPost(t, httpSrv, "/v1/sessions", body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("name %q: status %d, want 400", name, resp.StatusCode)
		}
	}
}

// TestCreateSession_InvalidAgentIgnored — sending an unrecognized
// agent string mustn't 500 or panic; the daemon just falls through to
// the project's default (claude, with no sidecar) for the launch.
func TestCreateSession_InvalidAgentIgnored(t *testing.T) {
	dir := pollSandbox(t)
	projDir := filepath.Join(dir, "proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	srv := newServer(testDaemonCfg(dir))
	mux := http.NewServeMux()
	srv.routes(mux)
	httpSrv := httptest.NewServer(mux)
	defer httpSrv.Close()

	body, _ := json.Marshal(daemon.NewSessionRequest{
		Project: "proj",
		Agent:   "not-an-agent",
	})
	resp := mustPost(t, httpSrv, "/v1/sessions", body)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status %d, want 200", resp.StatusCode)
	}
	// No sidecar written, because the agent string didn't parse.
	if _, err := os.Stat(filepath.Join(projDir, ".ccmux", "agent")); err == nil {
		t.Errorf("expected no .ccmux/agent sidecar for an invalid agent")
	}
}

// mustPost is a small POST helper for the create-session tests.
func mustPost(t *testing.T, srv *httptest.Server, path string, body []byte) *http.Response {
	t.Helper()
	resp, err := srv.Client().Post(srv.URL+path, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// sessionExists checks the isolated tmux server for a session by exact name.
func sessionExists(t *testing.T, name string) bool {
	t.Helper()
	err := exec.Command("tmux", "has-session", "-t", "="+name).Run()
	return err == nil
}
