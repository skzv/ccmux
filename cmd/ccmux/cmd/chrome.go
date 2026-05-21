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
	// The moshi probe drives only the cosmetic "reachable via Moshi"
	// badge, and on macOS it shells out to slow tooling. Give it its
	// own bounded context so a slow probe can't starve the chrome step
	// below — if it times out, reachable just stays false.
	mctx, mcancel := context.WithTimeout(context.Background(), 2*time.Second)
	mst := moshi.Detect(mctx)
	mcancel()
	reachable := mst.Paired && mst.HooksInstalled && mst.ServiceRunning

	// Apply chrome on a fresh, independent context: the tmux set-option
	// calls must always get their full deadline regardless of how long
	// the moshi probe took. Sharing one context with moshi.Detect is
	// what made CLI chrome flaky on macOS CI — the shared deadline
	// expired mid-probe and every set-option got cancelled, leaving the
	// session with vanilla tmux styling. nested=false: the CLI always
	// does a standalone attach-session, never switch-client.
	cctx, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = tmuxchrome.Apply(cctx, session, projectLabel, reachable, false)
	ccancel()

	return tmux.Attach(session, attachDetachOthers())
}
