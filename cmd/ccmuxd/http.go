package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// maxJSONBodyBytes caps the size of an incoming JSON request body for
// every handler. The largest legitimate body on either socket is a
// device-registration payload at well under 4 KiB; the cap exists so
// a tailnet peer can't OOM the daemon by streaming gigabytes of trash
// into a Decode call.
const maxJSONBodyBytes = 64 * 1024

// jsonBodyReadTimeout bounds how long a handler waits for a request
// body to fully arrive. MaxBytesReader caps bytes, not time — without
// a read deadline a tailnet peer trickling one byte a minute pins a
// handler goroutine for as long as it cares to keep the socket open.
const jsonBodyReadTimeout = 10 * time.Second

// decodeJSONBody decodes a size-capped JSON request body with a read
// deadline on the underlying connection. Every POST handler goes
// through here so no Decode can stall past the deadline. Wraps
// decodeJSONBodyWithin so tests can drive the deadline directly.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, v any) error {
	return decodeJSONBodyWithin(w, r, v, jsonBodyReadTimeout)
}

// decodeJSONBodyWithin is decodeJSONBody with an explicit deadline.
// SetReadDeadline is best-effort: httptest recorders and other
// non-network ResponseWriters don't support it, and those paths have
// no slow socket to protect against anyway.
//
// On success the body is read to its end and the deadline lifted.
// Decode stops at the end of the JSON value, and a deadline left armed
// over an unread tail (a client's trailing whitespace) made the
// server's post-handler drain fail once a slow handler finished,
// dropping the keep-alive connection. The tail is read here, still
// under the deadline, so a client that stalls after the value can't pin
// the handler either. On failure the deadline stays armed: the server
// drains the unread body before replying, and a stalled client would
// otherwise hold that drain open forever.
//
// The tail may only be whitespace: a second value or trailing garbage
// ({"name":"a"}{"name":"b"}, {} x) is a malformed body, not a valid one
// with noise to ignore.
func decodeJSONBodyWithin(w http.ResponseWriter, r *http.Request, v any, d time.Duration) error {
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(d))
	body := http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	dec := json.NewDecoder(body)
	if err := dec.Decode(v); err != nil {
		return err
	}
	tail, err := io.ReadAll(io.MultiReader(dec.Buffered(), body))
	if err != nil {
		return err
	}
	if strings.Trim(string(tail), " \t\r\n") != "" {
		return errors.New("unexpected data after the JSON value")
	}
	_ = rc.SetReadDeadline(time.Time{})
	return nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// newHTTPServer returns an *http.Server with timeouts set. zero-value
// timeouts let a tailnet peer hold a TCP connection open forever
// (slow-loris) or stall a handler reading from a half-open body — both
// of which leak Server goroutines for the daemon's lifetime. The
// values are generous enough to cover handleAttach's long-lived
// websocket and handleEvents's SSE stream (both opt out via
// per-request hijacking / streaming flush; ReadHeaderTimeout still
// applies to the initial request line).
func newHTTPServer(h http.Handler) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// Bodies are read inside handlers. Per-handler context timeouts
		// do NOT bound a stalled body read — a handler's Decode runs
		// before its context.WithTimeout even exists. decodeJSONBody
		// sets a per-request read deadline on the connection instead;
		// every body-consuming handler goes through it.
	}
}
