//go:build integration

package e2e

import (
	"strings"
	"testing"
)

// TestSessionKill_NonexistentFailsCleanly — `ccmux kill` of a session
// that doesn't exist must exit non-zero with a clear stderr message,
// not panic, and must leave every other session untouched.
func TestSessionKill_NonexistentFailsCleanly(t *testing.T) {
	e := newEnv(t)
	e.newTmuxSession("c-survivor", e.Home)

	stdout, stderr, err := e.ccmux("kill", "no-such-project")
	if err == nil {
		t.Fatalf("`ccmux kill no-such-project` unexpectedly succeeded\nstdout: %s", stdout)
	}
	if strings.TrimSpace(stderr) == "" {
		t.Error("`ccmux kill` of a nonexistent session failed with empty stderr — the user gets no explanation")
	}
	if !strings.Contains(stderr, "kill-session") && !strings.Contains(stderr, "no-such-project") {
		t.Errorf("kill error message doesn't say what failed; stderr: %q", stderr)
	}
	for _, sign := range []string{"panic:", "goroutine "} {
		if strings.Contains(stderr, sign) {
			t.Fatalf("`ccmux kill` panicked; stderr:\n%s", stderr)
		}
	}
	if !e.hasSession("c-survivor") {
		t.Error("unrelated session c-survivor was harmed by killing a nonexistent one")
	}
}
