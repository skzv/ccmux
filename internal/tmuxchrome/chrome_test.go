package tmuxchrome

import (
	"testing"
)

func TestPrettyKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"C-b", "Ctrl-b"},
		{"C-a", "Ctrl-a"},
		{"M-x", "Alt-x"},
		{"S-F1", "Shift-F1"},
		{"`", "`"},   // backtick remap left as-is
		{"", ""},
		{"plainkey", "plainkey"},
	}
	for _, tc := range cases {
		if got := PrettyKey(tc.in); got != tc.want {
			t.Errorf("PrettyKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestInTmux_RespectsTmuxEnv(t *testing.T) {
	// Save and restore the env hook.
	orig := envLookup
	defer func() { envLookup = orig }()

	envLookup = func(name string) string { return "" }
	if InTmux() {
		t.Error("InTmux should be false when $TMUX is empty")
	}

	envLookup = func(name string) string {
		if name == "TMUX" {
			return "/tmp/tmux-501/default,12345,0"
		}
		return ""
	}
	if !InTmux() {
		t.Error("InTmux should be true when $TMUX is set")
	}

	// Whitespace-only counts as empty.
	envLookup = func(name string) string {
		if name == "TMUX" {
			return "   "
		}
		return ""
	}
	if InTmux() {
		t.Error("InTmux should treat whitespace-only $TMUX as unset")
	}
}

// TestApply_RejectsEmptySession is the one explicit error path Apply
// has. Everything else is best-effort tmux calls we deliberately ignore.
func TestApply_RejectsEmptySession(t *testing.T) {
	if err := Apply(nil, "", "label", false, false); err == nil {
		t.Fatal("expected error for empty session name, got nil")
	}
}
