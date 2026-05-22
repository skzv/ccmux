package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/notes"
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
	assertPresent(t, out, "auth-redesign")
	assertAbsent(t, out, "p: switch project", "tab: focus preview")
}

// notesWith builds a Notes model holding a fixed entry list, ready to
// render — bypasses the filesystem so the list-rendering tests are
// deterministic.
func notesWith(entries []notes.Entry, cursor int) notesModel {
	m := newNotes(styles.Default(), DefaultKeymap())
	m.project = &project.Project{Name: "ccmux", Path: "/tmp/ccmux"}
	m.entries = entries
	m.cursor = cursor
	return m
}

// TestNotes_ListsFilesOutsideDocs — the list now surfaces markdown
// anywhere in the project, grouped by folder, with the project root
// labelled explicitly so README.md / CLAUDE.md aren't headerless.
func TestNotes_ListsFilesOutsideDocs(t *testing.T) {
	m := notesWith([]notes.Entry{
		{Rel: "README.md", Dir: "", Display: "README"},
		{Rel: "CLAUDE.md", Dir: "", Display: "CLAUDE"},
		{Rel: "docs/01_Specs/00_Vision.md", Dir: "docs/01_Specs", Display: "Vision"},
		{Rel: "openspec/specs/spec.md", Dir: "openspec/specs", Display: "spec"},
	}, 0)
	out := m.renderList(70, 40, false)

	// Files from the project root and from non-docs/ folders both show.
	assertPresent(t, out, "README", "CLAUDE", "Vision", "spec")
	// Folder headers, including the explicit project-root label.
	assertPresent(t, out, "(project root)", "docs/01_Specs/", "openspec/specs/")
}

// TestNotes_LongListWindowsAroundCursor — a list longer than the pane
// is windowed: the cursor row stays visible, off-screen rows are
// dropped, and a "more" hint reports how many.
func TestNotes_LongListWindowsAroundCursor(t *testing.T) {
	var entries []notes.Entry
	for i := 0; i < 60; i++ {
		entries = append(entries, notes.Entry{
			Rel:     fmt.Sprintf("docs/file%02d.md", i),
			Dir:     "docs",
			Display: fmt.Sprintf("file%02d", i),
		})
	}
	m := notesWith(entries, 55)
	out := m.renderList(70, 20, false)

	assertNoOverflow(t, out, 70)
	// The cursor row is on screen; rows far above it are not.
	assertPresent(t, out, "file55")
	assertAbsent(t, out, "file00", "file05")
	// The "more" affordance reports the hidden rows above.
	if !strings.Contains(out, "↑") || !strings.Contains(out, "more") {
		t.Errorf("expected a scroll hint for the hidden rows:\n%s", out)
	}
}

func TestWindowLines(t *testing.T) {
	rows := make([]string, 100)
	for i := range rows {
		rows[i] = fmt.Sprintf("row%02d", i)
	}

	// Everything fits → full slice, nothing hidden.
	vis, above, below := windowLines(rows[:10], 3, 20)
	if len(vis) != 10 || above != 0 || below != 0 {
		t.Errorf("fits: len=%d above=%d below=%d, want 10/0/0", len(vis), above, below)
	}

	// Cursor near the end → window clamps to the bottom, cursor visible,
	// the hidden counts still partition the whole list.
	vis, above, below = windowLines(rows, 95, 20)
	if len(vis) != 20 {
		t.Fatalf("window size = %d, want 20", len(vis))
	}
	if above+len(vis)+below != 100 {
		t.Errorf("above+visible+below = %d, want 100", above+len(vis)+below)
	}
	if below != 0 {
		t.Errorf("cursor at end should leave nothing below, got %d", below)
	}
	if !strings.Contains(strings.Join(vis, "|"), "row95") {
		t.Errorf("cursor row95 not in window: %v", vis)
	}

	// Cursor at the top → nothing hidden above.
	vis, above, _ = windowLines(rows, 0, 20)
	if above != 0 || !strings.Contains(strings.Join(vis, "|"), "row00") {
		t.Errorf("cursor at top: above=%d window=%v", above, vis)
	}
}

func TestFolderHeader(t *testing.T) {
	if got := folderHeader(""); got != "(project root)" {
		t.Errorf("folderHeader(\"\") = %q, want (project root)", got)
	}
	if got := folderHeader("docs/01_Specs"); got != "docs/01_Specs/" {
		t.Errorf("folderHeader(docs/01_Specs) = %q, want docs/01_Specs/", got)
	}
}

func TestScrollHintText(t *testing.T) {
	if got := scrollHintText(0, 5); !strings.Contains(got, "↓ 5") || strings.Contains(got, "↑") {
		t.Errorf("below-only hint = %q", got)
	}
	if got := scrollHintText(3, 0); !strings.Contains(got, "↑ 3") || strings.Contains(got, "↓") {
		t.Errorf("above-only hint = %q", got)
	}
	if got := scrollHintText(3, 5); !strings.Contains(got, "↑ 3") || !strings.Contains(got, "↓ 5") {
		t.Errorf("both-sides hint = %q", got)
	}
}
