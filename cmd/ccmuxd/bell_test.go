package main

import (
	"testing"

	"github.com/skzv/ccmux/internal/config"
)

// TestShouldRingBell pins the truth table for the notifications config.
// User feedback that prompted this: when moshi-hook is paired the
// original daemon silently muted the laptop bell, but a lot of users
// want both the audible cue at the desk AND the push on the phone —
// they're complementary, not duplicates. New default is "always ring";
// the legacy "no duplicates" behavior is opt-in via
// moshi_suppresses_bell.
func TestShouldRingBell(t *testing.T) {
	cases := []struct {
		name        string
		bell        bool
		moshiSupp   bool
		moshiPaired bool
		want        bool
	}{
		{"default (bell=true, suppress=false): ring even with moshi paired",
			true, false, true, true},
		{"default with no moshi: ring",
			true, false, false, true},
		{"bell off: never ring (moshi paired)",
			false, false, true, false},
		{"bell off: never ring (no moshi)",
			false, true, false, false},
		{"legacy: bell on, suppress on, moshi paired → silent",
			true, true, true, false},
		{"legacy: bell on, suppress on, moshi NOT paired → ring",
			true, true, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &server{cfg: config.Config{
				Notifications: config.NotificationsConfig{
					Bell:                tc.bell,
					MoshiSuppressesBell: tc.moshiSupp,
				},
			}}
			if got := s.shouldRingBell(tc.moshiPaired); got != tc.want {
				t.Errorf("shouldRingBell = %v, want %v", got, tc.want)
			}
		})
	}
}
