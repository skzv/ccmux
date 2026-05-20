package tui

import (
	"testing"

	"github.com/skzv/ccmux/internal/tui/styles"
)

// TestAgents_NarrowLayout — at phone width the Agents screen keeps the
// subtab labels and the active agent's config block headings (T0/T1)
// but drops the subtab hint, the settings-file path, and the per-agent
// Keys cheatsheet (T2), with no line overflowing the terminal.
func TestAgents_NarrowLayout(t *testing.T) {
	m := newAgents(styles.Default(), DefaultKeymap())
	out := m.View(50, 60)
	assertNoOverflow(t, out, 50)
	assertPresent(t, out, "Claude", "Codex", "Antigravity", "Default model")
	assertAbsent(t, out, "switch agent", "settings:", "pick default model")
}
