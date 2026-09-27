package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// notesPlaceFixture is a project with a root note, two notes in docs/
// and a deeper folder, loaded into a Notes model at a split-layout size.
func notesPlaceFixture(t *testing.T) (notesModel, string) {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range map[string]string{
		"README.md":        "# Readme\n",
		"docs/alpha.md":    "# Alpha\n",
		"docs/beta.md":     "# Beta\n\nbeta body\n",
		"docs/sub/deep.md": "# Deep\n",
	} {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := newNotes(styles.Default(), DefaultKeymap())
	m.SetSize(120, 40)
	cmd := m.SetProject(&project.Project{Name: "p", Path: dir})
	loaded, ok := findMsg[notesEntriesLoadedMsg](flattenCmd(cmd))
	if !ok {
		t.Fatal("SetProject produced no notesEntriesLoadedMsg")
	}
	m, _ = m.Update(loaded)
	return m, dir
}

// reloadNotes runs the editor-close reload end to end and lands the
// fresh listing plus the preview body.
func reloadNotes(t *testing.T, m notesModel) notesModel {
	t.Helper()
	m, cmd := m.Update(notesReloadMsg{})
	loaded, ok := findMsg[notesEntriesLoadedMsg](flattenCmd(cmd))
	if !ok {
		t.Fatal("notesReloadMsg did not re-list the project")
	}
	m, cmd = m.Update(loaded)
	if pv, ok := findMsg[notesPreviewLoadedMsg](flattenCmd(cmd)); ok {
		m, _ = m.Update(pv)
	}
	return m
}

// selectNote moves the cursor onto the visible file row for rel.
func selectNote(t *testing.T, m notesModel, rel string) notesModel {
	t.Helper()
	for i, r := range m.visibleRows() {
		if r.kind == rowFile && m.entries[r.entryIdx].Rel == rel {
			m.cursor = i
			return m
		}
	}
	t.Fatalf("note %q is not visible", rel)
	return m
}

// TestNotes_EditReloadKeepsPlace — editing a note (`e`, then the editor
// closes and the project is re-listed) used to collapse every folder and
// keep only the cursor index, so the cursor landed on another row and
// the preview said "No selection". Expansion and selection must survive.
func TestNotes_EditReloadKeepsPlace(t *testing.T) {
	m, dir := notesPlaceFixture(t)
	m.expanded["docs"] = true
	m = selectNote(t, m, "docs/beta.md")

	// Another note appears ahead of the selection while the editor is
	// open (index-based cursors drift on exactly this).
	if err := os.WriteFile(filepath.Join(dir, "docs", "aaa.md"), []byte("# AAA\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m = reloadNotes(t, m)

	if !m.expanded["docs"] {
		t.Error("docs/ collapsed after the editor-close reload")
	}
	e := m.selectedEntry()
	if e == nil || e.Rel != "docs/beta.md" {
		t.Fatalf("selection after reload = %+v, want docs/beta.md", e)
	}
	out := ansi.Strip(m.View(120, 40))
	if strings.Contains(out, "No selection") || !strings.Contains(out, "beta body") {
		t.Errorf("preview after reload doesn't show the edited note:\n%s", out)
	}
}

// TestNotes_NewNoteIsRevealedAndSelected — `n` creates the note, opens
// $EDITOR, and the reload afterwards must open the new note's folder and
// put the cursor on it rather than wherever the old index points.
func TestNotes_NewNoteIsRevealedAndSelected(t *testing.T) {
	m, dir := notesPlaceFixture(t)
	m = selectNote(t, m, "README.md")

	m, cmd := m.Update(newNoteSubmitMsg{Filename: "docs/sub/fresh.md", Title: "Fresh"})
	if cmd == nil {
		t.Fatal("new-note submit produced no command")
	}
	if _, err := os.Stat(filepath.Join(dir, "docs", "sub", "fresh.md")); err != nil {
		t.Fatalf("note not created: %v", err)
	}
	m = reloadNotes(t, m)

	if !m.expanded["docs"] || !m.expanded["docs/sub"] {
		t.Errorf("new note's folders not expanded: %v", m.expanded)
	}
	if e := m.selectedEntry(); e == nil || e.Rel != "docs/sub/fresh.md" {
		t.Fatalf("selection after creating a note = %+v, want docs/sub/fresh.md", e)
	}
}

// TestNotes_PreviewHintFollowsFocus — the preview footer said "tab: focus
// list" even while the list had focus, and wasn't translatable.
func TestNotes_PreviewHintFollowsFocus(t *testing.T) {
	m, _ := notesPlaceFixture(t)
	m = selectNote(t, m, "README.md")
	if out := m.View(120, 40); !strings.Contains(out, "tab: focus preview") {
		t.Errorf("list focused: hint should offer to focus the preview:\n%s", out)
	}
	m.focus = focusPreview
	if out := m.View(120, 40); !strings.Contains(out, "tab: focus list") {
		t.Errorf("preview focused: hint should offer to focus the list:\n%s", out)
	}
}
