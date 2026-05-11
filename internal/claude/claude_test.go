package claude

import (
	"testing"
	"time"
)

// claudeFrame mimics the bottom of Claude Code's TUI: a rounded box with
// `>` cursor. Matching only requires two box-drawing chars so this is
// realistic.
const claudeFrame = "╭─────────╮\n│ > _     │\n╰─────────╯"

func TestClassify_Unknown(t *testing.T) {
	cases := []string{"", "    ", "\n\n\t"}
	for _, pane := range cases {
		got := Classify(pane, time.Now(), 3*time.Second)
		if got != StateUnknown {
			t.Errorf("Classify(%q) = %v, want unknown", pane, got)
		}
	}
}

func TestClassify_ClaudePromptActive(t *testing.T) {
	// Pane just changed → active (not yet idle long enough to be needs_input).
	got := Classify(claudeFrame, time.Now(), 3*time.Second)
	if got != StateActive {
		t.Fatalf("fresh claude prompt: got %v, want active", got)
	}
}

func TestClassify_ClaudePromptNeedsInput(t *testing.T) {
	// Pane unchanged for longer than idleNeedsInput → needs_input.
	stale := time.Now().Add(-10 * time.Second)
	got := Classify(claudeFrame, stale, 3*time.Second)
	if got != StateNeedsInput {
		t.Fatalf("stale claude prompt: got %v, want needs_input", got)
	}
}

func TestClassify_ShellPromptIsError(t *testing.T) {
	cases := []string{
		"some old output\nsasha@laptop:~/projects/foo $",
		"line1\nline2\n#",
		"output\n% ",
	}
	for _, pane := range cases {
		got := Classify(pane, time.Now(), 3*time.Second)
		if got != StateError {
			t.Errorf("Classify(%q) = %v, want error (shell prompt)", pane, got)
		}
	}
}

func TestClassify_NonPromptActiveOrIdle(t *testing.T) {
	pane := "thinking…\nstreaming output\nmore tokens"
	// Recent change → active.
	if got := Classify(pane, time.Now(), 3*time.Second); got != StateActive {
		t.Errorf("recent non-prompt: got %v, want active", got)
	}
	// Stale → idle.
	if got := Classify(pane, time.Now().Add(-10*time.Second), 3*time.Second); got != StateIdle {
		t.Errorf("stale non-prompt: got %v, want idle", got)
	}
}

func TestLooksLikeClaudePrompt(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{"╭───╮", true},
		{"│ > ╯", true},
		{"plain text", false},
		{"╭", false},          // only 1 hit
		{"$ ls -la", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := looksLikeClaudePrompt(tc.line); got != tc.want {
			t.Errorf("looksLikeClaudePrompt(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

func TestLooksLikeShellPrompt(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{"sasha@laptop:~$", true},
		{"root@host:/#", true},
		{"prompt %", true},
		// A Claude-prompt line ending in $ or # should NOT be classified shell.
		{"│ > $", false},
		{"plain text", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := looksLikeShellPrompt(tc.line); got != tc.want {
			t.Errorf("looksLikeShellPrompt(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

// TestClassify_UsesLastNonEmptyLine — make sure trailing blank lines
// don't mask the actual prompt state.
func TestClassify_UsesLastNonEmptyLine(t *testing.T) {
	pane := claudeFrame + "\n\n\n   \n"
	got := Classify(pane, time.Now().Add(-10*time.Second), 3*time.Second)
	if got != StateNeedsInput {
		t.Fatalf("trailing-whitespace claude prompt: got %v, want needs_input", got)
	}
}
