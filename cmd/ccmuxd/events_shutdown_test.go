package main

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/config"
)

// TestShutdownHTTP_EndsEventStreams — http.Server.Shutdown waits for
// in-flight handlers, and the SSE handler only returned when its client
// left, so SIGTERM took the full 2s shutdown timeout whenever anything
// was subscribed to /v1/events.
func TestShutdownHTTP_EndsEventStreams(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", "")
	srv := newServer(config.Defaults())
	mux := http.NewServeMux()
	srv.routes(mux)
	hs := newHTTPServer(mux)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = hs.Serve(ln) }()

	resp, err := http.Get("http://" + ln.Addr().String() + "/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)
	if line, err := br.ReadString('\n'); err != nil || !strings.HasPrefix(line, ": connected") {
		t.Fatalf("stream opened with %q, %v", line, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	srv.shutdownHTTP(ctx, nil, hs)
	if took := time.Since(start); took > time.Second {
		t.Errorf("shutdown with an SSE client connected took %s", took)
	}
	ended := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(br)
		ended <- err
	}()
	select {
	case err := <-ended:
		if err != nil {
			t.Errorf("stream didn't end cleanly: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("event stream still open after shutdown")
		_ = resp.Body.Close()
		_ = hs.Close()
	}
}
