package tui

import (
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// TestProjects_SelectionSurvivesResort — the Projects list is sorted by
// mtime, so working in a project moves it to the top on the next
// refresh. The cursor was kept by index and silently landed on a
// different project; Enter then opened the wrong one. Selection is
// keyed by (host, path).
func TestProjects_SelectionSurvivesResort(t *testing.T) {
	now := time.Now()
	alpha := project.Project{Name: "alpha", Host: "local", Path: "/p/alpha", Modified: now}
	beta := project.Project{Name: "beta", Host: "local", Path: "/p/beta", Modified: now.Add(-time.Hour)}
	gamma := project.Project{Name: "gamma", Host: "local", Path: "/p/gamma", Modified: now.Add(-2 * time.Hour)}
	miniBeta := project.Project{Name: "beta", Host: "mac-mini", Path: "/p/beta", Modified: now.Add(-3 * time.Hour)}

	cases := []struct {
		name     string
		initial  []project.Project
		pick     int
		refresh  []project.Project
		wantHost string
		wantPath string
	}{
		{"worked-in project jumps to the top", []project.Project{alpha, beta, gamma}, 1,
			[]project.Project{beta, alpha, gamma}, "local", "/p/beta"},
		{"new project appears above", []project.Project{alpha, beta, gamma}, 2,
			[]project.Project{{Name: "new", Host: "local", Path: "/p/new"}, alpha, beta, gamma}, "local", "/p/gamma"},
		{"same path on another host keeps its host", []project.Project{beta, gamma, miniBeta}, 2,
			[]project.Project{miniBeta, beta, gamma}, "mac-mini", "/p/beta"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newProjects(styles.Default(), DefaultKeymap())
			m.SetProjects(tc.initial)
			m.cursor = tc.pick
			m.SetProjects(tc.refresh)
			sel := m.Selected()
			if sel == nil || projectHost(*sel) != tc.wantHost || sel.Path != tc.wantPath {
				t.Fatalf("after re-sort selected %+v, want %s:%s", sel, tc.wantHost, tc.wantPath)
			}
		})
	}
}

// TestProjects_SelectionVanishedClamps — when the selected project is
// gone, the cursor falls back to a valid row rather than going stale.
func TestProjects_SelectionVanishedClamps(t *testing.T) {
	m := newProjects(styles.Default(), DefaultKeymap())
	m.SetProjects([]project.Project{
		{Name: "a", Host: "local", Path: "/p/a"},
		{Name: "b", Host: "local", Path: "/p/b"},
		{Name: "c", Host: "local", Path: "/p/c"},
	})
	m.cursor = 2
	m.SetProjects([]project.Project{{Name: "a", Host: "local", Path: "/p/a"}})
	if sel := m.Selected(); sel == nil || sel.Path != "/p/a" {
		t.Fatalf("selected %+v, want the remaining /p/a", sel)
	}
}

// TestProjects_SelectionSurvivesResortWhileFiltered — with a filter
// applied, the cursor indexes the visible rows; a refresh must keep the
// same project selected within them.
func TestProjects_SelectionSurvivesResortWhileFiltered(t *testing.T) {
	m := newProjects(styles.Default(), DefaultKeymap())
	m.SetProjects([]project.Project{
		{Name: "ccmux", Host: "local", Path: "/p/ccmux"},
		{Name: "ccmux-website", Host: "local", Path: "/p/ccmux-website"},
		{Name: "other", Host: "local", Path: "/p/other"},
	})
	m.filter.SetValue("ccmux")
	m.cursor = 1 // ccmux-website
	m.SetProjects([]project.Project{
		{Name: "ccmux-website", Host: "local", Path: "/p/ccmux-website"},
		{Name: "other", Host: "local", Path: "/p/other"},
		{Name: "ccmux", Host: "local", Path: "/p/ccmux"},
	})
	if sel := m.Selected(); sel == nil || sel.Path != "/p/ccmux-website" {
		t.Fatalf("selected %+v, want /p/ccmux-website", sel)
	}
}
