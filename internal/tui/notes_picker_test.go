package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/skzv/ccmux/internal/project"
)

// TestNotesPicker_WorksBeforeProjectsLoad — on a fresh launch, Notes
// with no project says "Press p here to pick one", but `p` did nothing
// until the project list had arrived; and once it had, `p` opened the
// picker behind the no-project placeholder, where it was invisible and
// swallowed every key but esc (so 2 "didn't work" either). The picker
// now always shows, asks for the project list when it has none, and
// fills in when the list lands.
func TestNotesPicker_WorksBeforeProjectsLoad(t *testing.T) {
	a := newAppForTest(t)
	a.width, a.height = 80, 24
	a.screen = ScreenNotes
	if !strings.Contains(ansi.Strip(a.View()), tr("No project selected.")) {
		t.Fatalf("setup: Notes should start with no project:\n%s", ansi.Strip(a.View()))
	}

	a, cmd := updateApp(t, a, keyMsg("p"))
	if !a.notes.pickingProject {
		t.Fatal("p did not open the project picker")
	}
	out := ansi.Strip(a.View())
	if !strings.Contains(out, tr("Switch project")) || !strings.Contains(out, tr("loading projects…")) {
		t.Fatalf("picker not shown (or not loading) after p:\n%s", out)
	}
	if cmd == nil {
		t.Fatal("p with no projects asked for none")
	}
	if _, ok := cmd().(notesProjectsWantedMsg); !ok {
		t.Fatal("p with no projects didn't ask the App for them")
	}
	if _, refresh := updateApp(t, a, notesProjectsWantedMsg{}); refresh == nil {
		t.Fatal("the App didn't start a projects refresh for the picker")
	}

	dir := t.TempDir()
	a, _ = updateApp(t, a, projectsLoadedMsg{Projects: []project.Project{
		{Name: "alpha", Path: dir + "/alpha", Host: "local"},
		{Name: "beta", Path: dir + "/beta", Host: "local"},
	}})
	out = ansi.Strip(a.View())
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Fatalf("picker didn't fill in when the projects arrived:\n%s", out)
	}
	a, _ = updateApp(t, a, keyMsg("down"))
	a, _ = updateApp(t, a, tea.KeyMsg{Type: tea.KeyEnter})
	if a.notes.pickingProject || a.notes.project == nil || a.notes.project.Name != "beta" {
		t.Fatalf("enter in the picker didn't open beta (picking=%v project=%v)", a.notes.pickingProject, a.notes.project)
	}
}

// TestNotesPicker_VisibleWithoutProject — with the list already loaded
// but no project chosen, `p` opened the picker under the no-project
// placeholder: invisible, yet capturing every key.
func TestNotesPicker_VisibleWithoutProject(t *testing.T) {
	a := newAppForTest(t)
	a.width, a.height = 80, 24
	a.screen = ScreenNotes
	a.notes.SetProjects([]project.Project{{Name: "alpha", Path: t.TempDir(), Host: "local"}})
	a, _ = updateApp(t, a, keyMsg("p"))
	out := ansi.Strip(a.View())
	if !strings.Contains(out, tr("Switch project")) || !strings.Contains(out, "alpha") {
		t.Fatalf("picker open but not drawn:\n%s", out)
	}
	a, _ = updateApp(t, a, keyMsg("esc"))
	if a.notes.pickingProject {
		t.Fatal("esc didn't close the picker")
	}
}
