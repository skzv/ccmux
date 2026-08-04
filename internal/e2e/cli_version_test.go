//go:build integration

package e2e

import (
	"strings"
	"testing"
)

// TestVersionFlag_Works pins the fix for `ccmux --version` (#169):
// rootCmd.Version used to be assigned in init(), which runs before
// Execute(version) hands the command its real version string — so
// cobra never registered the --version flag and the command died with
// "unknown flag: --version". The flag must exit 0 and print a version
// line.
func TestVersionFlag_Works(t *testing.T) {
	e := newEnv(t)

	stdout, stderr, err := e.ccmux("--version")
	if err != nil {
		t.Fatalf("`ccmux --version` failed: %v\nstderr: %s", err, stderr)
	}
	out := strings.TrimSpace(stdout)
	if out == "" {
		t.Fatal("`ccmux --version` printed nothing")
	}
	if !strings.Contains(out, "version") {
		t.Errorf("`ccmux --version` output %q does not look like a version line", out)
	}

	// Control: an unknown flag must still error — proving the assertion
	// above passes because --version is registered, not because the
	// binary blanket-accepts arbitrary flags.
	if _, stderr, err := e.ccmux("--definitely-not-a-flag"); err == nil {
		t.Error("`ccmux --definitely-not-a-flag` unexpectedly succeeded")
	} else if !strings.Contains(stderr, "unknown flag") {
		t.Errorf("unknown-flag error not surfaced to the user; stderr: %q", stderr)
	}
}
