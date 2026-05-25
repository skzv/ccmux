//go:build integration

package e2e

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestDaemon_PeerAlreadyServing_ExitsZero verifies that when ccmuxd
// is launched while another ccmuxd is already bound to the Unix
// socket, the second one exits cleanly (status 0), not as a failure.
//
// Why this matters: the macOS launchd plist uses KeepAlive with
// SuccessfulExit=false (respawn on failure). If the conflict case
// exited non-zero, launchd would respawn ccmuxd every ~10 seconds —
// each respawn would re-trip the same conflict — flooding the user's
// stderr log forever. We hit this bug in the wild (the log filled to
// thousands of "another ccmuxd is already listening" lines before it
// was caught). This test pins the contract that prevents the loop.
//
// The matching plist contract is asserted by
// TestPlistTemplate_KeepAliveIsConditional in
// internal/daemonservice/service_test.go — both must stay correct
// for the loop to remain impossible.
func TestDaemon_PeerAlreadyServing_ExitsZero(t *testing.T) {
	e := newEnv(t)
	// First daemon: full health-checked startup via the helper.
	e.startDaemon()

	// Second daemon: same env (newEnv already mutated process env via
	// t.Setenv, so os.Environ() carries the sandbox HOME/PATH) and so
	// the same socket path. Should observe the peer and exit 0 within
	// a couple of seconds.
	second := exec.Command(builtCcmuxd)
	second.Dir = e.Home
	second.Env = os.Environ()

	if err := second.Start(); err != nil {
		t.Fatalf("start second ccmuxd: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- second.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			// Wait() returns an *exec.ExitError for non-zero exits.
			if ee, ok := err.(*exec.ExitError); ok {
				t.Fatalf("second ccmuxd exited with code %d (want 0); stderr+stdout:\n%s",
					ee.ExitCode(), ee.Stderr)
			}
			t.Fatalf("second ccmuxd Wait: %v", err)
		}
		// Exit 0 — perfect, the loop is broken before it can start.
	case <-time.After(5 * time.Second):
		_ = second.Process.Kill()
		t.Fatalf("second ccmuxd did not exit within 5s — it should detect the peer and bail")
	}
}
