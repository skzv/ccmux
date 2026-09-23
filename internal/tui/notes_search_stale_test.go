package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// notesProjectDir makes a temp project holding one markdown file.
func notesProjectDir(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// loadNotesProject points m at p and drives the async entry load to
// completion so the file tree is populated.
func loadNotesProject(t *testing.T, m notesModel, p project.Project) notesModel {
	t.Helper()
	cmd := m.SetProject(&p)
	if loaded, ok := findMsg[notesEntriesLoadedMsg](flattenCmd(cmd)); ok {
		m, _ = m.Update(loaded)
	}
	return m
}

// TestNotes_SetProjectClearsSearch — regression: switching project kept
// project A's search results (the H device switch cleared them, a
// project switch didn't), so the list showed A's hits under project B
// and Enter opened A's file.
func TestNotes_SetProjectClearsSearch(t *testing.T) {
	dirA := notesProjectDir(t, "alpha.md", "# alpha\n\nneedle in A\n")
	dirB := notesProjectDir(t, "beta.md", "# beta\n\nnothing here\n")

	m := newNotes(styles.Default(), DefaultKeymap())
	m = loadNotesProject(t, m, project.Project{Name: "a", Path: dirA})

	res, ok := m.runSearch("needle")().(notesSearchResultMsg)
	if !ok {
		t.Fatal("runSearch did not produce a notesSearchResultMsg")
	}
	m, _ = m.Update(res)
	if !m.hasActiveSearch() || len(m.searchResults) == 0 {
		t.Fatalf("search in project A found nothing; test setup broken (results=%d)", len(m.searchResults))
	}

	m = loadNotesProject(t, m, project.Project{Name: "b", Path: dirB})
	if m.hasActiveSearch() || len(m.searchResults) != 0 {
		t.Fatalf("project switch kept A's search (query=%q, %d hits)", m.searchQuery, len(m.searchResults))
	}
	if got := m.selectedPath(); strings.HasPrefix(got, dirA) {
		t.Errorf("selected path %q belongs to the previous project %q", got, dirA)
	}
}

// TestNotes_StaleSearchResultDropped — a search dispatched in project A
// that returns after the user moved to project B must be dropped, not
// applied to B's list.
func TestNotes_StaleSearchResultDropped(t *testing.T) {
	dirA := notesProjectDir(t, "alpha.md", "# alpha\n\nneedle in A\n")
	dirB := notesProjectDir(t, "beta.md", "# beta\n\nnothing here\n")

	m := newNotes(styles.Default(), DefaultKeymap())
	m = loadNotesProject(t, m, project.Project{Name: "a", Path: dirA})
	searchA := m.runSearch("needle") // dispatched while A is active

	m = loadNotesProject(t, m, project.Project{Name: "b", Path: dirB})
	res, ok := searchA().(notesSearchResultMsg) // lands after the switch
	if !ok {
		t.Fatal("runSearch did not produce a notesSearchResultMsg")
	}
	if len(res.Hits) == 0 {
		t.Fatal("search in project A found nothing; test setup broken")
	}
	m, _ = m.Update(res)
	if m.hasActiveSearch() || len(m.searchResults) != 0 {
		t.Fatalf("stale search from project A applied to project B (query=%q, %d hits)", m.searchQuery, len(m.searchResults))
	}
	if got := m.selectedPath(); strings.HasPrefix(got, dirA) {
		t.Errorf("Enter would open %q from the previous project", got)
	}
}
