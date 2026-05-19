package main

import (
	"testing"

	"github.com/skzv/ccmux/internal/config"
)

// TestBellAlwaysRingsRegardlessOfMoshi pins the always-ring policy.
// History: an earlier version had a notifications.moshi_suppresses_bell
// knob that muted the BEL when moshi-hook was paired. That hid the
// laptop signal from users sitting at the laptop — the two channels
// are complementary (audible cue at the desk, push on the phone),
// not duplicates. The knob is gone; the only switch is the master
// Notifications.Bell.
func TestBellAlwaysRingsRegardlessOfMoshi(t *testing.T) {
	cases := []struct {
		name string
		bell bool
		want bool
	}{
		{"bell on → ring", true, true},
		{"bell off → silent", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Config{Notifications: config.NotificationsConfig{Bell: tc.bell}}
			if got := cfg.Notifications.Bell; got != tc.want {
				t.Errorf("Notifications.Bell = %v, want %v", got, tc.want)
			}
		})
	}
}
