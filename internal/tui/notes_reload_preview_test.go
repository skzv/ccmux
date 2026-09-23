package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// TestNotes_ReloadRefreshesPreview — regression: after editing the
// selected note in $EDITOR (or pressing r) the notesReloadMsg path
// re-listed the files but refreshPreview short-circuited on "same rel,
// body already loaded", so the preview kept showing the pre-edit text.
func TestNotes_ReloadRefreshesPreview(t *testing.T) {
	dir := t.TempDir()
	note := filepath.Join(dir, "note.md")
	if err := os.WriteFile(note, []byte("# Note\n\nold body\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := newNotes(styles.Default(), DefaultKeymap())
	m.SetSize(120, 40)
	cmd := m.SetProject(&project.Project{Name: "p", Path: dir})
	loaded, ok := findMsg[notesEntriesLoadedMsg](flattenCmd(cmd))
	if !ok {
		t.Fatal("SetProject produced no notesEntriesLoadedMsg")
	}
	m, cmd = m.Update(loaded)
	pv, ok := findMsg[notesPreviewLoadedMsg](flattenCmd(cmd))
	if !ok {
		t.Fatal("entry load did not request the preview body")
	}
	m, _ = m.Update(pv)
	if !strings.Contains(m.previewSrc, "old body") {
		t.Fatalf("setup: preview = %q, want the original body", m.previewSrc)
	}

	// The user edits the note, then the editor-close reload fires.
	if err := os.WriteFile(note, []byte("# Note\n\nnew body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, cmd = m.Update(notesReloadMsg{})
	loaded, ok = findMsg[notesEntriesLoadedMsg](flattenCmd(cmd))
	if !ok {
		t.Fatal("notesReloadMsg did not re-list the project")
	}
	m, cmd = m.Update(loaded)
	if pv, ok := findMsg[notesPreviewLoadedMsg](flattenCmd(cmd)); ok {
		m, _ = m.Update(pv)
	}
	if !strings.Contains(m.previewSrc, "new body") {
		t.Errorf("preview after reload = %q, want the edited body", m.previewSrc)
	}
}
