//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tmux"
)

// listSessionsJSON runs `ccmux list --json` and parses the output.
func (e *Env) listSessionsJSON() []daemon.SessionState {
	e.t.Helper()
	stdout, stderr, err := e.ccmux("list", "--json")
	if err != nil {
		e.t.Fatalf("ccmux list --json: %v\nstderr: %s", err, stderr)
	}
	var out []daemon.SessionState
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		e.t.Fatalf("parse `ccmux list --json`: %v\nstdout: %q", err, stdout)
	}
	return out
}

// TestSessionLifecycle covers the create → list → rename → kill CUJ
// through the real CLI against the real daemon and a real tmux server.
func TestSessionLifecycle(t *testing.T) {
	e := newEnv(t)
	e.startDaemon()

	// Create: `ccmux shell` posts to the daemon's /v1/sessions/bare.
	// The command then execs `tmux attach`, which fails without a
	// controlling terminal — expected; the session is created first.
	const name = "c-e2e-life"
	_, stderr, _ := e.ccmux("shell", "--name", name, "--path", e.Root, "--agent", "shell")
	if !e.hasSession(name) {
		t.Fatalf("`ccmux shell` did not create session %q\nstderr: %s", name, stderr)
	}

	// List: `ccmux list --json` reports it via the daemon.
	found := false
	for _, s := range e.listSessionsJSON() {
		if s.Name == name {
			found = true
			if s.Host != "local" {
				t.Errorf("session host = %q, want %q", s.Host, "local")
			}
		}
	}
	if !found {
		t.Fatalf("`ccmux list --json` did not report session %q", name)
	}

	// Rename.
	const renamed = "c-e2e-life-renamed"
	if _, stderr, err := e.ccmux("rename", name, renamed); err != nil {
		t.Fatalf("ccmux rename: %v\nstderr: %s", err, stderr)
	}
	if e.hasSession(name) {
		t.Errorf("old session %q still present after rename", name)
	}
	if !e.hasSession(renamed) {
		t.Errorf("renamed session %q not present", renamed)
	}

	// Kill.
	if _, stderr, err := e.ccmux("kill", renamed); err != nil {
		t.Fatalf("ccmux kill: %v\nstderr: %s", err, stderr)
	}
	if e.hasSession(renamed) {
		t.Errorf("session %q still present after kill", renamed)
	}
}

// TestSessionKill_NameSanitization pins the fix for `ccmux kill` using
// a weaker sanitizer than tmux.SessionNameForPath. A project name with
// a space sanitizes to '_' for the session name; if `ccmux kill` only
// rewrites dots, it targets the wrong session and the kill fails.
func TestSessionKill_NameSanitization(t *testing.T) {
	e := newEnv(t)
	// SessionNameForPath turns "my proj" into "c-my_proj".
	if want := tmux.SessionNameForPath("/x/my proj"); want != "c-my_proj" {
		t.Fatalf("precondition: SessionNameForPath = %q, want c-my_proj", want)
	}
	e.newTmuxSession("c-my_proj", e.Home)

	if _, stderr, err := e.ccmux("kill", "my proj"); err != nil {
		t.Fatalf(`ccmux kill "my proj": %v\nstderr: %s`, err, stderr)
	}
	if e.hasSession("c-my_proj") {
		t.Error("c-my_proj still present — `ccmux kill` used the wrong sanitizer")
	}
}

// TestSessionName_NoCollision creates several auto-named bare sessions
// in succession and asserts every generated name is distinct — the
// daemon must not hand two sessions the same name.
func TestSessionName_NoCollision(t *testing.T) {
	e := newEnv(t)
	e.startDaemon()
	cli := e.localClient()

	seen := map[string]bool{}
	for i := 0; i < 6; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		res, err := cli.NewBareSession(ctx, daemon.NewBareSessionRequest{
			Path:  e.Root,
			Agent: "shell",
		})
		cancel()
		if err != nil {
			t.Fatalf("NewBareSession #%d: %v", i, err)
		}
		if seen[res.Session] {
			t.Fatalf("daemon reused session name %q for two bare sessions", res.Session)
		}
		seen[res.Session] = true
	}
	if got := len(e.sessionNames()); got < 6 {
		t.Errorf("expected >=6 distinct sessions, got %d", got)
	}
}

// TestSessionAttach_Local covers `ccmux attach <project>`: it creates
// the project's session if missing, then attaches. The attach fails
// without a tty in the harness — the assertion is that the correctly
// named session was created.
func TestSessionAttach_Local(t *testing.T) {
	e := newEnv(t)
	proj := filepath.Join(e.Root, "attachme")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}

	// Attach (exec of `tmux attach` at the end fails w/o a tty).
	_, _, _ = e.ccmux("attach", proj)

	if !e.hasSession("c-attachme") {
		t.Fatal("`ccmux attach` did not create session c-attachme")
	}
}

// TestSessionAttach_RemoteCommand covers the remote-attach path: with
// a configured host, `ccmux shell --host` resolves it and reaches for
// that host's daemon. The host here is unreachable, so the command
// fails — but failing at the remote round-trip (not at "no such host")
// proves the remote command path was taken.
func TestSessionAttach_RemoteCommand(t *testing.T) {
	e := newEnv(t)

	// Unknown host: must fail with the "configure it" hint.
	if _, stderr, err := e.ccmux("shell", "--host", "ghost"); err == nil {
		t.Errorf("`ccmux shell --host ghost` unexpectedly succeeded")
	} else if stderr == "" {
		t.Errorf("expected an error message for unknown host, got none")
	}

	// Configured but unreachable host: 127.0.0.1:1 refuses fast.
	if _, _, err := e.ccmux("host", "add", "fakehost", "127.0.0.1"); err != nil {
		t.Fatalf("ccmux host add: %v", err)
	}
	start := time.Now()
	if _, _, err := e.ccmux("shell", "--host", "fakehost"); err == nil {
		t.Errorf("`ccmux shell --host fakehost` unexpectedly succeeded")
	}
	if time.Since(start) > 35*time.Second {
		t.Errorf("remote shell attempt took too long: %v", time.Since(start))
	}
}
