//go:build !windows

package cmd

import (
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/tmux"
)

// Two projects whose folders share a name — ~/Projects/api and
// ~/work/api — both mapped to session c-api. With ~/work/api's c-api
// running, `ccmux attach api` attached to it (the wrong project, in the
// wrong directory), `ccmux new api` refused ("already has a running
// session (c-api)"), and `ccmux kill api` killed it. The CLI now finds
// a project's session by its directory: the first project keeps c-api,
// the second gets a path-tagged session of its own.

// sameNameEnv is a CLI env with ~/Projects/api (the projects root's)
// and ~/work/api, the latter's session c-api running.
func sameNameEnv(t *testing.T) (e *cliEnv, mine, other, tagged string) {
	t.Helper()
	e = newCLIEnv(t)
	mine = e.mkdir("Projects/api")
	other = e.mkdir("work/api")
	return e, mine, other, tmux.PathTaggedSessionName(mine)
}

func TestAttach_SameFolderNameGetsItsOwnSession(t *testing.T) {
	e, mine, other, tagged := sameNameEnv(t)
	e.env["FAKE_TMUX_SESSIONS"] = "c-api=" + other

	res := e.run("", "attach", "api")
	if res.code != 0 {
		t.Fatalf("attach exit %d\nstderr: %s", res.code, res.stderr)
	}
	news := e.tmuxCallsWith("new-session")
	if len(news) != 1 || !hasCall(news, "-s", tagged) || !hasCall(news, "-c", mine) {
		t.Errorf("attach api must start %s in %s, got new-session calls %v", tagged, mine, news)
	}
	attaches := e.tmuxCallsWith("attach-session")
	if !hasCall(attaches, "-t", exactTarget(tagged)) {
		t.Errorf("attach api must attach to %s; tmux calls:\n%s", tagged, strings.Join(e.tmuxCalls(), "\n"))
	}
	if hasCall(attaches, "-t", exactTarget("c-api")) {
		t.Errorf("attach api attached to ~/work/api's c-api")
	}
}

func TestAttach_SameFolderNameReattachesByDirectory(t *testing.T) {
	e, mine, other, tagged := sameNameEnv(t)
	e.env["FAKE_TMUX_SESSIONS"] = "c-api=" + other + " " + tagged + "=" + mine

	// The project under the root: its tagged session.
	if res := e.run("", "attach", "api"); res.code != 0 {
		t.Fatalf("attach api exit %d\nstderr: %s", res.code, res.stderr)
	}
	// The other one, by path: still its plain c-api.
	if res := e.run("", "attach", other); res.code != 0 {
		t.Fatalf("attach %s exit %d\nstderr: %s", other, res.code, res.stderr)
	}
	attaches := e.tmuxCallsWith("attach-session")
	if len(attaches) != 2 || !hasCall(attaches[:1], "-t", exactTarget(tagged)) || !hasCall(attaches[1:], "-t", exactTarget("c-api")) {
		t.Errorf("attach-session calls = %v, want %s then c-api", attaches, tagged)
	}
	if news := e.tmuxCallsWith("new-session"); len(news) != 0 {
		t.Errorf("running sessions must not be started again: %v", news)
	}
}

func TestNew_SameFolderNameElsewhereIsNotRunning(t *testing.T) {
	e := newCLIEnv(t)
	other := e.mkdir("work/api")
	e.env["FAKE_TMUX_SESSIONS"] = "c-api=" + other
	mine := e.home + "/Projects/api"

	res := e.run("", "new", "api")
	if res.code != 0 {
		t.Fatalf("new api refused over another directory's c-api: exit %d\nstderr: %s", res.code, res.stderr)
	}
	news := e.tmuxCallsWith("new-session")
	if tagged := tmux.PathTaggedSessionName(mine); len(news) != 1 || !hasCall(news, "-s", tagged) || !hasCall(news, "-c", mine) {
		t.Errorf("new api must start %s in %s, got %v", tagged, mine, news)
	}

	// Its own session running: refused, naming it.
	e.env["FAKE_TMUX_SESSIONS"] = "c-api=" + other + " " + tmux.PathTaggedSessionName(mine) + "=" + mine
	res = e.run("", "new", "api")
	if res.code == 0 || !strings.Contains(res.stderr, tmux.PathTaggedSessionName(mine)) {
		t.Errorf("new api with its session running: exit %d, stderr %q; want a refusal naming %s", res.code, res.stderr, tmux.PathTaggedSessionName(mine))
	}
}

func TestKill_SameFolderNameKillsByDirectory(t *testing.T) {
	e, mine, other, tagged := sameNameEnv(t)
	e.env["FAKE_TMUX_SESSIONS"] = "c-api=" + other + " " + tagged + "=" + mine

	res := e.run("", "kill", "api")
	if res.code != 0 {
		t.Fatalf("kill exit %d\nstderr: %s", res.code, res.stderr)
	}
	kills := e.tmuxCallsWith("kill-session")
	if len(kills) != 1 || !hasCall(kills, "-t", exactTarget(tagged)) {
		t.Errorf("kill api: kill-session calls = %v, want only %s", kills, tagged)
	}
	if !strings.Contains(res.stdout, "killed "+tagged) {
		t.Errorf("kill should say what it killed: %q", res.stdout)
	}

	// Only the other project's session running: nothing of api's to kill.
	e2 := newCLIEnv(t)
	e2.mkdir("Projects/api")
	other2 := e2.mkdir("work/api")
	e2.env["FAKE_TMUX_SESSIONS"] = "c-api=" + other2
	res = e2.run("", "kill", "api")
	if res.code == 0 {
		t.Fatal("kill api killed the other directory's c-api")
	}
	if !strings.Contains(res.stderr, "c-api runs in "+other2) {
		t.Errorf("error should point at the other project's c-api: %s", res.stderr)
	}
	if got := e2.tmuxCallsWith("kill-session"); len(got) != 0 {
		t.Errorf("kill-session sent: %v", got)
	}
}
