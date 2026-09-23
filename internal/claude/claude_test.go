package claude

import (
	"os"
	"path/filepath"
	"strings"
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
		// Realistic Claude frame: corners + horizontal + vertical.
		{"╭───╮", true},
		// A line with a corner glyph plus the > cursor.
		{"│ > ╯", true},
		// Single rounded corner glyph at the bottom of the box.
		{"╰─────╯", true},
		{"plain text", false},
		{"╭", false},        // only 1 hit, even with a corner
		{"│ > $", false},    // no corner — would false-positive on tree/gh/bat
		{"├── file", false}, // tree uses sharp corners, not rounded
		{"│ status │", false},
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
		// Claude tail lines really end in `│` or `╯`, never `$`/`#`/`%`,
		// so a Claude pane can't be misread as a shell prompt by tail
		// suffix alone — and looksLikeClaudePrompt still vetoes the
		// corner cases.
		{"│ > ╯", false},
		{"plain text", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := looksLikeShellPrompt(tc.line); got != tc.want {
			t.Errorf("looksLikeShellPrompt(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

// TestClassify_ClaudeCodeV2InputBox — Claude Code v2 draws its input
// box as a `❯` line between two `────` rules with a footer below, no
// rounded corners; the tail-line-only heuristic never recognised it, so
// a waiting v2 session idled out as "idle" instead of needs_input. The
// fixture is a real 2.1.281 capture shared with internal/agent.
func TestClassify_ClaudeCodeV2InputBox(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "agent", "testdata", "panes", "claude_v2_idle.txt"))
	if err != nil {
		t.Fatal(err)
	}
	pane := string(b)
	if got := Classify(pane, time.Now().Add(-10*time.Minute), 3*time.Second); got != StateNeedsInput {
		t.Errorf("quiet v2 input box = %v, want needs_input", got)
	}
	if got := Classify(pane, time.Now(), 3*time.Second); got != StateActive {
		t.Errorf("fresh v2 input box = %v, want active (idle gate)", got)
	}
}

// TestClassify_PercentFooterIsNotAShell — Claude's own footer or a
// statusline can end in `%`; with Claude chrome on screen that tail is
// not a crashed-to-shell prompt.
func TestClassify_PercentFooterIsNotAShell(t *testing.T) {
	rule := strings.Repeat("─", 80)
	for _, pane := range []string{
		"done\n" + rule + "\n❯ \n" + rule + "\n  ⏵⏵ auto mode on (shift+tab to cycle)    Context left until auto-compact: 7%",
		"done\n" + rule + "\n❯ \n" + rule + "\n  ? for shortcuts\n  opus · ~/Projects/ccmux · ctx 42%",
		claudeFrame + "\n  ? for shortcuts   Context left until auto-compact: 7%",
	} {
		for _, lastChange := range []time.Time{time.Now(), time.Now().Add(-10 * time.Minute)} {
			if got := Classify(pane, lastChange, 3*time.Second); got == StateError {
				t.Errorf("Classify(%q) = error; a %% footer under Claude chrome is not a shell prompt", pane)
			}
		}
	}
	shell := "Error: Cannot find module 'cli.js'\n\nNode.js v22.22.3\nuser@host ~ % "
	if got := Classify(shell, time.Now(), 3*time.Second); got != StateError {
		t.Errorf("real zsh prompt = %v, want error", got)
	}
}

func TestLooksLikeClaudeV2Dialog(t *testing.T) {
	cases := []struct {
		lines []string
		want  bool
	}{
		{[]string{" Do you want to proceed?", " ❯ 1. Yes", "   2. No"}, true},
		{[]string{" Edit file", "   1. Yes", " ❯ 2. Yes, allow all edits", "   3. No"}, true},
		{[]string{" ❯ No, exit", "   Yes, I trust this folder", " Enter to confirm · Esc to cancel"}, true},
		{[]string{"1. a numbered list", "2. in plain output"}, false},
		{[]string{"press Esc to cancel the build"}, false},
	}
	for _, tc := range cases {
		if got := looksLikeClaudeV2Dialog(tc.lines); got != tc.want {
			t.Errorf("looksLikeClaudeV2Dialog(%q) = %v, want %v", tc.lines, got, tc.want)
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
