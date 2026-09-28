package cmd

import (
	"bytes"
	"io"
	"os"
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

// TestStdinIsTerminal_DevNullIsNotATerminal — stdinIsTerminal checked
// for a character device, which /dev/null is: `ccmux </dev/null` got
// the first-run setup nudge (whose EOF answer counts as "yes"), and
// `ccmux update </dev/null` printed its y/N question to nobody.
func TestStdinIsTerminal_DevNullIsNotATerminal(t *testing.T) {
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	origIn, origOut := os.Stdin, os.Stdout
	t.Cleanup(func() {
		os.Stdin, os.Stdout = origIn, origOut
		_ = null.Close()
	})
	os.Stdin = null
	if stdinIsTerminal() {
		t.Error("stdinIsTerminal() = true with stdin on " + os.DevNull)
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	answer := promptYesNo("Re-run setup?")
	os.Stdout = origOut
	_ = w.Close()
	printed, _ := io.ReadAll(r)
	_ = r.Close()
	if answer || len(printed) != 0 {
		t.Errorf("promptYesNo with stdin on %s = %v, printed %q; want false without asking", os.DevNull, answer, printed)
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
