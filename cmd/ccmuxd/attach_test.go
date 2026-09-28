//go:build integration

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestHandleAttach_LargePasteKeepsConnection — the attach websocket kept
// the library's 32 KiB read limit, so a paste bigger than that from the
// phone closed the connection and dropped the terminal. A 100 KiB
// message must go through and leave the connection usable.
func TestHandleAttach_LargePasteKeepsConnection(t *testing.T) {
	dir := pollSandbox(t)
	mustTmux(t, "new-session", "-d", "-s", "c-att", "-c", dir, "cat > /dev/null")

	srv := newServer(testDaemonCfg(dir))
	mux := http.NewServeMux()
	srv.routes(mux)
	httpSrv := httptest.NewServer(mux)
	defer httpSrv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpSrv.URL, "http")+"/v1/sessions/c-att/attach", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	// Ping needs a concurrent reader to receive the pong; PTY output is
	// discarded.
	readDone := make(chan error, 1)
	go func() {
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				readDone <- err
				return
			}
		}
	}()

	paste := strings.Repeat(strings.Repeat("x", 99)+"\n", 1024) // ~100 KiB
	if err := conn.Write(ctx, websocket.MessageBinary, []byte(paste)); err != nil {
		t.Fatalf("write paste: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
	defer pingCancel()
	if err := conn.Ping(pingCtx); err != nil {
		select {
		case rerr := <-readDone:
			t.Fatalf("connection closed after a 100 KiB paste: %v (read: %v)", err, rerr)
		default:
			t.Fatalf("connection unusable after a 100 KiB paste: %v", err)
		}
	}
}
