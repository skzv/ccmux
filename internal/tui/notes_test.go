package tui

import (
	"testing"

	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// TestNotes_NarrowLayout — at phone width the Notes screen keeps its
// header (T0) but drops the inline key-hint line (T2), with no line
// overflowing the terminal.
func TestNotes_NarrowLayout(t *testing.T) {
	m := newNotes(styles.Default(), DefaultKeymap())
	m.SetProject(&project.Project{
		Name: "auth-redesign",
		Path: "/tmp/ccmux-notes-narrow-test-nonexistent",
	})
	out := m.View(50, 40)
	assertNoOverflow(t, out, 50)
	assertPresent(t, out, "auth-redesign / docs")
	assertAbsent(t, out, "p: switch project", "tab: focus preview")
}
