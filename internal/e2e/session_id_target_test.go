//go:build integration

package e2e

import (
	"strings"
	"testing"
)

// TestCLI_SessionIDTargetsCantReachOtherSessions — tmux reads a target
// starting with "$" as a session ID even in the exact `=name:` form, so
// `ccmux kill '$1'` exited 0 after killing whichever session had ID $1,
// `ccmux rename '$1' x` renamed it and `ccmux attach '$1'` attached to
// it. Against a real tmux server, each must fail and leave that session
// alone.
func TestCLI_SessionIDTargetsCantReachOtherSessions(t *testing.T) {
	e := newEnv(t)
	e.newTmuxSession("c-first", e.Home)
	e.newTmuxSession("c-victim", e.Home)
	out, err := e.tmux("display-message", "-p", "-t", "=c-victim:", "#{session_id}")
	if err != nil {
		t.Fatalf("session id of c-victim: %v\n%s", err, out)
	}
	id := strings.TrimSpace(out) // e.g. "$1"; no session has that name
	if !strings.HasPrefix(id, "$") {
		t.Fatalf("unexpected session id %q", id)
	}
	// The premise: tmux itself resolves the exact target by ID.
	if got, err := e.tmux("display-message", "-p", "-t", "="+id+":", "#{session_name}"); err != nil || strings.TrimSpace(got) != "c-victim" {
		t.Skipf("this tmux doesn't resolve =%s: as a session ID (%q, %v); nothing to guard", id, got, err)
	}

	for _, args := range [][]string{
		{"kill", id},
		{"rename", id, "c-renamed"},
		{"attach", id},
	} {
		stdout, stderr, err := e.ccmux(args...)
		if err == nil {
			t.Errorf("ccmux %v succeeded; stdout %q", args, stdout)
		}
		if !strings.Contains(stderr, "session ID") {
			t.Errorf("ccmux %v: stderr should say tmux reads %q as a session ID: %q", args, id, stderr)
		}
		if names := e.sessionNames(); !e.hasSession("c-victim") || !e.hasSession("c-first") || len(names) != 2 {
			t.Fatalf("ccmux %v touched another session; sessions now %v", args, names)
		}
	}
}
