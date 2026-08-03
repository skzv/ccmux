package main

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestDecodeJSONBody_RejectsStalledBodyWithinDeadline — finding: the
// tailnet HTTP server sets only ReadHeaderTimeout, and handlers ran
// json.Decode before any deadline existed, so a peer trickling one
// byte a minute pinned a handler goroutine forever (MaxBytesReader
// caps bytes, not time). decodeJSONBody now sets a read deadline on
// the connection before decoding; a stalled body must produce an
// error response within the deadline, not an indefinite hang.
//
// Exercised over a real TCP conn (an httptest recorder has no
// connection to deadline) with the same newHTTPServer config the
// daemon uses.
func TestDecodeJSONBody_RejectsStalledBodyWithinDeadline(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v map[string]any
		if err := decodeJSONBodyWithin(w, r, &v, 300*time.Millisecond); err != nil {
			http.Error(w, "decode: "+err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	srv := newHTTPServer(handler)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	start := time.Now()
	// Headers complete, body deliberately short of Content-Length —
	// then stall, exactly like a trickling tailnet peer.
	fmt.Fprintf(conn, "POST / HTTP/1.1\r\nHost: t\r\nContent-Type: application/json\r\nContent-Length: 512\r\n\r\n{")

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("no response before the client gave up (%v) — the stalled body pinned the handler", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("response took %v — want within the read deadline (~300ms + slack)", elapsed)
	}
	if !strings.Contains(string(buf[:n]), "400") {
		t.Errorf("expected a 400 for the stalled body, got response: %q", string(buf[:n]))
	}
}
