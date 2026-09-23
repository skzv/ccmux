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
	bad := []string{"a:b", "a/b", `a\b`, "c-foo:1", "win:0.1", "a.b"}
	for _, n := range bad {
		if !badSessionName(n) {
			t.Errorf("badSessionName(%q) = false, want true", n)
		}
	}
	good := []string{"c-foo", "myproj", "c-shell-12ab", "foo-bar_baz", ""}
	for _, n := range good {
		if badSessionName(n) {
			t.Errorf("badSessionName(%q) = true, want false", n)
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
