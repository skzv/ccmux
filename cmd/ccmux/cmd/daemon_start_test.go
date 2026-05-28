package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// TestRunDaemonStart_NoopsWhenAlreadyRunning is the regression guard
// for the "two daemons" report: `ccmux daemon start` must NOT spawn a
// second ccmuxd when one is already up. It should report the existing
// pid and return nil without calling spawn.
func TestRunDaemonStart_NoopsWhenAlreadyRunning(t *testing.T) {
	spawned := false
	var out bytes.Buffer
	err := runDaemonStart(&out, daemonStartDeps{
		running: func() (int, bool) { return 4242, true },
		spawn: func() (int, error) {
			spawned = true
			return 0, errors.New("spawn must not be called when a daemon is already running")
		},
	})
	if err != nil {
		t.Fatalf("runDaemonStart returned error: %v", err)
	}
	if spawned {
		t.Fatal("spawn was called despite a daemon already running — this is the duplicate-daemon bug")
	}
	got := out.String()
	if !strings.Contains(got, "already running") || !strings.Contains(got, "4242") {
		t.Errorf("output = %q, want it to mention 'already running' and the existing pid 4242", got)
	}
}

// TestRunDaemonStart_SpawnsWhenNoneRunning — the happy path: no
// daemon up, so we spawn one and report its pid.
func TestRunDaemonStart_SpawnsWhenNoneRunning(t *testing.T) {
	spawned := false
	var out bytes.Buffer
	err := runDaemonStart(&out, daemonStartDeps{
		running: func() (int, bool) { return 0, false },
		spawn: func() (int, error) {
			spawned = true
			return 9001, nil
		},
	})
	if err != nil {
		t.Fatalf("runDaemonStart returned error: %v", err)
	}
	if !spawned {
		t.Fatal("spawn was not called when no daemon was running")
	}
	got := out.String()
	if !strings.Contains(got, "started") || !strings.Contains(got, "9001") {
		t.Errorf("output = %q, want it to mention 'started' and the new pid 9001", got)
	}
	if strings.Contains(got, "already running") {
		t.Errorf("output = %q, should not claim 'already running' when it spawned", got)
	}
}

// TestRunDaemonStart_PropagatesSpawnError — a spawn failure (e.g.
// ccmuxd not on PATH) surfaces to the caller, and nothing claims
// success.
func TestRunDaemonStart_PropagatesSpawnError(t *testing.T) {
	var out bytes.Buffer
	wantErr := errors.New("ccmuxd not on PATH")
	err := runDaemonStart(&out, daemonStartDeps{
		running: func() (int, bool) { return 0, false },
		spawn:   func() (int, error) { return 0, wantErr },
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want it to wrap the spawn error", err)
	}
	if strings.Contains(out.String(), "started") {
		t.Errorf("output = %q, should not claim 'started' on spawn failure", out.String())
	}
}

// TestDefaultDaemonStartDeps_Wired — the production deps are
// non-nil so the command can't panic on a nil seam.
func TestDefaultDaemonStartDeps_Wired(t *testing.T) {
	d := defaultDaemonStartDeps()
	if d.running == nil || d.spawn == nil {
		t.Fatal("defaultDaemonStartDeps must wire both running and spawn")
	}
	// running() is safe to call — it shells to pgrep and returns a
	// bool. We don't assert the result (depends on the host), only
	// that it doesn't panic.
	_, _ = d.running()
}
