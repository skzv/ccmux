package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/cursorusage"
	"github.com/skzv/ccmux/internal/project"
)

// newAsyncRoutingApp is newAppForTest with HOME and the Claude config
// dir sandboxed, since the Agents model reads both at construction.
func newAsyncRoutingApp(t *testing.T) App {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	a := newAppForTest(t)
	a.width, a.height = 120, 40
	return a
}

// notesProjectApp returns an App whose Projects cursor sits on a local
// project holding one note, ready for `4` to open it in Notes.
func notesProjectApp(t *testing.T) App {
	t.Helper()
	a := newAsyncRoutingApp(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ps := []project.Project{{Name: "alpha", Host: "local", Path: dir}}
	a.projectsM.SetProjects(ps)
	a.notes.SetProjects(ps)
	return a
}

// TestNotes_ListingLandsAfterSwitchingAway — press 4 on a project, then
// 1 before the listing loads, then 4 again. The listing result used to
// reach only the ACTIVE screen, so it was dropped on Sessions; and
// SetProject saw the same project on the second 4 and did nothing, so
// Notes spun on "scanning project…" forever.
func TestNotes_ListingLandsAfterSwitchingAway(t *testing.T) {
	a := notesProjectApp(t)
	a, cmd := updateApp(t, a, keyRunes("4"))
	loaded, ok := findMsg[notesEntriesLoadedMsg](flattenCmd(cmd))
	if !ok {
		t.Fatal("4 did not start a Notes listing")
	}
	a, _ = updateApp(t, a, keyRunes("1"))
	a, _ = updateApp(t, a, loaded) // lands while Sessions is active
	a, _ = updateApp(t, a, keyRunes("4"))
	if a.notes.loading {
		t.Fatal("Notes is still loading: the listing that landed on another screen was dropped")
	}
	if len(a.notes.entries) != 1 {
		t.Fatalf("Notes entries = %d, want the project's one note", len(a.notes.entries))
	}
}

// TestNotes_SetProjectRetriesALostLoad — if a listing for the current
// project never lands, opening Notes on it again must start a new one
// instead of treating the half-loaded project as current.
func TestNotes_SetProjectRetriesALostLoad(t *testing.T) {
	a := notesProjectApp(t)
	a, _ = updateApp(t, a, keyRunes("4")) // listing dispatched, result lost
	a, _ = updateApp(t, a, keyRunes("1"))
	_, cmd := updateApp(t, a, keyRunes("4"))
	if _, ok := findMsg[notesEntriesLoadedMsg](flattenCmd(cmd)); !ok {
		t.Fatal("re-opening Notes on a project whose listing never landed did not retry it")
	}
}

// TestNotes_AsyncResultsPassModals — the project picker and the note
// info panel returned early for every message, so a listing / preview
// that arrived while one was open was silently dropped.
func TestNotes_AsyncResultsPassModals(t *testing.T) {
	a := notesProjectApp(t)
	a, cmd := updateApp(t, a, keyRunes("4"))
	loaded, ok := findMsg[notesEntriesLoadedMsg](flattenCmd(cmd))
	if !ok {
		t.Fatal("4 did not start a Notes listing")
	}
	a, _ = updateApp(t, a, keyRunes("p")) // open the project picker
	if !a.notes.pickingProject {
		t.Fatal("setup: p did not open the project picker")
	}
	a, _ = updateApp(t, a, loaded)
	if a.notes.loading || len(a.notes.entries) != 1 {
		t.Fatalf("listing dropped while the picker was open (loading=%v entries=%d)", a.notes.loading, len(a.notes.entries))
	}
}

// TestCursorLoad_LandsOffScreen — the Cursor sub-tab's SQLite read
// finished while another screen was active and was dropped; with
// loading stuck on, EnsureFresh never reloaded and the tab spun forever.
func TestCursorLoad_LandsOffScreen(t *testing.T) {
	a := newAsyncRoutingApp(t)
	a.screen = ScreenAgents
	a.agentsM.active = agent.IDCursor
	a.agentsM.cursor, _ = a.agentsM.cursor.EnsureFresh()
	if !a.agentsM.cursor.loading {
		t.Fatal("setup: EnsureFresh did not start a load")
	}
	a, _ = updateApp(t, a, keyRunes("1"))
	a, _ = updateApp(t, a, cursorLoadedMsg{Err: cursorusage.ErrNotInstalled})
	if a.agentsM.cursor.loading {
		t.Fatal("cursorLoadedMsg that landed on Sessions was dropped; the Cursor tab spins forever")
	}
	if !a.agentsM.cursor.notInstalled {
		t.Error("cursor load result was not applied")
	}
}

// TestProjectAgentSwitch_LandsOffScreen — `a` on Projects writes the
// sidecar and then reports back; switching screens before the report
// landed left the Projects list showing the old agent.
func TestProjectAgentSwitch_LandsOffScreen(t *testing.T) {
	a := newAsyncRoutingApp(t)
	a.projectsM.SetProjects([]project.Project{{Name: "alpha", Host: "local", Path: "/p/alpha", Agent: agent.IDClaude}})
	a.screen = ScreenSessions
	a, _ = updateApp(t, a, projectAgentSwitchedMsg{Path: "/p/alpha", Agent: agent.IDCodex})
	if got := a.projectsM.Selected().Agent; got != agent.IDCodex {
		t.Fatalf("project agent = %q after an off-screen switch report, want codex", got)
	}
}

// TestProjectAgentSwitch_ReachesListUnderForm — the report must update
// the list even when the new-project form is open over it (the form's
// Update swallowed it).
func TestProjectAgentSwitch_ReachesListUnderForm(t *testing.T) {
	a := newAsyncRoutingApp(t)
	a.projectsM.SetProjects([]project.Project{{Name: "alpha", Host: "local", Path: "/p/alpha", Agent: agent.IDClaude}})
	a.screen = ScreenProjects
	a, _ = updateApp(t, a, keyRunes("n"))
	if a.projectsM.form == nil {
		t.Fatal("setup: n did not open the new-project form")
	}
	a, _ = updateApp(t, a, projectAgentSwitchedMsg{Path: "/p/alpha", Agent: agent.IDCodex})
	if got := a.projectsM.projects[0].Agent; got != agent.IDCodex {
		t.Fatalf("project agent = %q, want codex", got)
	}
}
