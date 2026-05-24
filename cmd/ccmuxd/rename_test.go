package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
)

// TestHandleRename_RejectsInvalidNames pins the validation guard added
// to handleRename — same rule createSession enforces, for the same
// reason. Without it a peer could rename a session to "victim:0" and
// then subsequent send-keys parses session=victim, window=0, letting
// the attacker inject keystrokes into a totally unrelated tmux session.
//
// Unit-level test: the validation runs before tmux is invoked, so no
// real tmux server is needed.
func TestHandleRename_RejectsInvalidNames(t *testing.T) {
	s := &server{
		cfg: config.Config{
			Daemon: config.DaemonConfig{TailnetPort: 7474},
		},
	}

	for _, newName := range []string{"victim:0", "bad/name", "bad\\name"} {
		t.Run(newName, func(t *testing.T) {
			body, _ := json.Marshal(daemon.RenameRequest{Name: newName})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/sessions/old/rename", bytes.NewReader(body))
			s.handleRename(rec, req, "old")
			if rec.Code != http.StatusBadRequest {
				t.Errorf("rename to %q: status = %d, want 400; body=%s", newName, rec.Code, rec.Body)
			}
			if !strings.Contains(rec.Body.String(), "/, \\, or :") {
				t.Errorf("rename to %q: body should explain the rule; got %q", newName, rec.Body)
			}
		})
	}
}

// TestHandleRename_RejectsWrongMethod — POST-only, mirrors handleKill.
func TestHandleRename_RejectsWrongMethod(t *testing.T) {
	s := &server{cfg: config.Config{}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/old/rename", nil)
	s.handleRename(rec, req, "old")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET status = %d, want 405", rec.Code)
	}
}

// TestHandleRename_RejectsEmptyName — the existing guard before the new
// character validation.
func TestHandleRename_RejectsEmptyName(t *testing.T) {
	s := &server{cfg: config.Config{}}
	body, _ := json.Marshal(daemon.RenameRequest{Name: ""})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/old/rename", bytes.NewReader(body))
	s.handleRename(rec, req, "old")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}
