package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/claude"
)

// FuzzClaudeRulesAgreeWithFallback — Claude's state comes from two
// classifiers that must never contradict each other: the rule file
// (internal/agentdetect/rules/claude.toml, via the engine) and the
// legacy body classifier in internal/claude, which the engine falls
// back to whenever no rule matches and which Claude.Classify still
// calls directly. They are separate encodings of the same prompt shapes
// (regexes vs Go line checks), so a shape edited in one and not the
// other drifts silently: TestClaudeFallbackAgreesWithRules only checks
// the handful of pane fixtures. Its first run found the two reading
// different last lines — internal/claude trimmed the pane's trailing
// blanks first, so a selector row `❯ 1. ` was a dialog (needs_input) to
// the rules and plain output (idle) to the fallback. Both seeds are
// under testdata/fuzz/.
//
// The title is left empty (the OSC-title rule has no legacy
// counterpart), and lastChange sits far from the idle threshold on
// either side so the two time.Since calls can't straddle it.
func FuzzClaudeRulesAgreeWithFallback(f *testing.F) {
	rule := strings.Repeat("─", 40)
	for _, seed := range []string{
		"",
		"plain output",
		"some output\n╭──────────╮\n│ >        │\n╰──────────╯",
		"some output\n╰",
		"out\n" + rule + "\n❯ \n" + rule + "\n  ⏵⏵ auto mode on (shift+tab to cycle)",
		"out\n" + rule + "\n❯ \n" + rule + "\n  ? for shortcuts\n  opus · ~/Projects/ccmux · ctx 42%",
		" Do you want to proceed?\n ❯ 1. Yes\n   2. No",
		"Node.js v22.22.3\nuser@host ~ % ",
		"Node.js v22.22.3\n\n╭─ ~/Projects/demo  main ⇡1 ···· ✔  10:42:17\n╰─❯ ",
		"Node.js v22.22.3\n\ndemo on main via go\n❯ ",
		"Node.js v22.22.3\n➜  demo git:(main) ✗ ",
		"╭─dev@mbp ~/Projects/demo ‹main›\n╰─$ ",
	} {
		f.Add(seed)
	}
	if entries, err := os.ReadDir(filepath.Join("testdata", "panes")); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "claude_") && filepath.Ext(e.Name()) == ".txt" {
				if b, err := os.ReadFile(filepath.Join("testdata", "panes", e.Name())); err == nil {
					f.Add(string(b))
				}
			}
		}
	}
	const idle = 3 * time.Second
	f.Fuzz(func(t *testing.T, pane string) {
		for _, lastChange := range []time.Time{time.Now().Add(-time.Hour), time.Now().Add(time.Hour)} {
			engine := (Claude{}).ClassifyWithTitle(pane, "", lastChange, idle)
			legacy := State(claude.Classify(pane, lastChange, idle))
			if engine != legacy {
				t.Fatalf("pane %q (quiet=%v): rules say %v, internal/claude says %v",
					pane, lastChange.Before(time.Now()), engine, legacy)
			}
		}
	})
}
