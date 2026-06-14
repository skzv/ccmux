package daemonservice

import (
	"errors"
	"testing"
)

func TestRestartIfRunning(t *testing.T) {
	origRun, origRestart := isRunningHook, restartHook
	defer func() { isRunningHook, restartHook = origRun, origRestart }()

	// Not running → no-op, no restart issued.
	calls := 0
	isRunningHook = func() bool { return false }
	restartHook = func() (Status, error) { calls++; return Status{}, nil }
	if restarted, err := RestartIfRunning(); restarted || err != nil || calls != 0 {
		t.Fatalf("not-running: got (restarted=%v err=%v calls=%d), want (false nil 0)", restarted, err, calls)
	}

	// Running → restart issued exactly once.
	isRunningHook = func() bool { return true }
	if restarted, err := RestartIfRunning(); !restarted || err != nil || calls != 1 {
		t.Fatalf("running: got (restarted=%v err=%v calls=%d), want (true nil 1)", restarted, err, calls)
	}

	// Restart failure is propagated, still reported as "restarted attempted".
	wantErr := errors.New("kickstart failed")
	restartHook = func() (Status, error) { return Status{}, wantErr }
	if restarted, err := RestartIfRunning(); !restarted || !errors.Is(err, wantErr) {
		t.Fatalf("restart error: got (restarted=%v err=%v), want (true %v)", restarted, err, wantErr)
	}
}
