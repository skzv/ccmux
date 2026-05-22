package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"

	"github.com/coder/websocket"
	"github.com/creack/pty"
)

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

	cmd := exec.Command("tmux", "attach-session", "-t", "="+name)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "LC_ALL=C.UTF-8")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		conn.Close(websocket.StatusInternalError, "pty start failed")
		return
	}
	defer func() { _ = ptmx.Close() }()
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: 80})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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
