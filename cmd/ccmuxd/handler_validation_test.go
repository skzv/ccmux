package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
)

// TestCreateProject_RejectsControlCharacters — a project name carrying
// ESC or another control character was created, and every later listing
// printed it raw into the user's terminal.
func TestCreateProject_RejectsControlCharacters(t *testing.T) {
	logPath := fakeTmuxLog(t)
	root := t.TempDir()
	s := &server{cfg: config.Config{Projects: config.ProjectsConfig{Root: root}}}
	for _, name := range []string{"esc\x1b]0;pwned\x07", "c1\u009b31m", "del\x7f"} {
		body, _ := json.Marshal(daemon.NewProjectRequest{Name: name})
		rec := httptest.NewRecorder()
		s.createProject(rec, httptest.NewRequest(http.MethodPost, "/v1/projects", bytes.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("project %q: status %d, want 400 (%s)", name, rec.Code, rec.Body)
		}
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Errorf("directories created for rejected names: %v", entries)
	}
	if calls := tmuxCallsWith(t, logPath, "new-session"); len(calls) != 0 {
		t.Errorf("tmux new-session ran for a rejected name: %q", calls)
	}
}

// TestSessionNamesStartingWithDollarAreRejected — tmux reads `=$1:` as
// session ID $1, so POST /v1/sessions/$1/kill answered 204 after
// killing whichever session had that ID, and POST /v1/sessions/bare
// {"name":"$0"} answered 200 without creating anything (has-session
// matched the session with ID $0).
func TestSessionNamesStartingWithDollarAreRejected(t *testing.T) {
	fakeTmuxLog(t) // nothing may reach a real tmux
	killed := []string{}
	s := &server{cfg: config.Config{}, events: daemon.NewEventBus(), seen: map[string]*tracked{}}
	s.kill = func(_ context.Context, name string) error {
		killed = append(killed, name)
		return nil
	}
	s.rename = func(context.Context, string, string) error { return nil }
	s.capture = func(context.Context, string, int) (string, error) { return "", nil }
	s.has = func(context.Context, string) (bool, error) { return true, nil }
	mux := http.NewServeMux()
	s.routes(mux)

	for _, path := range []string{"/v1/sessions/$1/kill", "/v1/sessions/$0/rename", "/v1/sessions/$0/send-keys", "/v1/sessions/$0/preview"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(`{"name":"x","keys":"y"}`))))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400 (%s)", path, rec.Code, rec.Body)
		}
	}
	if len(killed) != 0 {
		t.Errorf("tmux kill ran for %q", killed)
	}

	for _, name := range []string{"$0", "$x"} {
		body, _ := json.Marshal(daemon.NewBareSessionRequest{Name: name, Path: t.TempDir(), Agent: "shell"})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/sessions/bare", bytes.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("bare session %q: status %d, want 400 (%s)", name, rec.Code, rec.Body)
		}
		body, _ = json.Marshal(daemon.RenameRequest{Name: name})
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/sessions/old/rename", bytes.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("rename to %q: status %d, want 400 (%s)", name, rec.Code, rec.Body)
		}
	}
}
