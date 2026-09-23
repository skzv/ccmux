package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/claude"
)

// Pane fixtures under testdata/panes/. Two are verbatim tmux captures
// of real agent CLIs; the rest are derived from them — same chrome,
// rule widths and glyphs (including the non-breaking space Claude
// Code v2 prints after its `❯` prompt glyph) — with only the
// scenario-specific lines changed:
//
//   - claude_v2_idle.txt            real: Claude Code 2.1.281 waiting at its input box
//   - opencode_networking_idle.txt  real: OpenCode 1.16.2 idle in a dir named networking-lab
//   - claude_v2_working.txt         spinner line above the input box mid-turn
//   - claude_v2_context_footer.txt  footer ending in "Context left until auto-compact: 7%"
//   - claude_v2_statusline.txt      custom statusline ending in "ctx 42%"
//   - claude_v2_typed_multiline.txt multi-line prompt being typed
//   - claude_v2_permission.txt      tool-permission dialog ("Do you want to proceed?")
//   - claude_v2_trust.txt           workspace trust dialog ("Enter to confirm · Esc to cancel")
//   - claude_crashed_shell.txt      claude died, zsh prompt at the tail
//   - opencode_networking_working.txt the real OpenCode capture with its running footer
func readPaneFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "panes", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestDetectionFixtures pins what each fixture classifies as through
// the daemon's real entry point — ClassifyState, which applies the rule
// engine, the require_idle gate and the per-agent fallback — both when
// the pane has been quiet for ten minutes and when it just changed.
// A rule-file edit that silently breaks detection for a real screen
// fails here.
func TestDetectionFixtures(t *testing.T) {
	const idle = 3 * time.Second // the daemon's default idle_seconds_for_needs_input
	quiet := time.Now().Add(-10 * time.Minute)
	fresh := time.Now()
	cases := []struct {
		fixture string
		agent   ID
		title   string
		quiet   bool
		want    State
	}{
		// Claude Code v2: the input box means "waiting" once quiet …
		{"claude_v2_idle.txt", IDClaude, "", true, StateNeedsInput},
		{"claude_v2_idle.txt", IDClaude, "✳ Claude Code", true, StateNeedsInput},
		// … and only once quiet: the box stays drawn while Claude works.
		{"claude_v2_idle.txt", IDClaude, "", false, StateActive},
		{"claude_v2_working.txt", IDClaude, "", false, StateActive},
		{"claude_v2_working.txt", IDClaude, "⠐ Fix flaky poll test", false, StateActive},
		{"claude_v2_working.txt", IDClaude, "⠐ Fix flaky poll test", true, StateActive},
		{"claude_v2_typed_multiline.txt", IDClaude, "", true, StateNeedsInput},
		{"claude_v2_typed_multiline.txt", IDClaude, "", false, StateActive},
		// A `%`-terminated footer or statusline is not a crashed shell.
		{"claude_v2_context_footer.txt", IDClaude, "", true, StateNeedsInput},
		{"claude_v2_context_footer.txt", IDClaude, "", false, StateActive},
		{"claude_v2_statusline.txt", IDClaude, "", true, StateNeedsInput},
		{"claude_v2_statusline.txt", IDClaude, "", false, StateActive},
		// v2 dialogs block on the user, behind the same idle gate.
		{"claude_v2_permission.txt", IDClaude, "", true, StateNeedsInput},
		{"claude_v2_permission.txt", IDClaude, "", false, StateActive},
		{"claude_v2_trust.txt", IDClaude, "", true, StateNeedsInput},
		{"claude_v2_trust.txt", IDClaude, "", false, StateActive},
		// A real shell prompt at the tail is still a crash.
		{"claude_crashed_shell.txt", IDClaude, "", true, StateError},
		{"claude_crashed_shell.txt", IDClaude, "", false, StateError},

		// OpenCode (and its fork Kilo): a cwd containing "working"
		// must not pin the session active forever.
		{"opencode_networking_idle.txt", IDOpenCode, "", true, StateNeedsInput},
		{"opencode_networking_idle.txt", IDOpenCode, "", false, StateActive},
		{"opencode_networking_idle.txt", IDKilo, "", true, StateNeedsInput},
		{"opencode_networking_working.txt", IDOpenCode, "", true, StateActive},
		{"opencode_networking_working.txt", IDKilo, "", true, StateActive},
	}
	for _, tc := range cases {
		lastChange, when := fresh, "fresh"
		if tc.quiet {
			lastChange, when = quiet, "quiet"
		}
		t.Run(string(tc.agent)+"/"+tc.fixture+"/"+when+"/title="+tc.title, func(t *testing.T) {
			pane := readPaneFixture(t, tc.fixture)
			if got := ClassifyState(ByID(tc.agent), pane, tc.title, lastChange, idle); got != tc.want {
				t.Errorf("ClassifyState = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestClaudeFallbackAgreesWithRules — internal/claude's body classifier
// is still consulted (Claude.Classify, and engineClassify whenever no
// rule matches), so it must not contradict the rule file on any Claude
// fixture: before the v2 fix the rules and the fallback disagreed on
// whether a `%` footer was a crash.
func TestClaudeFallbackAgreesWithRules(t *testing.T) {
	const idle = 3 * time.Second
	entries, err := os.ReadDir(filepath.Join("testdata", "panes"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if len(name) < len("claude_") || name[:len("claude_")] != "claude_" {
			continue
		}
		pane := readPaneFixture(t, name)
		for _, lastChange := range []time.Time{time.Now().Add(-10 * time.Minute), time.Now()} {
			engine := (Claude{}).ClassifyWithTitle(pane, "", lastChange, idle)
			fallback := State(claude.Classify(pane, lastChange, idle))
			if engine != fallback {
				t.Errorf("%s (lastChange %s ago): rules say %v, internal/claude says %v",
					name, time.Since(lastChange).Round(time.Second), engine, fallback)
			}
		}
	}
}
