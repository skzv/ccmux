package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/skzv/ccmux/internal/daemon"
)

// maxPreviewLines caps /preview's ?lines= so a caller can't make every
// request capture the full scrollback.
const maxPreviewLines = 500

// handlePreview returns the last N lines of the session's agent pane
// — the pane the poll loop classifies and /send-keys types into (see
// agentPaneID), not whichever pane happens to be active — as plain
// text. Read-only — the daemon's poll loop captures the pane every few
// seconds anyway, so this just adds a "give me current content" hook
// for clients that don't want to open the WebSocket PTY just to take a
// peek. Used by the iOS app's session detail view (and MCP read_pane)
// to show "what's on screen right now" without committing to a full
// terminal attach.
func (s *server) handlePreview(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	lines := 24
	if q := r.URL.Query().Get("lines"); q != "" {
		var n int
		if _, err := fmt.Sscanf(q, "%d", &n); err == nil && n > 0 {
			// Clamp rather than ignore: a caller asking for more than
			// the cap (ccmux-mcp advertises 500) should get the cap,
			// not a silent fall-back to the 24-line default.
			lines = min(n, maxPreviewLines)
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	out, err := s.previewCapture(ctx, name, lines)
	if err != nil {
		// tmux exits non-zero when the session is gone; map that to 404
		// so clients can distinguish "no session" from other errors.
		writeTmuxError(w, err)
		return
	}
	writeJSON(w, daemon.PreviewResponse{Lines: lines, Content: lastLines(out, lines)})
}

// previewCapture reads the session's agent pane by id. When that pane
// can't be resolved or read (it exited, or the session is gone), it
// reads the session target instead — through the capture seam (default
// tmux.CapturePane), so a missing session still maps to 404 and the
// mapping stays unit-testable with an injected failure.
func (s *server) previewCapture(ctx context.Context, name string, lines int) (string, error) {
	if s.capturePane != nil {
		if id := s.agentPaneID(ctx, name); id != "" {
			if out, err := s.capturePane(ctx, id, lines); err == nil {
				return out, nil
			}
		}
	}
	return s.capture(ctx, name, lines)
}

// lastLines keeps the last n lines of a capture-pane dump, ignoring the
// blank rows that pad out the visible screen below the last output.
// capture-pane -S -N returns N lines of scrollback plus the whole
// visible screen, so ?lines=N (and MCP read_pane) answered N + the
// screen height, while reporting "lines": N.
func lastLines(s string, n int) string {
	rows := strings.Split(s, "\n")
	end := len(rows)
	for end > 0 && strings.TrimSpace(rows[end-1]) == "" {
		end--
	}
	rows = rows[max(0, end-n):end]
	if len(rows) == 0 {
		return ""
	}
	return strings.Join(rows, "\n") + "\n"
}
