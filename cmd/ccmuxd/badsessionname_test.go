package main

import (
	"testing"
	"time"
)

// TestBadSessionName — the names that must be rejected before reaching
// a tmux -t target. `:` selects a window/pane, `.` a pane; `/` and `\`
// are path separators. Anything else (including the c- prefix names ccmux
// generates) is allowed.
func TestBadSessionName(t *testing.T) {
	bad := []string{"a:b", "a/b", `a\b`, "c-foo:1", "win:0.1", "a.b", "tab\tname", "nl\nname", "esc\x1b[31m", "del\x7f", "$0", "$1", "$x"}
	for _, n := range bad {
		if !badSessionName(n) {
			t.Errorf("badSessionName(%q) = false, want true", n)
		}
	}
	// `#` is fine in a target: a session someone created outside ccmux
	// with a `#` in its name must stay reachable.
	good := []string{"c-foo", "myproj", "c-shell-12ab", "foo-bar_baz", "", "work#2", "café", "a$1", "@0", "%0"}
	for _, n := range good {
		if badSessionName(n) {
			t.Errorf("badSessionName(%q) = true, want false", n)
		}
	}
}

// TestBadNewSessionName — names ccmux gives a session must also avoid
// `#`, which tmux expands as a format in new-session -s and
// rename-session (TestTmuxMangledSessionNamesAreRejected pins that on a
// real tmux).
func TestBadNewSessionName(t *testing.T) {
	for _, n := range []string{"x#{session_id}", "x#(echo hi)", "work#2", "a:b", "tab\tname"} {
		if !badNewSessionName(n) {
			t.Errorf("badNewSessionName(%q) = false, want true", n)
		}
	}
	for _, n := range []string{"c-foo", "c-shell-12ab", "café"} {
		if badNewSessionName(n) {
			t.Errorf("badNewSessionName(%q) = true, want false", n)
		}
	}
}

// TestParseUsageWindow — an absurd ?window= must not turn every
// /v1/usage call into a full-history transcript walk.
func TestParseUsageWindow(t *testing.T) {
	for q, want := range map[string]time.Duration{
		"":        5 * time.Hour,
		"junk":    5 * time.Hour,
		"-1h":     5 * time.Hour,
		"24h":     24 * time.Hour,
		"876000h": maxUsageWindow,
	} {
		if got := parseUsageWindow(q); got != want {
			t.Errorf("parseUsageWindow(%q) = %v, want %v", q, got, want)
		}
	}
}
