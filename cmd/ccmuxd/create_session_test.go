package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
)

// postNewSession drives createSession against the logging fake tmux
// (fakeTmuxLog): it lists no sessions, so the request starts one, and
// every tmux call is only recorded.
func postNewSession(t *testing.T, s *server, req daemon.NewSessionRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(req)
	rec := httptest.NewRecorder()
	s.createSession(rec, httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body)))
	return rec
}

// TestCreateSession_ExpandsTildeInPath — MCP spawn_session passes paths
// like "~/Projects/foo". createBareSession expanded the tilde but
// createSession looked for a literal "~" directory and answered 404.
func TestCreateSession_ExpandsTildeInPath(t *testing.T) {
	fakeTmuxLog(t) // also gives the test its own HOME
	home := os.Getenv("HOME")
	proj := filepath.Join(home, "Projects", "foo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	s := &server{cfg: config.Config{}}

	rec := postNewSession(t, s, daemon.NewSessionRequest{Project: "foo", Path: "~/Projects/foo"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got daemon.SessionState
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Path != proj {
		t.Errorf("path = %q, want %q", got.Path, proj)
	}
}

// TestCreateSession_RejectsProjectOutsideRoot — without a path, the
// project name is joined onto the projects root. It wasn't validated,
// so {"project": "../.."} started a session outside the root, the way
// createProject's name check exists to prevent.
func TestCreateSession_RejectsProjectOutsideRoot(t *testing.T) {
	fakeTmuxLog(t)
	root := filepath.Join(t.TempDir(), "a", "b", "Projects")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	s := &server{cfg: config.Config{Projects: config.ProjectsConfig{Root: root}}}

	for _, p := range []string{"../..", "..", "../b", ".hidden", "a/b"} {
		if rec := postNewSession(t, s, daemon.NewSessionRequest{Project: p}); rec.Code != http.StatusBadRequest {
			t.Errorf("project %q: status %d, want 400 (%s)", p, rec.Code, rec.Body)
		}
	}
}

// TestCreateBareSession_RejectsFormatInName — tmux expands formats in
// new-session -s, so "x#{session_id}" became a session called "x$1"
// and the name handed back to the client didn't exist.
func TestCreateBareSession_RejectsFormatInName(t *testing.T) {
	logPath := fakeTmuxLog(t)
	s := &server{cfg: config.Config{}}
	s.has = func(context.Context, string) (bool, error) { return false, nil }

	body, _ := json.Marshal(daemon.NewBareSessionRequest{Name: "x#{session_id}", Path: t.TempDir(), Agent: "shell"})
	rec := httptest.NewRecorder()
	s.createBareSession(rec, httptest.NewRequest(http.MethodPost, "/v1/sessions/bare", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400 (%s)", rec.Code, rec.Body)
	}
	if calls := tmuxCallsWith(t, logPath, "new-session"); len(calls) != 0 {
		t.Errorf("tmux new-session ran for a rejected name: %q", calls)
	}
}
