package cmd

import (
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
