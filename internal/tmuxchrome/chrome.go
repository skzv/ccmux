// Package tmuxchrome configures the tmux status bar of a session before
// the user attaches to it, so the attached session feels like part of
// ccmux instead of a plain tmux. Pure tmux options — no embedded pty,
// no terminal hijacking. Works on every tmux that supports set-option.
//
// The bar shows three things:
//
//  1. The project / session name on the left in ccmux's brand color
//     (Catppuccin mauve).
//  2. A "prefix + d to return to ccmux" hint in the middle so the
//     detach keybinding is always visible.
//  3. A "📱 reachable via Moshi" indicator on the right, only when
//     moshi-hook is paired so the user knows the phone can pick up
//     this session seamlessly.
//
// All overrides are applied per-session (via `tmux set-option -t <name>`)
// so they don't leak into other tmux sessions the user has running.
package tmuxchrome

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Theme colors. Matches the TUI's Catppuccin Mocha palette so the
// embedded session feels continuous with ccmux. Centralized here so a
// future theme picker can change them in one place.
const (
	bg       = "#1e1e2e" // Catppuccin Mocha base
	bgAlt    = "#181825" // mantle
	fg       = "#cdd6f4" // text
	fgMuted  = "#7f849c" // overlay1
	accent   = "#cba6f7" // mauve
	accentBG = "#313244" // surface0
	good     = "#a6e3a1" // green
	warning  = "#f9e2af" // yellow
)

// Apply writes ccmux's chrome onto `session`. It runs a small batch of
// `tmux set-option -t <session>` calls. Failure is non-fatal: the worst
// case is the user gets vanilla tmux styling.
//
// `moshiReachable` controls the "reachable via Moshi" badge. `nested`
// indicates we got to this attach via `tmux switch-client` from inside
// an outer tmux (the persistent `ccmux` outer session in the mobile
// flow). The detach hint differs:
//
//   - Standalone (not nested): `<prefix> then d` (tmux detach-client).
//   - Nested: `<prefix> then L` (last-session, jumps back to the outer
//     ccmux session). `<prefix> then d` would close the whole client.
//
// Important wording: tmux prefix-key gestures are a SEQUENCE, not a
// combo. The user presses the prefix, releases it, then presses the
// second key. Cmd+D / Ctrl+D look like combos but are entirely
// different things — Cmd+D splits the iTerm pane, Ctrl+D sends EOF and
// exits Claude. The chrome explicitly uses "then" not "+" so this is
// unambiguous.
func Apply(ctx context.Context, session, projectLabel string, moshiReachable, nested bool) error {
	if session == "" {
		return fmt.Errorf("tmuxchrome: session name required")
	}
	prefix := DetectedPrefix(ctx)
	for _, kv := range Options(session, projectLabel, moshiReachable, nested, prefix) {
		args := append([]string{"set-option", "-t", session, "-q", kv[0]}, kv[1])
		cmd := exec.CommandContext(ctx, "tmux", args...)
		// We intentionally ignore individual errors; a partial chrome is
		// fine, vanilla is fine too. Only a fully broken tmux call needs
		// to surface.
		_ = cmd.Run()
	}
	return nil
}

// Options returns the tmux set-option key/value pairs Apply will emit
// for the given session. Pure function — no exec, no env, no tmux
// server. Split out so:
//
//  1. Callers that just want the option strings (e.g. for a
//     `--print-chrome` debug flag, or rendering a preview pane) can
//     get them without spawning anything.
//  2. Tests can pin the exact set of keys and their interpolations
//     without standing up a real tmux server.
//
// `prefix` is the human-readable prefix-key string (e.g. "Ctrl-b").
// Pass DetectedPrefix(ctx) from Apply; tests can pass any string.
func Options(session, projectLabel string, moshiReachable, nested bool, prefix string) [][]string {
	if projectLabel == "" {
		projectLabel = session
	}

	moshiBadge := ""
	if moshiReachable {
		moshiBadge = fmt.Sprintf("#[fg=%s] 📱 reachable via Moshi ", good)
	} else {
		moshiBadge = fmt.Sprintf("#[fg=%s] phone: not paired (ccmux moshi-setup) ", fgMuted)
	}

	returnHint := fmt.Sprintf("press %s then d to detach", prefix)
	if nested {
		returnHint = fmt.Sprintf("press %s then L to return to ccmux", prefix)
	}

	statusLeft := fmt.Sprintf(
		"#[bg=%s,fg=%s,bold] ccmux #[bg=%s,fg=%s] %s ",
		accent, bg, accentBG, accent, projectLabel,
	)
	statusRight := fmt.Sprintf(
		"#[fg=%s]%s %s",
		fgMuted, returnHint, moshiBadge,
	)

	return [][]string{
		{"status", "on"},
		{"status-position", "bottom"},
		{"status-interval", "5"},
		{"status-style", fmt.Sprintf("bg=%s,fg=%s", bgAlt, fg)},
		{"status-left", statusLeft},
		{"status-right", statusRight},
		{"status-left-length", "80"},
		{"status-right-length", "80"},
		// Show the window list compactly between the two flanks.
		{"window-status-current-style", fmt.Sprintf("bg=%s,fg=%s,bold", accentBG, accent)},
		{"window-status-style", fmt.Sprintf("fg=%s", fgMuted)},
		// window-size=latest: when more than one client is attached
		// (ccmux's mirror mode — laptop + phone on the same session),
		// size the window to whichever client was most recently
		// active rather than shrinking everyone to the smallest one.
		// Harmless in exclusive mode too — with a single client,
		// "latest" is just that client. Set per-session so it doesn't
		// disturb the user's other (non-ccmux) tmux sessions, and so
		// it overrides any `window-size smallest` in their ~/.tmux.conf
		// for ccmux sessions specifically.
		{"window-size", "latest"},
	}
}

// DetectedPrefix returns the human-readable form of the user's current
// tmux prefix-key binding (default Ctrl-b). Used by Apply and by the
// Sessions detail pane so the hint matches the user's actual keymap
// instead of assuming defaults. Returns "Ctrl-b" on any error.
func DetectedPrefix(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "tmux", "show-options", "-g", "prefix").Output()
	if err != nil {
		return "Ctrl-b"
	}
	// Output forms: `prefix C-b` or `prefix C-a` or `prefix \``.
	parts := strings.Fields(strings.TrimSpace(string(out)))
	if len(parts) < 2 {
		return "Ctrl-b"
	}
	return PrettyKey(parts[1])
}

// PrettyKey turns tmux's internal key notation (C-b, M-a, S-F1) into
// the human-readable form a user would type from muscle memory.
func PrettyKey(k string) string {
	switch {
	case strings.HasPrefix(k, "C-"):
		return "Ctrl-" + k[2:]
	case strings.HasPrefix(k, "M-"):
		return "Alt-" + k[2:]
	case strings.HasPrefix(k, "S-"):
		return "Shift-" + k[2:]
	}
	return k
}

// Reset restores tmux's per-session options to the global default. Called
// when ccmux kills or "releases" a session so the chrome doesn't linger
// if the user later attaches to it from a non-ccmux client.
func Reset(ctx context.Context, session string) error {
	opts := []string{
		"status-position", "status-style", "status-left", "status-right",
		"status-left-length", "status-right-length",
		"window-status-current-style", "window-status-style",
		"status-interval", "window-size",
	}
	for _, key := range opts {
		_ = exec.CommandContext(ctx, "tmux", "set-option", "-t", session, "-u", key).Run()
	}
	return nil
}

// InTmux reports whether the calling process is already inside a tmux
// session (i.e. $TMUX is set). Callers use this to choose between
// `attach-session` (from a bare terminal) and `switch-client` (when
// already inside tmux — avoids the "sessions should be nested with care"
// refusal).
func InTmux() bool {
	for _, ev := range []string{"TMUX"} {
		if v := strings.TrimSpace(envLookup(ev)); v != "" {
			return true
		}
	}
	return false
}

// envLookup is a tiny wrapper so tests can swap it.
var envLookup = func(name string) string {
	return getenv(name)
}

// getenv is split out from envLookup so we don't import os in this file
// just for one symbol; the wrapper above keeps the public surface small.
func getenv(name string) string {
	return osGetenv(name)
}

// osGetenv is replaced at link time via go:linkname avoidance — actually,
// no, simplest: just import os. (kept here as a thin shim in case we
// need to mock it later.)
var osGetenv = func(name string) string {
	// Inlined because importing "os" everywhere is fine — this file is
	// only one of two places that needs it.
	return getenvImpl(name)
}
