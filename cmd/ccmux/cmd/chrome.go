package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"golang.org/x/term"

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
// detachOthers controls the tmux -d flag. Existing-session attach callers
// pass the configured attach mode; create-then-attach callers pass false so
// opening a fresh session preserves other clients.
//
// Inside tmux ($TMUX set) it switches the current client to the session
// instead of attaching — tmux refuses a nested attach-session ("sessions
// should be nested with care"), which used to fail `ccmux new`/`attach`/
// `resume` from a tmux pane AFTER the session had been created. That's
// the TUI's behavior too (attachReadyMsg.Nested), including the nested
// chrome that advertises the switch-back key instead of detach.
//
// Standalone, on success this does not return: tmux.Attach replaces the
// process.
func attachWithChrome(session, projectLabel string, detachOthers bool) error {
	applyCLIChrome(session, projectLabel)
	if tmuxchrome.InTmux() {
		return runForeground(tmuxAttachCmd(session, detachOthers, true))
	}
	return tmux.Attach(session, detachOthers)
}

// applyCLIChrome styles session's status bar the way the TUI and the
// daemon do. Failure is swallowed: it's cosmetic.
func applyCLIChrome(session, projectLabel string) {
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
	// session with vanilla tmux styling.
	cctx, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = tmuxchrome.Apply(cctx, session, projectLabel, reachable, tmuxchrome.InTmux())
	ccancel()
}

// stdinIsTTY reports whether stdin is a terminal — what `tmux
// attach-session` needs (it opens the client on stdin). A var so tests
// can choose.
var stdinIsTTY = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

// canAttachHere reports whether this process can put its terminal in a
// session: inside tmux a switch-client needs no terminal of its own;
// otherwise stdin must be one.
func canAttachHere() bool { return tmuxchrome.InTmux() || stdinIsTTY() }

// attachAfterStart hands the terminal to a session `new`, `resume` or
// `shell` just started (created=true) or found already running. With no
// terminal — a script, a pipe, cron, an agent's tool call — tmux used
// to fail with "open terminal failed: not a terminal" AFTER the session
// was up, and the command exited 1 ("ccmux: exit status 1") although
// it had done its job. Now it reports the session and how to attach to
// it, and succeeds.
//
// The session gets its chrome either way, so it looks right whenever
// (and however) it's attached later.
func attachAfterStart(w io.Writer, session, projectLabel string, detachOthers, created bool) error {
	if !canAttachHere() {
		applyCLIChrome(session, projectLabel)
		printAttachHint(w, session, created)
		return nil
	}
	return attachWithChrome(session, projectLabel, detachOthers)
}

// printAttachHint is the no-terminal report: which session is running
// and the command that attaches to it.
func printAttachHint(w io.Writer, session string, created bool) {
	if created {
		fmt.Fprintf(w, "created %s; attach with: ccmux attach %s\n", safeField(session), shellWord(session))
		return
	}
	fmt.Fprintf(w, "%s is already running; attach with: ccmux attach %s\n", safeField(session), shellWord(session))
}

// tmuxAttachCmd picks the tmux command that puts this terminal in
// session: switch-client when already inside tmux (nested), else
// attach-session. detachOthers only applies to attach-session — a
// switch moves this client and leaves every other one alone.
func tmuxAttachCmd(session string, detachOthers, nested bool) *exec.Cmd {
	if nested {
		return tmux.SwitchClientCmd(session)
	}
	return tmux.AttachCmd(session, detachOthers)
}

// runForeground runs c on this terminal and waits for it.
func runForeground(c *exec.Cmd) error {
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}
