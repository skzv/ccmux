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
		// Realistic Claude frame borders: a corner, a `─` run, a corner.
		{"╭───╮", true},
		{"╰─────╯", true},
		{"  ╰──────────╯  ", true},
		{"╰────", true}, // border run, closing corner not drawn yet
		{"╰╯", true},    // corner pair
		{"plain text", false},
		{"╭", false},        // a lone corner is a partial redraw
		{"╰╰", false},       // the same corner twice is still one glyph
		{"│ > ╯", false},    // a corner plus stray frame glyphs is not a border
		{"│ > $", false},    // no corner — would false-positive on tree/gh/bat
		{"├── file", false}, // tree uses sharp corners, not rounded
		{"│ status │", false},
		{"$ ls -la", false},
		{"", false},
		// Shell prompts a crashed session falls back to: a corner, ONE
		// `─`, then the prompt glyph.
		{"╰─❯ ", false}, // powerlevel10k framed
		{"╰─❯                              ─╯", false}, // p10k full frame
		{"╰─$ ", false}, // oh-my-zsh bira
		{"╭─ ~/Projects/demo  main ⇡1 ···· ✔", false},
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
		// Modern prompt themes.
		{"❯ ", true},                                  // starship / pure / p10k lean
		{"❯", true},                                   // pure, no trailing space
		{"╰─❯ ", true},                                // powerlevel10k framed
		{"╰─$ ", true},                                // oh-my-zsh bira
		{"➜  demo git:(main) ✗ ", true},               // oh-my-zsh default, dirty
		{"➜  demo git:(feature/x) ", true},            // oh-my-zsh default, clean
		{"➜  ~ ", true},                               // oh-my-zsh default, no repo
		{"➜  demo git:(main) ✗ go test ./...", false}, // typing a command
		{"❯ fix the flaky poll test", false},          // Claude v2 transcript line
		{"poll ➜ classify ➜ bell", false},
		{"╰──────────╯", false}, // Claude's own frame
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

// TestClassify_CrashedToModernShellPrompt — after a crash ccmux's
// launch chain drops the pane into the user's shell. With a
// powerlevel10k framed prompt the `╰─❯ ` tail read as Claude's v1 frame
// (needs_input); with starship or oh-my-zsh it read as idle. All three
// are a crash. The fixtures are shared with internal/agent.
func TestClassify_CrashedToModernShellPrompt(t *testing.T) {
	for _, name := range []string{"claude_crashed_p10k.txt", "claude_crashed_starship.txt", "claude_crashed_ohmyzsh.txt"} {
		b, err := os.ReadFile(filepath.Join("..", "agent", "testdata", "panes", name))
		if err != nil {
			t.Fatal(err)
		}
		for _, lastChange := range []time.Time{time.Now(), time.Now().Add(-10 * time.Minute)} {
			if got := Classify(string(b), lastChange, 3*time.Second); got != StateError {
				t.Errorf("%s (lastChange %s ago) = %v, want error",
					name, time.Since(lastChange).Round(time.Second), got)
			}
		}
	}
	// p10k with a "solid" connection draws a `─` run across its first
	// line; that is not Claude's v2 input-box rule.
	solid := "Node.js v22.22.3\n\n╭─ ~/Projects/demo  main ──────────────────────────── ✔  10:42:17\n╰─❯ "
	if got := Classify(solid, time.Now(), 3*time.Second); got != StateError {
		t.Errorf("p10k solid-connection prompt = %v, want error", got)
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
