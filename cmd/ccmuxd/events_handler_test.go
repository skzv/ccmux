package main

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
)

// TestHandleEvents_StreamsFramedJSON pins the SSE wire format —
// Content-Type, the "data: " prefix, and the trailing blank line
// terminator that EventSource clients require to parse a complete
// frame. A regression here silently breaks the "live dashboard from
// another machine" CUJ; the connection stays open but no events ever
// parse.
func TestHandleEvents_StreamsFramedJSON(t *testing.T) {
	srv := &server{
		cfg:    config.Config{},
		events: daemon.NewEventBus(),
	}

	httpSrv := httptest.NewServer(http.HandlerFunc(srv.handleEvents))
	defer httpSrv.Close()

	// Plain GET — the handler streams until the client disconnects.
	// We'll close the response body to disconnect once we've seen
	// our frame.
	resp, err := http.Get(httpSrv.URL + "/v1/events")
	if err != nil {
		t.Fatalf("GET /v1/events: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}

	// Subscribe takes effect inside handleEvents; give it a brief
	// moment to register before publishing.
	time.Sleep(50 * time.Millisecond)

	srv.events.Publish(daemon.SessionEvent{
		At:   time.Now(),
		Kind: "needs_input",
		Session: daemon.SessionState{
			Name: "c-foo", Host: "local", State: "needs_input",
		},
	})

	// Read until we have one complete SSE frame (terminated by \n\n).
	// Use a deadline so a wire-format regression that produces no
	// terminator doesn't hang the test forever.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	frame, err := readSSEFrame(ctx, resp.Body)
	if err != nil {
		t.Fatalf("read SSE frame: %v", err)
	}
	if !strings.Contains(frame, "data: ") {
		t.Errorf("SSE frame missing 'data: ' prefix:\n%q", frame)
	}
	if !strings.Contains(frame, `"kind":"needs_input"`) {
		t.Errorf("SSE frame missing the encoded event JSON:\n%q", frame)
	}
	if !strings.HasSuffix(frame, "\n\n") {
		t.Errorf("SSE frame missing the \\n\\n terminator:\n%q", frame)
	}
}

// readSSEFrame reads bytes until two consecutive newlines, the SSE
// "end of event" terminator. Returns the full frame (including the
// trailing \n\n). Honors ctx — without a deadline a hung handler
// would wedge the test.
func readSSEFrame(ctx context.Context, r io.Reader) (string, error) {
	done := make(chan struct{})
	var out string
	var readErr error
	go func() {
		defer close(done)
		br := bufio.NewReader(r)
		var b strings.Builder
		// Skip ping/comment lines and accumulate until we see a frame
		// with a data: line.
		for {
			line, err := br.ReadString('\n')
			b.WriteString(line)
			if err != nil {
				readErr = err
				return
			}
			if strings.HasSuffix(b.String(), "\n\n") {
				if strings.Contains(b.String(), "data: ") {
					out = b.String()
					return
				}
				// Pure heartbeat or non-data frame; reset and keep going.
				b.Reset()
			}
		}
	}()
	select {
	case <-done:
		return out, readErr
	case <-ctx.Done():
		return out, ctx.Err()
	}
}

// TestHandleEvents_RejectsNonStreamingWriter — without an http.Flusher
// we can't deliver SSE; the handler must 500 rather than write a half-
// useful response.
func TestHandleEvents_RejectsNonStreamingWriter(t *testing.T) {
	srv := &server{cfg: config.Config{}, events: daemon.NewEventBus()}
	rec := httptest.NewRecorder()
	// nonFlusherWriter wraps the recorder so the http.Flusher type
	// assertion fails — drives the guard branch in handleEvents.
	srv.handleEvents(&nonFlusherWriter{ResponseWriter: rec}, httptest.NewRequest(http.MethodGet, "/v1/events", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 when flusher unavailable", rec.Code)
	}
}

// nonFlusherWriter wraps an http.ResponseWriter without exposing
// Flush, so handleEvents's `w.(http.Flusher)` assertion fails.
type nonFlusherWriter struct {
	http.ResponseWriter
}
