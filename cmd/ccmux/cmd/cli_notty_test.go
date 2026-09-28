//go:build !windows

package cmd

import (
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/creack/pty"

	"github.com/skzv/ccmux/internal/conversations"
	"github.com/skzv/ccmux/internal/daemon"
)

// TestStdinIsTerminal_PTYIsATerminal — the terminal-driver check behind
// the setup nudge and `ccmux update`'s prompt still accepts a real
// terminal (its /dev/null counterpart is in root_test.go).
func TestStdinIsTerminal_PTYIsATerminal(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()
	orig := os.Stdin
	os.Stdin = tty
	defer func() { os.Stdin = orig }()
	if !stdinIsTerminal() {
		t.Error("stdinIsTerminal() = false on a pty")
	}
}

// bareSessionDaemon is a fake ccmuxd answering POST /v1/sessions/bare
// with session name; hits counts the calls.
func bareSessionDaemon(t *testing.T, e *cliEnv, name string) *atomic.Int32 {
	t.Helper()
	var hits atomic.Int32
	e.fakeDaemon(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sessions/bare" {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		writeJSON(t, w, daemon.NewBareSessionResponse{Session: name, Path: "/work", Host: "box"})
	}))
	return &hits
}

// TestShell_UnknownAgentRejected — `ccmux shell --agent nope` was sent
// to the daemon, which silently launched the default agent.
func TestShell_UnknownAgentRejected(t *testing.T) {
	e := newCLIEnv(t)
	hits := bareSessionDaemon(t, e, "c-shell-x")
	res := e.run("", "shell", "--agent", "nope")
	if res.code == 0 {
		t.Fatalf("shell --agent nope should fail; stdout: %s", res.stdout)
	}
	if !strings.Contains(res.stderr, `unknown agent "nope"`) || !strings.Contains(res.stderr, "shell") {
		t.Errorf("error should name the bad agent and the valid ones: %s", res.stderr)
	}
	if hits.Load() != 0 {
		t.Errorf("an unknown agent still reached the daemon (%d calls)", hits.Load())
	}
	// "shell" and real agent ids stay accepted.
	for _, ok := range []string{"shell", "codex", "claude"} {
		if res := e.run("", "shell", "--agent", ok); res.code != 0 {
			t.Errorf("shell --agent %s: exit %d: %s", ok, res.code, res.stderr)
		}
	}
}

// assertNoAttach fails if ccmux ran a tmux attach — which, with no
// terminal, dies with "open terminal failed: not a terminal" after the
// session was already created.
func assertNoAttach(t *testing.T, e *cliEnv, what string) {
	t.Helper()
	if got := e.tmuxCallsWith("attach-session"); len(got) != 0 {
		t.Errorf("%s without a terminal still ran tmux attach: %v", what, got)
	}
}

// TestShell_NoTTYPrintsAttachHint — without a terminal `ccmux shell`
// created the session, then failed attaching and exited 1.
func TestShell_NoTTYPrintsAttachHint(t *testing.T) {
	e := newCLIEnv(t)
	bareSessionDaemon(t, e, "c-shell-abc")
	res := e.run("", "shell")
	if res.code != 0 {
		t.Fatalf("shell without a tty: exit %d\n%s", res.code, res.stderr)
	}
	if want := "created c-shell-abc; attach with: ccmux attach c-shell-abc"; !strings.Contains(res.stdout, want) {
		t.Errorf("stdout = %q, want %q", res.stdout, want)
	}
	assertNoAttach(t, e, "shell")
}

// TestNew_NoTTYPrintsAttachHint — same for `ccmux new`.
func TestNew_NoTTYPrintsAttachHint(t *testing.T) {
	e := newCLIEnv(t)
	e.mkdir("Projects")
	res := e.run("", "new", "beta")
	if res.code != 0 {
		t.Fatalf("new without a tty: exit %d\n%s", res.code, res.stderr)
	}
	if want := "created c-beta; attach with: ccmux attach c-beta"; !strings.Contains(res.stdout, want) {
		t.Errorf("stdout = %q, want %q", res.stdout, want)
	}
	if !hasCall(e.tmuxCallsWith("new-session"), "-s", "c-beta") {
		t.Errorf("the session must still be created; tmux calls:\n%s", strings.Join(e.tmuxCalls(), "\n"))
	}
	assertNoAttach(t, e, "new")
}

// TestResume_NoTTYPrintsAttachHint — same for `ccmux resume`.
func TestResume_NoTTYPrintsAttachHint(t *testing.T) {
	e := newCLIEnv(t)
	id := "3f2a1b4c-5d6e-4f70-8a9b-0c1d2e3f4a5b"
	e.seedClaudeTranscript(id, "hello")
	res := e.run("", "resume", id)
	if res.code != 0 {
		t.Fatalf("resume without a tty: exit %d\n%s", res.code, res.stderr)
	}
	name := conversations.ResumeSessionName(id)
	if want := "created " + name + "; attach with: ccmux attach " + name; !strings.Contains(res.stdout, want) {
		t.Errorf("stdout = %q, want %q", res.stdout, want)
	}
	assertNoAttach(t, e, "resume")
}

// TestNew_WithTTYStillAttaches — with a terminal, `ccmux new` keeps
// handing it to the new session.
func TestNew_WithTTYStillAttaches(t *testing.T) {
	e := newCLIEnv(t)
	e.mkdir("Projects")
	res := e.runTTY("", "new", "gamma")
	if res.code != 0 {
		t.Fatalf("new with a tty: exit %d\n%s", res.code, res.stderr)
	}
	if !hasCall(e.tmuxCallsWith("attach-session"), "-t", exactTarget("c-gamma")) {
		t.Errorf("with a terminal, new must attach; tmux calls:\n%s", strings.Join(e.tmuxCalls(), "\n"))
	}
	if strings.Contains(res.stdout, "attach with:") {
		t.Errorf("with a terminal there's no need for the hint: %q", res.stdout)
	}
}
