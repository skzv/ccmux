package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tmux"
)

// Two projects whose folders share a name — ~/Projects/api and
// ~/work/api (a --projects override root next to the default, say) —
// both mapped to c-api. With ~/work/api's c-api running, Enter on
// ~/Projects/api failed with "duplicate session" (or, from the menu,
// the "new session" was numbered as though it were c-api's sibling),
// and the Projects screen showed c-api as its session and counted the
// other project's sessions as its own.

// sameNameDirs makes ~/Projects/api and ~/work/api under a temp dir.
func sameNameDirs(t *testing.T) (mine, other string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mine, other = filepath.Join(base, "Projects", "api"), filepath.Join(base, "work", "api")
	for _, d := range []string{mine, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return mine, other
}

// fakeProjectTmux points the project-open tmux seams at sessions and
// records every session created as "name@dir".
func fakeProjectTmux(t *testing.T, sessions []tmux.Session) *[]string {
	t.Helper()
	var created []string
	origList, origNew := projectTmuxList, projectTmuxNew
	t.Cleanup(func() { projectTmuxList, projectTmuxNew = origList, origNew })
	projectTmuxList = func(context.Context) ([]tmux.Session, error) { return sessions, nil }
	projectTmuxNew = func(_ context.Context, name, dir, _ string) error {
		created = append(created, name+"@"+dir)
		return nil
	}
	return &created
}

func TestOpenProject_SameFolderNameStartsItsOwnSession(t *testing.T) {
	mine, other := sameNameDirs(t)
	tagged := tmux.PathTaggedSessionName(mine)
	created := fakeProjectTmux(t, []tmux.Session{{Name: "c-api", Path: other}})

	a := newAppForTest(t)
	msgs := flattenCmd(a.attachOrCreateLocal(project.Project{Name: "api", Path: mine}))
	ready, ok := findMsg[projectSessionReadyMsg](msgs)
	if !ok {
		t.Fatalf("opening the project produced %#v, want a started session", msgs)
	}
	if ready.Session != tagged {
		t.Errorf("session = %q, want %s (c-api is ~/work/api's)", ready.Session, tagged)
	}
	if len(*created) != 1 || (*created)[0] != tagged+"@"+mine {
		t.Errorf("created %v, want %s in %s", *created, tagged, mine)
	}
}

func TestOpenProject_MenuListsOnlyThisDirectorysSessions(t *testing.T) {
	mine, other := sameNameDirs(t)
	tagged := tmux.PathTaggedSessionName(mine)
	fakeProjectTmux(t, []tmux.Session{{Name: "c-api", Path: other}, {Name: tagged, Path: mine}})

	a := newAppForTest(t)
	msgs := flattenCmd(a.attachOrCreateLocal(project.Project{Name: "api", Path: mine}))
	menu, ok := findMsg[projectMenuMsg](msgs)
	if !ok {
		t.Fatalf("opening a project with a running session produced %#v, want its menu", msgs)
	}
	if len(menu.Sessions) != 1 || menu.Sessions[0].Name != tagged {
		t.Errorf("menu sessions = %+v, want only %s", menu.Sessions, tagged)
	}
}

func TestProjectMenuNewSession_NumbersTheProjectsOwnSession(t *testing.T) {
	mine, other := sameNameDirs(t)
	tagged := tmux.PathTaggedSessionName(mine)

	for _, tc := range []struct {
		name     string
		sessions []tmux.Session
		want     string
	}{
		{"plain name elsewhere, none here", []tmux.Session{{Name: "c-api", Path: other}}, tagged},
		{"tagged session running here", []tmux.Session{{Name: "c-api", Path: other}, {Name: tagged, Path: mine}}, tagged + "-2"},
		{"no collision", []tmux.Session{{Name: "c-api", Path: mine}}, "c-api-2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			created := fakeProjectTmux(t, tc.sessions)
			a := newAppForTest(t)
			_, cmd := a.Update(projectMenuPickMsg{
				Project:     "api",
				ProjectPath: mine,
				Entry:       projectMenuEntry{kind: menuNewSession},
			})
			ready, ok := findMsg[projectSessionReadyMsg](flattenCmd(cmd))
			if !ok || ready.Session != tc.want {
				t.Errorf("new session = %+v (found %v), want %s", ready, ok, tc.want)
			}
			if len(*created) != 1 || (*created)[0] != tc.want+"@"+mine {
				t.Errorf("created %v, want %s in %s", *created, tc.want, mine)
			}
		})
	}
}

func TestProjectSessionName_AndCount_ByDirectory(t *testing.T) {
	mine, other := sameNameDirs(t)
	tagged := tmux.PathTaggedSessionName(mine)
	p := project.Project{Name: "api", Path: mine}
	sessions := []daemon.SessionState{
		{Name: "c-api", Host: "local", Path: other},
		{Name: "c-api-2", Host: "local", Path: other},
		{Name: tagged, Host: "local", Path: mine},
		{Name: tagged + "-2", Host: "local", Path: mine},
		{Name: tagged, Host: "mini", Path: mine}, // another machine's
	}
	if got := projectSessionName(p, sessions); got != tagged {
		t.Errorf("projectSessionName = %q, want %s", got, tagged)
	}
	if got := countSessionsForProject(p, sessions); got != 2 {
		t.Errorf("countSessionsForProject = %d, want 2 (%s and %s-2, not ~/work/api's)", got, tagged, tagged)
	}
	otherProj := project.Project{Name: "api", Path: other}
	if got := projectSessionName(otherProj, sessions); got != "c-api" {
		t.Errorf("the other project's session = %q, want c-api", got)
	}
	if got := countSessionsForProject(otherProj, sessions); got != 2 {
		t.Errorf("the other project's count = %d, want 2", got)
	}

	// Before its session exists, the detail pane names the one Enter
	// would start.
	m := newProjects(newAppForTest(t).styles, DefaultKeymap())
	m.SetProjects([]project.Project{p})
	m.SetSessions(sessions[:2])
	if out := m.renderDetail(80, 20); !strings.Contains(out, tagged) {
		t.Errorf("detail pane should name %s:\n%s", tagged, out)
	}
}
