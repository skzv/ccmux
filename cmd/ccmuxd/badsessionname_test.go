package main

import "testing"

// TestBadSessionName — the names that must be rejected before reaching
// a tmux -t target. `:` selects a window/pane; `/` and `\` are path
// separators. Anything else (including the c- prefix names ccmux
// generates) is allowed.
func TestBadSessionName(t *testing.T) {
	bad := []string{"a:b", "a/b", `a\b`, "c-foo:1", "win:0.1"}
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
