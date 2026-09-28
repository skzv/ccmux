package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/skzv/ccmux/internal/tmux"
)

// failingTmux installs a `tmux` shim that prints msg to stderr and
// exits 1, the way tmux reports a missing session.
func failingTmux(t *testing.T, msg string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("sh-script tmux fake is unix-only")
	}
	dir := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\necho %q >&2\nexit 1\n", msg)
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

// serve runs one request through the daemon's routes.
func serve(s *server, method, path, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	s.routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

// TestItemHandlers_MissingSessionIs404 — kill, rename and send-keys on
// a session that doesn't exist answered 500, indistinguishable from a
// real tmux failure.
func TestItemHandlers_MissingSessionIs404(t *testing.T) {
	for _, stderr := range []string{"can't find session: nope", "no server running on /tmp/tmux-501/default"} {
		failingTmux(t, stderr)
		s := newPollTestServer(t)
		s.kill, s.rename, s.capture = tmux.Kill, tmux.Rename, tmux.CapturePane
		for path, body := range map[string]string{
			"/v1/sessions/nope/kill":      "",
			"/v1/sessions/nope/rename":    `{"name":"other"}`,
			"/v1/sessions/nope/send-keys": `{"keys":"hi"}`,
			"/v1/sessions/nope/preview":   "",
		} {
			method := http.MethodPost
			if strings.HasSuffix(path, "/preview") {
				method = http.MethodGet
			}
			if rec := serve(s, method, path, body); rec.Code != http.StatusNotFound {
				t.Errorf("%s (%s): status %d (%s), want 404", path, stderr, rec.Code, rec.Body)
			}
		}
	}
	// Any other failure stays a 500.
	failingTmux(t, "server exited unexpectedly")
	s := newPollTestServer(t)
	s.kill = tmux.Kill
	if rec := serve(s, http.MethodPost, "/v1/sessions/x/kill", ""); rec.Code != http.StatusInternalServerError {
		t.Errorf("other kill failure: status %d, want 500", rec.Code)
	}
}

// TestHandleRename_OntoExistingNameIs409 — renaming onto a name another
// session has answered 500 "duplicate session".
func TestHandleRename_OntoExistingNameIs409(t *testing.T) {
	s := newPollTestServer(t)
	s.rename = func(context.Context, string, string) error {
		return errors.New("tmux rename-session: exit status 1 (duplicate session: taken)")
	}
	rec := serve(s, http.MethodPost, "/v1/sessions/old/rename", `{"name":"taken"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"taken"`) {
		t.Errorf("status %d body %q, want 409 naming the taken name", rec.Code, rec.Body)
	}
}

// TestListSessions_ErrorNamesTheCommandOnce — the list error body read
// "tmux list-sessions: tmux list-sessions: …".
func TestListSessions_ErrorNamesTheCommandOnce(t *testing.T) {
	s := newPollTestServer(t)
	s.list = func(context.Context) ([]tmux.Session, error) {
		return nil, errors.New("tmux list-sessions: exit status 1 (permission denied)")
	}
	rec := serve(s, http.MethodGet, "/v1/sessions", "")
	if rec.Code != http.StatusInternalServerError || strings.Count(rec.Body.String(), "tmux list-sessions") != 1 {
		t.Errorf("status %d body %q, want a 500 naming the command once", rec.Code, rec.Body)
	}
}

// TestHealthAndEvents_RejectOtherMethods — /v1/health and /v1/events
// answered any method.
func TestHealthAndEvents_RejectOtherMethods(t *testing.T) {
	s := newPollTestServer(t)
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodPost, "/v1/health", http.StatusMethodNotAllowed},
		{http.MethodDelete, "/v1/health", http.StatusMethodNotAllowed},
		{http.MethodGet, "/v1/health", http.StatusOK},
		{http.MethodHead, "/v1/health", http.StatusOK},
		{http.MethodPost, "/v1/events", http.StatusMethodNotAllowed},
		{http.MethodPut, "/v1/events", http.StatusMethodNotAllowed},
	} {
		// Bounded, so an event stream that (wrongly) opens ends too.
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		mux := http.NewServeMux()
		s.routes(mux)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, tc.method, tc.path, nil))
		cancel()
		if rec.Code != tc.want {
			t.Errorf("%s %s: status %d, want %d", tc.method, tc.path, rec.Code, tc.want)
		}
	}
}

// TestDecodeJSONBody_RejectsTrailingData — a body with a second JSON
// value or garbage after the first was accepted (only the first value
// counted). Trailing whitespace is still fine.
func TestDecodeJSONBody_RejectsTrailingData(t *testing.T) {
	decode := func(body string) error {
		var v map[string]any
		rec := httptest.NewRecorder()
		return decodeJSONBodyWithin(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)), &v, time.Second)
	}
	for _, bad := range []string{`{"name":"a"}{"name":"b"}`, `{"name":"a"} x`, `{} []`, "{}\n{}\n"} {
		if err := decode(bad); err == nil {
			t.Errorf("decode(%q) accepted trailing data", bad)
		}
	}
	for _, ok := range []string{`{"name":"a"}`, "{\"name\":\"a\"}\n", "{} \t\r\n  "} {
		if err := decode(ok); err != nil {
			t.Errorf("decode(%q) = %v", ok, err)
		}
	}
	// Through a handler: a rename with a smuggled second object is a 400.
	s := newPollTestServer(t)
	s.rename = func(context.Context, string, string) error { return nil }
	if rec := serve(s, http.MethodPost, "/v1/sessions/old/rename", `{"name":"a"}{"name":"b"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("rename with trailing data: status %d, want 400", rec.Code)
	}
}

// TestHandleAttach_MissingSessionIs404 — attaching to a session that
// doesn't exist upgraded to a WebSocket (101) and then streamed tmux's
// "can't find session" into the terminal.
func TestHandleAttach_MissingSessionIs404(t *testing.T) {
	fakeTmuxLog(t)
	s := newPollTestServer(t)
	s.has = func(context.Context, string) (bool, error) { return false, nil }
	mux := http.NewServeMux()
	s.routes(mux)
	httpSrv := httptest.NewServer(mux)
	defer httpSrv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpSrv.URL, "http")+"/v1/sessions/nope/attach", nil)
	if err == nil {
		conn.CloseNow()
		t.Fatal("attach to a missing session upgraded to a WebSocket")
	}
	if resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Errorf("attach to a missing session: response %v (err %v), want 404", resp, err)
	}
}
