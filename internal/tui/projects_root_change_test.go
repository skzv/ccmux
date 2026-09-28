package tui

import (
	"testing"

	"github.com/skzv/ccmux/internal/project"
)

// TestConfigSaved_RootChangeRefreshesProjects — changing projects.root in
// Settings adopted the new root but never re-listed, so Projects (and the
// Notes project it feeds) kept showing the old root's projects until `r`.
func TestConfigSaved_RootChangeRefreshesProjects(t *testing.T) {
	a := newHomeApp(t, 120, 40, 0)
	a.cfg.Projects.Root = t.TempDir()
	a.notes.SetProject(&project.Project{Name: "old", Path: a.cfg.Projects.Root})

	// Saving something else (same root) must not re-list.
	same := a.cfg
	same.Sessions.DefaultDir = "/tmp"
	before := a.projectsLoadGen
	a, cmd := updateApp(t, a, configSavedMsg{Cfg: same})
	if cmd != nil || a.projectsLoadGen != before {
		t.Fatalf("a save that kept the root re-listed projects (gen %d → %d)", before, a.projectsLoadGen)
	}

	moved := a.cfg
	moved.Projects.Root = t.TempDir()
	a, cmd = updateApp(t, a, configSavedMsg{Cfg: moved})
	if cmd == nil || a.projectsLoadGen == before {
		t.Fatal("changing projects.root did not start a projects refresh")
	}
	if a.projectsM.root != moved.Projects.Root {
		t.Errorf("projects root = %q, want %q", a.projectsM.root, moved.Projects.Root)
	}
	if a.notes.project != nil {
		t.Errorf("Notes still on %q from the old root", a.notes.project.Path)
	}
}
