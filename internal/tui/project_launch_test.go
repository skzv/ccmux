package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/project"
)

// These tests pin the TUI's project-launch resolution against two
// bugs: "everything launches claude" and "a new session resumes an
// old conversation". Two helpers — launchCmdForProject (Project in
// hand) and launchCmdForProjectPath (path on disk) — feed every
// create-session call site on the Projects screen, and both must
// agree on what command the project's sidecar maps to. A future
// change that hardcodes "claude" in either spot is caught by the
// source-grep audit in internal/agent/no_hardcode_audit_test.go;
// these tests pin the positive behavior so a refactor that moves the
// resolution elsewhere doesn't silently regress.

// TestLaunchCmdForProject_PerAgent — every supported agent ID on a
// project.Project resolves to a fresh launch with a shell fallback. The
// `false` matters: both callers create a session the user asked for
// as new, and `--continue` would resume whatever conversation the
// agent last had in that directory instead. Resuming a specific past
// conversation is its own menu row (resumeConversationCmd).
func TestLaunchCmdForProject_PerAgent(t *testing.T) {
	for _, a := range agent.All() {
		t.Run(string(a.ID()), func(t *testing.T) {
			p := project.Project{Agent: a.ID()}
			got := launchCmdForProject(p)
			want := a.LaunchCmd(false) + " || zsh || bash || sh"
			if got != want {
				t.Errorf("launchCmdForProject(Agent=%q) = %q, want %q",
					a.ID(), got, want)
			}
			if !strings.HasPrefix(got, a.Binary()) {
				t.Errorf("launchCmdForProject(Agent=%q) = %q, expected to start with binary %q",
					a.ID(), got, a.Binary())
			}
			if strings.Contains(got, "--continue") || strings.Contains(got, " resume") {
				t.Errorf("launchCmdForProject(Agent=%q) = %q, a new session must not resume a conversation",
					a.ID(), got)
			}
		})
	}
}

// TestLaunchCmdForProject_EmptyAgentDefaultsToClaude — projects
// scaffolded before the sidecar landed have Agent == "". By
// agent.ByID's contract that resolves to Claude. This is the
// back-compat invariant: a pre-multi-agent project must keep
// launching claude when the user hits Enter.
func TestLaunchCmdForProject_EmptyAgentDefaultsToClaude(t *testing.T) {
	got := launchCmdForProject(project.Project{Agent: ""})
	want := agent.Claude{}.LaunchCmd(false) + " || zsh || bash || sh"
	if got != want {
		t.Errorf("empty agent = %q, want %q (claude back-compat)", got, want)
	}
}

// TestLaunchCmdForProjectPath_HonorsSidecar — the path-flavoured
// helper reads `.ccmux/agent` and uses that. Without this, the project
// menu's "Start a new session" path would hardcode claude regardless
// of the sidecar.
func TestLaunchCmdForProjectPath_HonorsSidecar(t *testing.T) {
	for _, a := range agent.All() {
		t.Run(string(a.ID()), func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, ".ccmux"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := project.SetAgent(dir, a.ID()); err != nil {
				t.Fatal(err)
			}
			got := launchCmdForProjectPath(dir)
			want := a.LaunchCmd(false) + " || zsh || bash || sh"
			if got != want {
				t.Errorf("launchCmdForProjectPath(sidecar=%q) = %q, want %q",
					a.ID(), got, want)
			}
		})
	}
}

// TestLaunchCmdForProjectPath_MissingSidecarFallsBackToClaude — the
// path helper must default to claude when the sidecar is missing.
// project.ReadAgent already does the IDClaude fallback; this test
// confirms the launch helper follows.
func TestLaunchCmdForProjectPath_MissingSidecarFallsBackToClaude(t *testing.T) {
	dir := t.TempDir()
	got := launchCmdForProjectPath(dir)
	want := agent.Claude{}.LaunchCmd(false) + " || zsh || bash || sh"
	if got != want {
		t.Errorf("missing sidecar = %q, want %q", got, want)
	}
}

// TestLaunchCmdForProject_AgreesWithPathFlavor — both helpers must
// produce the same string for the same project. A divergence would
// mean Enter-on-Projects launches one agent but the "Start new"
// branch of the picker launches another for the same project.
func TestLaunchCmdForProject_AgreesWithPathFlavor(t *testing.T) {
	for _, a := range agent.All() {
		t.Run(string(a.ID()), func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, ".ccmux"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := project.SetAgent(dir, a.ID()); err != nil {
				t.Fatal(err)
			}
			p := project.Project{Path: dir, Agent: a.ID()}
			if launchCmdForProject(p) != launchCmdForProjectPath(dir) {
				t.Errorf("project (%q) and path (%q) flavors disagree for agent=%q",
					launchCmdForProject(p), launchCmdForProjectPath(dir), a.ID())
			}
		})
	}
}

// runCmdTree runs cmd and every command nested in the message it
// returns (tea.Batch / tea.Sequence results, which are slices of
// commands), collecting the leaf messages.
func runCmdTree(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if v := reflect.ValueOf(msg); v.IsValid() && v.Kind() == reflect.Slice {
		var out []tea.Msg
		for i := 0; i < v.Len(); i++ {
			if c, ok := v.Index(i).Interface().(tea.Cmd); ok {
				out = append(out, runCmdTree(c)...)
			}
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

// TestRemoteProjectEnter_StartsFreshSession — #188 on remote hosts:
// Enter on a remote project asked its daemon for a session with
// Continue: true, so the agent resumed whatever conversation it last
// had in that directory (often a headless one) instead of starting the
// new session the user asked for.
func TestRemoteProjectEnter_StartsFreshSession(t *testing.T) {
	var got []daemon.NewSessionRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/sessions" {
			var req daemon.NewSessionRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			got = append(got, req)
			_ = json.NewEncoder(w).Encode(daemon.SessionState{Name: "c-alpha"})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	a := newAppForTest(t)
	a.hosts = []hostStatus{{Name: "mac-mini", Source: "configured", Address: strings.TrimPrefix(srv.URL, "http://"), DialHost: "mac-mini", OK: true, DaemonOK: true}}
	msgs := runCmdTree(a.attachOrCreateRemote(project.Project{Name: "alpha", Host: "mac-mini", Path: "/Users/me/Projects/alpha"}, "mac-mini"))
	if len(got) != 1 {
		t.Fatalf("remote daemon saw %d create requests, want 1 (msgs %#v)", len(got), msgs)
	}
	if got[0].Continue {
		t.Error("remote project Enter asked for --continue; it must start a fresh session")
	}
	if got[0].Project != "alpha" {
		t.Errorf("create request project = %q, want alpha", got[0].Project)
	}
	if _, ok := findMsg[remoteSessionStartedMsg](msgs); !ok {
		t.Errorf("no remoteSessionStartedMsg to attach with, got %#v", msgs)
	}
}
