package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
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

// TestDecodeJSONBody_StalledTailStillBounded — a client that sends a
// complete JSON value and then stalls short of its Content-Length must
// not pin the connection either: lifting the deadline as soon as the
// value decodes would leave the server's post-handler drain of the tail
// blocked forever.
func TestDecodeJSONBody_StalledTailStillBounded(t *testing.T) {
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
	fmt.Fprintf(conn, "POST / HTTP/1.1\r\nHost: t\r\nContent-Type: application/json\r\nContent-Length: 512\r\n\r\n{\"a\":1}")

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 4096)
	if _, err := conn.Read(buf); err != nil {
		t.Fatalf("no response before the client gave up (%v) — the stalled tail pinned the handler", err)
	}
}

// TestDecodeJSONBody_DeadlineDoesNotOutliveDecode — the body read
// deadline stayed on the connection after decoding. net/http clears it
// itself once a body is read to EOF, but Decode stops at the end of the
// JSON value: with anything after it (a client's trailing whitespace)
// the deadline was still armed when the handler finished, the server's
// drain of the unread tail failed on it, and the keep-alive connection
// was dropped for any handler slower than the deadline.
func TestDecodeJSONBody_DeadlineDoesNotOutliveDecode(t *testing.T) {
	const d = 100 * time.Millisecond
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v map[string]any
		if err := decodeJSONBodyWithin(w, r, &v, d); err != nil {
			http.Error(w, "decode: "+err.Error(), http.StatusBadRequest)
			return
		}
		time.Sleep(3 * d) // slow work, past the body deadline
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(newHTTPServer(handler).Handler)
	defer srv.Close()

	// The value ends inside Decode's first read; the padding after it
	// is left for the server to drain.
	body := `{"a":1}` + strings.Repeat(" ", 4096)
	var reused []bool
	for range 2 {
		trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = append(reused, info.Reused) }}
		req, err := http.NewRequestWithContext(httptrace.WithClientTrace(context.Background(), trace), http.MethodPost, srv.URL, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d, want 200", resp.StatusCode)
		}
	}
	if len(reused) != 2 || !reused[1] {
		t.Errorf("second request reused the connection = %v, want true: the stale body deadline broke keep-alive", reused)
	}
}
