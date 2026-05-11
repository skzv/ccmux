// Package tmuxchrome configures the tmux status bar of a session before
// the user attaches to it, so the attached session feels like part of
// ccmux instead of a plain tmux. Pure tmux options — no embedded pty,
// no terminal hijacking. Works on every tmux that supports set-option.
//
// The bar shows three things:
//
//   1. The project / session name on the left in ccmux's brand color
//      (Catppuccin mauve).
//   2. A "prefix + d to return to ccmux" hint in the middle so the
//      detach keybinding is always visible.
//   3. A "📱 reachable via Moshi" indicator on the right, only when
//      moshi-hook is paired so the user knows the phone can pick up
//      this session seamlessly.
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
// `moshiReachable` indicates whether moshi-hook is paired and connected;
// it controls whether the "reachable via Moshi" badge is rendered.
func Apply(ctx context.Context, session, projectLabel string, moshiReachable bool) error {
	if session == "" {
		return fmt.Errorf("tmuxchrome: session name required")
	}
	if projectLabel == "" {
		projectLabel = session
	}

	moshiBadge := ""
	if moshiReachable {
		moshiBadge = fmt.Sprintf("#[fg=%s] 📱 reachable via Moshi ", good)
	} else {
		moshiBadge = fmt.Sprintf("#[fg=%s] phone: not paired (ccmux moshi-setup) ", fgMuted)
	}

	statusLeft := fmt.Sprintf(
		"#[bg=%s,fg=%s,bold] ccmux #[bg=%s,fg=%s] %s ",
		accent, bg, accentBG, accent, projectLabel,
	)
	statusRight := fmt.Sprintf(
		"#[fg=%s]prefix + d to return to ccmux %s",
		fgMuted, moshiBadge,
	)

	opts := [][]string{
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
	}

	for _, kv := range opts {
		args := append([]string{"set-option", "-t", session, "-q", kv[0]}, kv[1])
		cmd := exec.CommandContext(ctx, "tmux", args...)
		// We intentionally ignore individual errors; a partial chrome is
		// fine, vanilla is fine too. Only a fully broken tmux call needs
		// to surface.
		_ = cmd.Run()
	}
	return nil
}

// Reset restores tmux's per-session options to the global default. Called
// when ccmux kills or "releases" a session so the chrome doesn't linger
// if the user later attaches to it from a non-ccmux client.
func Reset(ctx context.Context, session string) error {
	opts := []string{
		"status-position", "status-style", "status-left", "status-right",
		"status-left-length", "status-right-length",
		"window-status-current-style", "window-status-style",
		"status-interval",
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
