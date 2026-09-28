package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/notes"
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

// TestNotesSearch_TimeoutShowsPartialResults — the TUI ran the local
// search with `hits, _ :=`: when its 3s budget ran out the partial hits
// were discarded (and any other error ignored), so a slow search on a
// big tree just said "(no matches)". A timed-out search now reports
// what it found, flagged as partial, and the list says so.
func TestNotesSearch_TimeoutShowsPartialResults(t *testing.T) {
	dir := notesProjectDir(t, "a.md", "# a\n\nneedle here\n")
	m := newNotes(styles.Default(), DefaultKeymap())
	m.SetSize(120, 40)
	m = loadNotesProject(t, m, project.Project{Name: "p", Host: "local", Path: dir})

	orig := notesSearchTimeout
	notesSearchTimeout = time.Nanosecond
	res, ok := m.runSearch("needle")().(notesSearchResultMsg)
	notesSearchTimeout = orig
	if !ok {
		t.Fatal("runSearch did not produce a notesSearchResultMsg")
	}
	if !res.Partial || res.Err != "" {
		t.Fatalf("timed-out search: Partial=%v Err=%q, want Partial and no error", res.Partial, res.Err)
	}

	// What the list shows for a partial result set.
	res.Hits = []notes.SearchHit{{Path: filepath.Join(dir, "a.md"), Rel: "a.md", LineNum: 3, Snippet: "needle here"}}
	m, _ = m.Update(res)
	assertPresent(t, m.View(120, 30), "timed out", "a.md:3")

	// A complete search clears the flag.
	res.Partial = false
	m, _ = m.Update(res)
	assertAbsent(t, m.View(120, 30), "timed out")
}
