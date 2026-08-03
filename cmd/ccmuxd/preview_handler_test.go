package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/tmux"
)

// TestHandlePreview_MapsMissingSessionTo404 — finding: the 404 mapping
// never fired because exec.ExitError.Error() is just "exit status 1"
// (tmux's stderr lives in ExitError.Stderr). internal/tmux now folds
// the stderr text into the wrapped error, and handlePreview goes
// through the capture seam so the mapping is testable end-to-end.
func TestHandlePreview_MapsMissingSessionTo404(t *testing.T) {
	s := &server{cfg: config.Config{}}
	s.capture = func(ctx context.Context, name string, lines int) (string, error) {
		// The exact shape tmux.CapturePane produces for a dead session
		// after the stderr-wrapping fix.
		return "", fmt.Errorf("tmux capture-pane: %w (can't find session: %s)",
			errors.New("exit status 1"), name)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/nope/preview", nil)
	s.handlePreview(rec, req, "nope")
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing session preview: status = %d, want 404; body=%s", rec.Code, rec.Body)
	}
}

// TestHandlePreview_OtherErrorsSurfaceStderrText — a non-"missing
// session" failure stays a 500, and the client-visible body carries
// the tmux stderr diagnostic instead of an opaque "exit status 1".
func TestHandlePreview_OtherErrorsSurfaceStderrText(t *testing.T) {
	s := &server{cfg: config.Config{}}
	s.capture = func(ctx context.Context, name string, lines int) (string, error) {
		return "", fmt.Errorf("tmux capture-pane: %w (server version mismatch)",
			errors.New("exit status 2"))
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/x/preview", nil)
	s.handlePreview(rec, req, "x")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "server version mismatch") {
		t.Errorf("body should carry the stderr diagnostic; got %q", rec.Body)
	}
}

// TestListSessions_SurfacesListFailure — finding 5c: the sessions
// endpoint returned an empty 200 on a tmux.List failure ("tmux not on
// PATH" indistinguishable from "no sessions"). Real errors are now a
// 500; the no-server case is still a success inside tmux.List.
func TestListSessions_SurfacesListFailure(t *testing.T) {
	s := &server{cfg: config.Config{}, seen: map[string]*tracked{}}
	s.list = func(ctx context.Context) ([]tmux.Session, error) {
		return nil, errors.New(`exec: "tmux": executable file not found in $PATH`)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	s.listSessions(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500; body=%s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "not found in $PATH") {
		t.Errorf("body should carry the underlying error; got %q", rec.Body)
	}
}
