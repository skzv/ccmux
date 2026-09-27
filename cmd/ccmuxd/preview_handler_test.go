package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
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

// TestHandlePreview_ReturnsExactlyTheLastNLines — capture-pane -S -N
// gives N lines of scrollback plus the whole visible screen (blank rows
// included), so ?lines=N answered far more than N lines while reporting
// "lines": N.
func TestHandlePreview_ReturnsExactlyTheLastNLines(t *testing.T) {
	s := &server{cfg: config.Config{}}
	var pane strings.Builder
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&pane, "line %d\n", i)
	}
	pane.WriteString("\n\n\n\n\n\n") // the unused rest of the screen
	s.capture = func(context.Context, string, int) (string, error) { return pane.String(), nil }

	for _, tc := range []struct {
		query string
		want  string
	}{
		{"?lines=3", "line 38\nline 39\nline 40\n"},
		{"?lines=1", "line 40\n"},
		{"", strings.Join(func() []string {
			var l []string
			for i := 17; i <= 40; i++ {
				l = append(l, fmt.Sprintf("line %d", i))
			}
			return l
		}(), "\n") + "\n"},
	} {
		rec := httptest.NewRecorder()
		s.handlePreview(rec, httptest.NewRequest(http.MethodGet, "/v1/sessions/x/preview"+tc.query, nil), "x")
		var got daemon.PreviewResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); rec.Code != http.StatusOK || err != nil {
			t.Fatalf("%q: status %d: %s", tc.query, rec.Code, rec.Body)
		}
		if got.Content != tc.want {
			t.Errorf("%q: content = %q, want %q", tc.query, got.Content, tc.want)
		}
	}
	if got := lastLines("\n\n", 5); got != "" {
		t.Errorf("blank pane = %q, want empty", got)
	}
	if got := lastLines("a\nb", 5); got != "a\nb\n" {
		t.Errorf("short pane = %q", got)
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

// TestHandlePreview_ClampsLines — an over-cap ?lines= gets the cap, not
// a silent fall-back to the 24-line default (ccmux-mcp forwards up to
// 500 and used to receive 24).
func TestHandlePreview_ClampsLines(t *testing.T) {
	for _, tc := range []struct {
		q    string
		want int
	}{
		{"", 24},
		{"300", 300},
		{"500", maxPreviewLines},
		{"100000", maxPreviewLines},
		{"-3", 24},
	} {
		var got int
		s := &server{cfg: config.Config{}}
		s.capture = func(ctx context.Context, name string, lines int) (string, error) {
			got = lines
			return "ok", nil
		}
		target := "/v1/sessions/x/preview"
		if tc.q != "" {
			target += "?lines=" + tc.q
		}
		s.handlePreview(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, target, nil), "x")
		if got != tc.want {
			t.Errorf("lines=%q: captured %d, want %d", tc.q, got, tc.want)
		}
	}
}
