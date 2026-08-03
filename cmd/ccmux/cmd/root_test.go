package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/config"
)

// TestShouldNudgeSetup pins the first-run nudge decision: only on an
// interactive terminal, only before setup completes, and not after a
// dismissal.
func TestShouldNudgeSetup(t *testing.T) {
	completed := config.Config{}
	completed.Setup.Completed = true
	dismissed := config.Config{}
	dismissed.Setup.NudgeDismissed = true
	existingUser := config.Config{}
	existingUser.Tour.Shown = true

	cases := []struct {
		name        string
		cfg         config.Config
		interactive bool
		want        bool
	}{
		{"fresh + interactive", config.Config{}, true, true},
		{"fresh + non-interactive (script)", config.Config{}, false, false},
		{"already completed", completed, true, false},
		{"previously dismissed", dismissed, true, false},
		{"existing user (tour shown)", existingUser, true, false},
	}
	for _, tc := range cases {
		if got := shouldNudgeSetup(tc.cfg, tc.interactive); got != tc.want {
			t.Errorf("%s: shouldNudgeSetup = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestSetupCmd_HasYesFlag pins the non-interactive flag on `ccmux setup`.
func TestSetupCmd_HasYesFlag(t *testing.T) {
	c := newSetupCmd()
	if c.Flags().Lookup("yes") == nil {
		t.Fatal("`ccmux setup` should have a --yes flag")
	}
	if c.Flags().ShorthandLookup("y") == nil {
		t.Error("`ccmux setup --yes` should have a -y shorthand")
	}
}

// TestExecute_VersionFlag — regression for `ccmux --version` failing
// with "unknown flag: --version". rootCmd.Version was assigned in
// init(), which runs before Execute(version) sets versionString, so
// cobra saw an empty Version and never registered the flag. Execute
// must set Version before running the command.
func TestExecute_VersionFlag(t *testing.T) {
	// Reset the state init()+Execute mutate, and restore afterwards so
	// other tests in this package see the defaults.
	origVersion := rootCmd.Version
	defer func() {
		rootCmd.Version = origVersion
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	}()
	rootCmd.Version = ""

	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"--version"})

	if err := Execute("1.2.3-test"); err != nil {
		t.Fatalf("Execute(--version) errored: %v\noutput:\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "1.2.3-test") {
		t.Errorf("--version output missing version string; got:\n%s", out.String())
	}
}
