package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"
)

// attachPingInterval is how often the daemon sends a websocket ping to
// the attached client. Without active pings, a client that drops off
// (phone screen locks, NAT goes silent, network partition) leaves the
// daemon-side PTY + tmux client process alive forever. The ping with
// a deadline lets the websocket library notice the dead peer and tear
// the connection down.
const attachPingInterval = 25 * time.Second

// attachPingDeadline bounds each ping's round-trip. A miss closes the
// websocket and unblocks the read loop.
const attachPingDeadline = 10 * time.Second

// handleAttach upgrades GET /v1/sessions/{name}/attach to a WebSocket
// and bridges it to a real `tmux attach` running in a PTY — giving the
// mobile client a true interactive terminal (live streaming, real
// input, the tmux status bar, native resize) instead of polled
// capture-pane snapshots.
//
// Wire protocol:
//   - binary frame, server→client: raw PTY output bytes
//   - binary frame, client→server: raw keystroke bytes (PTY stdin)
//   - text frame,   client→server: JSON {"cols":N,"rows":N} resize
//
// Killing the spawned `tmux attach` process only detaches that client;
// the tmux session itself keeps running.
func (s *server) handleAttach(w http.ResponseWriter, r *http.Request, name string) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// The tailnet is the trust boundary, as for the rest of the
		// API — and a native client sends no Origin header anyway.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return // Accept already wrote the error response
	}
	defer conn.CloseNow()

	// Carry the request context — tmux attach gets cancelled if the
	// daemon shuts down. Previously this used exec.Command (no ctx),
	// so a daemon SIGTERM left the child running.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	cmd := exec.CommandContext(ctx, "tmux", "attach-session", "-t", "="+name)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "LC_ALL=C.UTF-8")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		conn.Close(websocket.StatusInternalError, "pty start failed")
		return
	}
	// Defer order matters: close pty (which causes the read loop to
	// EOF), then kill+wait the child. Process.Kill is a no-op when
	// CommandContext already terminated it via ctx cancellation.
	defer func() { _ = ptmx.Close() }()
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: 80})

	// Keep-alive: ping the client periodically. A failed ping cancels
	// the request context, which closes the PTY and unwedges any
	// goroutine blocked on a read.
	go func() {
		t := time.NewTicker(attachPingInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				pingCtx, pingCancel := context.WithTimeout(ctx, attachPingDeadline)
				err := conn.Ping(pingCtx)
				pingCancel()
				if err != nil {
					log.Printf("ccmuxd: attach %s: ping failed: %v", name, err)
					cancel()
					return
				}
			}
		}
	}()

	// PTY → WebSocket.
	go func() {
		defer cancel()
		buf := make([]byte, 8192)
		for {
			n, rerr := ptmx.Read(buf)
			if n > 0 {
				if werr := conn.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					return
				}
			}
			if rerr != nil {
				return
			}
		}
	}()

	// WebSocket → PTY.
	for {
		typ, data, rerr := conn.Read(ctx)
		if rerr != nil {
			return
		}
		switch typ {
		case websocket.MessageBinary:
			if _, werr := ptmx.Write(data); werr != nil {
				return
			}
		case websocket.MessageText:
			var ctrl struct {
				Cols int `json:"cols"`
				Rows int `json:"rows"`
			}
			if json.Unmarshal(data, &ctrl) == nil && ctrl.Cols > 0 && ctrl.Rows > 0 {
				_ = pty.Setsize(ptmx, &pty.Winsize{
					Rows: uint16(ctrl.Rows),
					Cols: uint16(ctrl.Cols),
				})
			}
		}
	}
}
