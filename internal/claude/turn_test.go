package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func paneFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "agent", "testdata", "panes", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// typed is the idle pane with text typed into its input box.
func typed(idle, text string) string {
	return strings.Replace(idle, "❯", "❯ "+text, 1)
}

// TestReadTurn pins what ReadTurn sees in real Claude Code captures.
func TestReadTurn(t *testing.T) {
	working := paneFixture(t, "claude_v2_working.txt")
	idle := paneFixture(t, "claude_v2_idle.txt")
	done := paneFixture(t, "claude_v2_statusline.txt")
	for _, tc := range []struct {
		name       string
		pane       string
		hasInput   bool
		busy       bool
		outputTail string // the last line of Output
	}{
		{"working: status line above the box", working, true, true, "✻ Cogitating… (12s · ↓ 1.2k tokens · esc to interrupt)"},
		{"waiting at the box", idle, true, false, "  Get to finished work sooner with Opus 5.5. Switch anytime with /model."},
		{"turn done, statusline footer", done, true, false, "⏺ Done."},
		{"multi-line prompt typed", paneFixture(t, "claude_v2_typed_multiline.txt"), true, false, "⏺ Done."},
		{"tool permission dialog", paneFixture(t, "claude_v2_permission.txt"), true, false, "⏺ Bash(rm -rf build/)"},
		{"trust dialog, nothing above it", paneFixture(t, "claude_v2_trust.txt"), true, false, ""},
		{"v1 rounded frame", "done.\n╭──────────╮\n│ > hi     │\n╰──────────╯", true, false, "done."},
		{"crashed to a shell", paneFixture(t, "claude_crashed_shell.txt"), false, false, ""},
		{"p10k prompt is not a frame", paneFixture(t, "claude_crashed_p10k.txt"), false, false, ""},
		{"empty", "", false, false, ""},
		// Killed mid-turn: the last frame stays on screen with the shell
		// printing under it. The status line in it is frozen.
		{"frozen frame above a shell prompt", working + "\nzsh: killed     claude\nuser@host ~ % ", true, false, "✻ Cogitating… (12s · ↓ 1.2k tokens · esc to interrupt)"},
		{"interrupt hint in the footer", typed(idle, "") + "\n  esc to interrupt", true, true, "  Get to finished work sooner with Opus 5.5. Switch anytime with /model."},
		{"answer mentioning the key is not a status line", strings.Replace(idle, "  Get to finished", "⏺ Press esc to interrupt me (any time).\n  Get to finished", 1), true, false, "  Get to finished work sooner with Opus 5.5. Switch anytime with /model."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ReadTurn(tc.pane)
			if got.HasInput != tc.hasInput || got.Busy != tc.busy {
				t.Errorf("HasInput=%v Busy=%v, want %v/%v", got.HasInput, got.Busy, tc.hasInput, tc.busy)
			}
			lines := strings.Split(got.Output, "\n")
			if last := lines[len(lines)-1]; last != tc.outputTail {
				t.Errorf("Output ends %q, want %q", last, tc.outputTail)
			}
		})
	}
}

// TestReadTurn_TypingLeavesOutputAlone — typing into the input box, a
// prompt that wraps onto more lines, and the footer changing leave
// Output as it was; Claude printing something doesn't.
func TestReadTurn_TypingLeavesOutputAlone(t *testing.T) {
	idle := paneFixture(t, "claude_v2_idle.txt")
	base := ReadTurn(idle).Output
	for name, pane := range map[string]string{
		"a word":         typed(idle, "fix"),
		"a sentence":     typed(idle, "fix the flaky poll test"),
		"several lines":  typed(idle, "first line\n  second line\n  third line"),
		"footer repaint": strings.Replace(idle, "auto mode on", "accept edits on", 1),
	} {
		if got := ReadTurn(pane).Output; got != base {
			t.Errorf("%s: Output changed:\n%s\nwant\n%s", name, got, base)
		}
	}
	// A long transcript: a box grown by a typed line scrolls the screen,
	// and the capture window loses its top line.
	box := idle[strings.Index(idle, "────"):]
	var transcript strings.Builder
	for i := range 30 {
		transcript.WriteString("⏺ output line " + strings.Repeat("x", i) + "\n")
	}
	long := transcript.String() + "\n" + box
	scrolled := long[strings.Index(long, "\n")+1:]
	scrolled = typed(scrolled, "first line\n  second line")
	if a, b := ReadTurn(long).Output, ReadTurn(scrolled).Output; a != b {
		t.Errorf("scrolling the top line out of the capture changed Output:\n%s\nvs\n%s", a, b)
	}
	answered := strings.Replace(idle, "  Get to finished", "❯ hi\n\n⏺ Hello!\n\n  Get to finished", 1)
	if ReadTurn(answered).Output == base {
		t.Error("Claude's answer above the box left Output unchanged")
	}
	if a, b := ReadTurn(paneFixture(t, "claude_v2_statusline.txt")).Output, ReadTurn(paneFixture(t, "claude_v2_typed_multiline.txt")).Output; a != b {
		t.Errorf("a multi-line prompt typed into the same session changed Output:\n%s\nvs\n%s", a, b)
	}
}

// FuzzReadTurn — ReadTurn runs on every Claude session's capture each
// poll tick, so no capture may crash it, and its answer must stay
// coherent: Busy and Output only with an input area, and Output never
// longer than its tail.
func FuzzReadTurn(f *testing.F) {
	rule := strings.Repeat("─", 40)
	for _, seed := range []string{
		"",
		"\n\n\n",
		"out\n" + rule + "\n❯ \n" + rule + "\n  ? for shortcuts",
		"✻ Cogitating… (1s · esc to interrupt)\n" + rule + "\n❯ \n" + rule + "\n  ⏵⏵ auto mode on",
		"✻ Cogitating… (1s · esc to interrupt)\n" + rule + "\n❯ \n" + rule + "\nuser@host ~ % ",
		rule + "\n Do you want to proceed?\n ❯ 1. Yes",
		"done.\n╭──────────╮\n│ > hi     │\n╰──────────╯",
		rule + "\n❯",
		" " + rule + "\n ❯ x\n" + rule + "\n ",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, pane string) {
		got := ReadTurn(pane)
		if !got.HasInput && (got.Busy || got.Output != "") {
			t.Fatalf("no input area but %+v", got)
		}
		if n := strings.Count(got.Output, "\n") + 1; n > outputTailLines {
			t.Fatalf("Output has %d lines, more than %d", n, outputTailLines)
		}
	})
}
