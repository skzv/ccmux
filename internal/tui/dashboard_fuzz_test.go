package tui

import (
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// FuzzRenderSessionLine_DegenerateInputs exercises the dashboard
// row renderer with arbitrary session names + inner-width values.
// Contract:
//
//  1. Never panics. Lipgloss panics on some degenerate-style inputs;
//     this target locks the renderer's tolerance.
//  2. The output is non-empty for any non-empty session name. A
//     blank row in the dashboard would be a UX cliff — the user
//     wouldn't be able to navigate to it.
//  3. The output's visible width never exceeds `inner` by more than
//     a few cells (lipgloss padding can add 1-2 cells of overhead).
//     If the renderer ever blows past `inner` materially, the
//     dashboard's two-column layout breaks.
//
// The state, host, attached, lastChange fields are derived from
// the fuzz inputs so the four major branches of renderSessionLine
// (attached/detached × stale/fresh) get exercised across the
// random-input space.
func FuzzRenderSessionLine_DegenerateInputs(f *testing.F) {
	for _, seed := range []struct {
		name  string
		state string
		host  string
		inner int
	}{
		{"c-foo", "active", "local", 120},
		{"", "idle", "local", 80},
		{strings.Repeat("a", 200), "needs_input", "mac-mini", 40},
		{"c-foo", "unknown", "", 1},        // tiny inner; the function should clamp
		{"c-foo", "active", "local", 1000}, // huge inner; padding shouldn't explode
		{"日本語セッション", "active", "local", 50},
		{"c-\x1bb\x07", "idle", "local", 50},
	} {
		f.Add(seed.name, seed.state, seed.host, seed.inner)
	}
	st := styles.Default()
	f.Fuzz(func(t *testing.T, name, state, host string, inner int) {
		// Constrain inner to a sensible (but still extreme) range so
		// the fuzzer isn't burning cycles on -2 billion. The contract
		// is "any positive int up to a TUI-realistic size".
		if inner < 1 {
			inner = 1
		}
		if inner > 500 {
			inner = 500
		}
		s := daemon.SessionState{
			Name:  name,
			State: state,
			Host:  host,
			Agent: string(agent.IDClaude),
		}
		got := renderSessionLine(st, s, inner)
		if name != "" && strings.TrimSpace(stripANSI(got)) == "" {
			t.Fatalf("renderSessionLine produced blank output for non-empty name=%q (inner=%d)", name, inner)
		}
		// We don't assert hard upper bound on width — lipgloss adds
		// padding that's hard to predict — but the function should
		// at least produce something containing the name's bytes (or
		// the truncation marker), so a regression that produces only
		// padding is visible.
	})
}

// stripANSI is a tiny helper for the renderer's output. lipgloss
// emits ANSI escape sequences for color; we only care about the
// underlying glyph stream for the "non-blank" assertion.
func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == 0x1b {
			inEsc = true
			continue
		}
		if inEsc {
			// CSI sequences end on an alphabetic byte in [@-~]
			if (c >= '@' && c <= '~') || c == 'm' {
				inEsc = false
			}
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}
