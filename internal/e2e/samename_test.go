//go:build integration

package e2e

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tmux"
)

// sessionPaths maps every session on the sandbox tmux server to the
// directory it was started in.
func (e *Env) sessionPaths() map[string]string {
	e.t.Helper()
	out, err := e.tmux("list-sessions", "-F", "#{session_name}\t#{session_path}")
	if err != nil {
		return map[string]string{}
	}
	paths := map[string]string{}
	for _, ln := range strings.Split(strings.TrimSpace(out), "\n") {
		if name, path, ok := strings.Cut(ln, "\t"); ok {
			paths[name] = path
		}
	}
	return paths
}

// TestSameFolderName_EachProjectGetsItsOwnSession — two projects with
// the same folder name (the projects root's api and ~/work/api) both
// mapped to c-api, so opening the second attached to the first one's
// session, running in the wrong directory. Through the real CLI, the
// real daemon and a real (sandboxed) tmux server: the first keeps c-api,
// the second gets a path-tagged session in its own directory, and
// kill / the daemon's create endpoints all find each project's own.
func TestSameFolderName_EachProjectGetsItsOwnSession(t *testing.T) {
	e := newEnv(t)
	mine := filepath.Join(e.Root, "api")
	other := filepath.Join(e.Home, "work", "api")
	mkdirAll(t, mine)
	mkdirAll(t, other)
	tagged := tmux.PathTaggedSessionName(mine)

	// The ~/work one first: it gets the plain name. (attach's final
	// `tmux attach` fails without a terminal; the session is made first.)
	_, _, _ = e.ccmux("attach", other)
	if p := e.sessionPaths()["c-api"]; !tmux.SamePath(p, other) {
		t.Fatalf("c-api runs in %q, want %s (sessions %v)", p, other, e.sessionPaths())
	}

	// The projects root's api: its own, tagged session — not c-api.
	_, _, _ = e.ccmux("attach", "api")
	paths := e.sessionPaths()
	if p, ok := paths[tagged]; !ok || !tmux.SamePath(p, mine) {
		t.Fatalf("`ccmux attach api` should start %s in %s; sessions %v", tagged, mine, paths)
	}
	if !tmux.SamePath(paths["c-api"], other) || len(paths) != 2 {
		t.Fatalf("sessions after both attaches = %v, want c-api in %s and %s in %s", paths, other, tagged, mine)
	}

	// Attaching again starts nothing new, for either project.
	_, _, _ = e.ccmux("attach", "api")
	_, _, _ = e.ccmux("attach", other)
	if got := len(e.sessionPaths()); got != 2 {
		t.Errorf("re-attaching started sessions: %v", e.sessionPaths())
	}

	// The daemon answers with each project's own session.
	e.startDaemon()
	cli := e.localClient()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	st, err := cli.NewSession(ctx, daemon.NewSessionRequest{Project: "api"})
	if err != nil || st.Name != tagged {
		t.Errorf("POST /v1/sessions {project: api} = %+v, %v; want %s", st, err, tagged)
	}
	pr, err := cli.NewProject(ctx, daemon.NewProjectRequest{Name: "api"})
	if err != nil || pr.Session != tagged {
		t.Errorf("POST /v1/projects {name: api} = %+v, %v; want %s", pr, err, tagged)
	}
	st, err = cli.NewSession(ctx, daemon.NewSessionRequest{Project: "api", Path: other})
	if err != nil || st.Name != "c-api" {
		t.Errorf("POST /v1/sessions for %s = %+v, %v; want c-api", other, st, err)
	}

	// kill api kills the projects root's api's session, not ~/work's.
	if _, stderr, err := e.ccmux("kill", "api"); err != nil {
		t.Fatalf("ccmux kill api: %v\n%s", err, stderr)
	}
	paths = e.sessionPaths()
	if _, ok := paths[tagged]; ok {
		t.Errorf("%s survived `ccmux kill api`", tagged)
	}
	if !tmux.SamePath(paths["c-api"], other) {
		t.Errorf("`ccmux kill api` killed ~/work/api's c-api; sessions %v", paths)
	}

	// With c-api still ~/work's, `ccmux new api` isn't "already
	// running": it starts the projects root's api's own session again.
	_, stderr, _ := e.ccmux("new", "api")
	if strings.Contains(stderr, "already has a running session") {
		t.Errorf("`ccmux new api` refused over ~/work/api's c-api: %s", stderr)
	}
	if p, ok := e.sessionPaths()[tagged]; !ok || !tmux.SamePath(p, mine) {
		t.Errorf("`ccmux new api` should start %s in %s; sessions %v\nstderr: %s", tagged, mine, e.sessionPaths(), stderr)
	}
}
