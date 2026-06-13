package claude

import (
	"strings"
	"testing"
	"time"
)

// TestClassifyWithTitle_BrailleSpinner_OverridesIdle — the headline
// signal. A braille-spinner frame in the OSC title means "I'm
// working", and that beats the body looking otherwise idle. Without
// this, a stale prompt frame in the body could keep us reporting
// idle/needs_input while the agent is in fact actively working.
func TestClassifyWithTitle_BrailleSpinner_OverridesIdle(t *testing.T) {
	// Sweep a few braille glyphs across the working spinner block.
	for _, r := range []rune{0x2800, 0x2801, 0x2807, 0x2819, 0x281B, 0x28FF} {
		title := string(r) + " building…"
		old := time.Now().Add(-1 * time.Hour) // long idle → body would say needs_input
		got := ClassifyWithTitle(claudeFrame, title, old, 3*time.Second)
		if got != StateActive {
			t.Errorf("title %q → %v, want active (braille spinner overrides body)", title, got)
		}
	}
}

// TestClassifyWithTitle_NoTitle_FallsThroughToBody — an empty title
// must reproduce the legacy body-only behavior exactly. The poll loop
// passes "" for sessions whose title isn't set, so this is the dominant
// case until agents start broadcasting.
func TestClassifyWithTitle_NoTitle_FallsThroughToBody(t *testing.T) {
	for _, tc := range []struct {
		name string
		pane string
		old  time.Duration
		want State
	}{
		{"prompt fresh = active", claudeFrame, 0, StateActive},
		{"prompt aged = needs_input", claudeFrame, 1 * time.Hour, StateNeedsInput},
		{"empty pane = unknown", "", 0, StateUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lc := time.Now().Add(-tc.old)
			got := ClassifyWithTitle(tc.pane, "", lc, 3*time.Second)
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestClassifyWithTitle_NonBrailleTitleIgnored — a title that's just
// the cwd ("~/Projects/foo"), the shell hostname, or any non-spinner
// string must not force a state. We yield to body classification.
func TestClassifyWithTitle_NonBrailleTitleIgnored(t *testing.T) {
	for _, title := range []string{
		"~/Projects/ccmux",
		"sputnik.mini.skz.dev",
		"vim · main.go",
		" leading-space-then-text",
		"Action Required", // intentionally not strong-typed for claude — that's an agent-specific rule for Phase 3
	} {
		// Fresh-prompt body would say active; if the title were taken as
		// authoritative for "blocked"/"working" we'd see a different state.
		got := ClassifyWithTitle(claudeFrame, title, time.Now(), 3*time.Second)
		if got != StateActive {
			t.Errorf("title %q forced %v; non-spinner titles must defer to body (active)", title, got)
		}
	}
}

// TestClassifyTitle_TrimmedAndFirstRuneOnly — defensive: a braille
// glyph anywhere BUT the leading position should not match, and
// leading whitespace shouldn't either (the spinner ALWAYS leads).
// Pinning this keeps a future tweak from accidentally over-matching.
func TestClassifyTitle_TrimmedAndFirstRuneOnly(t *testing.T) {
	mid := "Doing thing ⠙ then more"
	if _, ok := classifyTitle(mid); ok {
		t.Errorf("braille mid-string should NOT match: %q", mid)
	}
	// Leading whitespace is trimmed before checking — a title with
	// a stray space before the spinner is still working.
	lead := "  ⠹ thinking"
	st, ok := classifyTitle(lead)
	if !ok || st != StateActive {
		t.Errorf("leading-whitespace braille should match active, got (%v, %v)", st, ok)
	}
}

// TestClassifyWithTitle_LongTitleNoPanic — a defensive smoke test
// against pathological input from a malicious or buggy upstream.
func TestClassifyWithTitle_LongTitleNoPanic(t *testing.T) {
	huge := strings.Repeat("⠙x", 100_000)
	_ = ClassifyWithTitle(claudeFrame, huge, time.Now(), 3*time.Second)
}
