package cmd

import (
	"context"
	"time"

	"github.com/skzv/ccmux/internal/moshi"
	"github.com/skzv/ccmux/internal/tmux"
	"github.com/skzv/ccmux/internal/tmuxchrome"
)

// attachWithChrome applies ccmux's status-bar chrome to `session`, then
// exec's `tmux attach` (replacing this process).
//
// Every cmd/ccmux command that creates a tmux session and then attaches
// to it MUST go through here. `attach`, `new`, and `resume` each used
// to call tmux.Attach directly and skipped the chrome step — so a
// session spawned from the CLI showed vanilla green tmux instead of the
// ccmux bar, while the same session spawned from the TUI (localAttachCmd)
// or the daemon (applyChrome) looked right. Routing every CLI attach
// through one helper makes the chrome non-optional. The chrome layer is
// agent-agnostic, so this works the same for claude, codex, and
// antigravity (gemini) sessions.
//
// Chrome failure is swallowed — a missing status bar is cosmetic and
// must never block the attach. This matches the daemon's applyChrome
// and the TUI's localAttachCmd.
//
// On success this does not return: tmux.Attach replaces the process.
func attachWithChrome(session, projectLabel string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	// moshi state drives the "reachable via Moshi" badge — detected the
	// same way the TUI's localAttachCmd does. nested=false: the CLI
	// always does a standalone `attach-session`, never switch-client.
	mst := moshi.Detect(ctx)
	reachable := mst.Paired && mst.HooksInstalled && mst.ServiceRunning
	_ = tmuxchrome.Apply(ctx, session, projectLabel, reachable, false)
	cancel()
	return tmux.Attach(session, attachDetachOthers())
}
