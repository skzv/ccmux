//go:build integration

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/daemon"
)

// TestDaemonIPC_SessionStateObserved covers the daemon's poll →
// classify → IPC chain end-to-end: a session created through `ccmux
// shell` (running the sleeping stub agent, not a real Claude CLI) must
// show up over the daemon's Unix socket and settle into a plausible
// agent state. "unknown" is a legitimate transient right after
// creation, so the test polls for the transition instead of asserting
// a single snapshot.
func TestDaemonIPC_SessionStateObserved(t *testing.T) {
	e := newEnv(t)
	e.startDaemon()

	const name = "c-stateflow"
	if _, stderr, _ := e.ccmux("shell", "--name", name, "--path", e.Root); !e.hasSession(name) {
		t.Fatalf("`ccmux shell` did not create session %q\nstderr: %s", name, stderr)
	}

	// Fixture config polls every 1s with a 1s needs-input threshold;
	// 15s is generous headroom for slow CI without a fixed sleep.
	settled := map[string]bool{"active": true, "idle": true, "needs_input": true}
	cli := e.localClient()
	var last daemon.SessionState
	seen := false
	ok := waitFor(15*time.Second, func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		ss, err := cli.Sessions(ctx)
		if err != nil {
			return false
		}
		for _, s := range ss {
			if s.Name == name {
				seen = true
				last = s
				return settled[s.State]
			}
		}
		return false
	})
	if !seen {
		t.Fatalf("daemon never reported session %q over IPC", name)
	}
	if !ok {
		t.Fatalf("session %q never settled into a plausible state; last state = %q", name, last.State)
	}
	if last.Host != "local" {
		t.Errorf("session host = %q, want local", last.Host)
	}

	// CLI surface: `ccmux list --json` (served by the same daemon)
	// must reflect the session with a plausible state too.
	found := false
	for _, s := range e.listSessionsJSON() {
		if s.Name == name {
			found = true
			if !settled[s.State] {
				t.Errorf("`ccmux list --json` state = %q, want one of active/idle/needs_input", s.State)
			}
		}
	}
	if !found {
		t.Errorf("`ccmux list --json` did not report session %q", name)
	}
}
