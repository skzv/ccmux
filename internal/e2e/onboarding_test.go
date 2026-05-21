//go:build integration

package e2e

import (
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/config"
)

// TestDoctor_PrintsChecks covers the doctor CUJ: `ccmux doctor` runs all
// dependency checks and prints a line per tool. The test is lenient about
// exit code (mosh / tailscale may not be installed in CI) but strict about
// output structure: every tool listed in the spec must appear.
func TestDoctor_PrintsChecks(t *testing.T) {
	e := newEnv(t)
	// doctor calls os.Exit(n) when n > 0 tools are missing.
	// Run via subprocess so the test process doesn't exit; collect stdout
	// regardless of the non-zero exit code.
	stdout, _, _ := e.ccmux("doctor")

	for _, want := range []string{"tmux", "mosh", "tailscale", "AI agents"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("doctor output missing section %q; got:\n%s", want, stdout)
		}
	}
}

// TestDoctor_TmuxFoundWhenInstalled checks that doctor reports ✓ for tmux
// when tmux is on PATH (which it must be for the e2e harness to work at all).
func TestDoctor_TmuxFoundWhenInstalled(t *testing.T) {
	e := newEnv(t)
	stdout, _, _ := e.ccmux("doctor")
	if !strings.Contains(stdout, "✓ tmux") {
		t.Errorf("doctor should report tmux as found; got:\n%s", stdout)
	}
}

// TestHostAddListRemove covers the host management CUJ: `ccmux host add`
// persists to config, `host list` shows the entry, and `host remove`
// deletes it.
func TestHostAddListRemove(t *testing.T) {
	e := newEnv(t)

	if _, stderr, err := e.ccmux("host", "add", "boxA", "100.1.2.3"); err != nil {
		t.Fatalf("host add: %v\nstderr: %s", err, stderr)
	}

	// Verify the config file was actually updated.
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	found := false
	for _, h := range cfg.Hosts {
		if h.Name == "boxA" && h.Address == "100.1.2.3" {
			found = true
		}
	}
	if !found {
		t.Errorf("config does not contain host boxA after add; hosts = %v", cfg.Hosts)
	}

	// List output should include the host name and address.
	stdout, _, err := e.ccmux("host", "list")
	if err != nil {
		t.Fatalf("host list: %v", err)
	}
	if !strings.Contains(stdout, "boxA") || !strings.Contains(stdout, "100.1.2.3") {
		t.Errorf("host list output missing boxA/100.1.2.3; got:\n%s", stdout)
	}

	// Remove the host.
	if _, stderr, err := e.ccmux("host", "remove", "boxA"); err != nil {
		t.Fatalf("host remove: %v\nstderr: %s", err, stderr)
	}
	cfg2, _ := config.Load()
	for _, h := range cfg2.Hosts {
		if h.Name == "boxA" {
			t.Errorf("host boxA still present in config after remove")
		}
	}

	// List output should no longer include it.
	stdout2, _, _ := e.ccmux("host", "list")
	if strings.Contains(stdout2, "boxA") {
		t.Errorf("host list still shows boxA after remove:\n%s", stdout2)
	}
}
