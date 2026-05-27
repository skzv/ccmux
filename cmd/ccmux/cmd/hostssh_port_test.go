package cmd

import (
	"testing"

	"github.com/skzv/ccmux/internal/config"
)

// TestResolveTarget_HonorsSSHPortField — the new SSHPort field is
// the preferred source of the SSH port. Setting Port (the ccmuxd
// HTTP port) shouldn't accidentally hijack the SSH dial.
func TestResolveTarget_HonorsSSHPortField(t *testing.T) {
	cfg := config.Config{
		Hosts: []config.Host{
			{Name: "mini", Address: "sputnik", User: "alice", Port: 7474, SSHPort: 2222},
		},
	}
	got, _, err := resolveTarget("mini", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 2222 {
		t.Errorf("Port = %d, want 2222 (SSHPort wins over Port)", got.Port)
	}
}

// TestResolveTarget_DefaultsTo22WhenSSHPortUnset — a config that
// doesn't set SSHPort and has Port=7474 (ccmuxd) must still resolve
// to SSH port 22.
func TestResolveTarget_DefaultsTo22WhenSSHPortUnset(t *testing.T) {
	cfg := config.Config{
		Hosts: []config.Host{
			{Name: "mini", Address: "sputnik", User: "alice", Port: 7474},
		},
	}
	got, _, err := resolveTarget("mini", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 22 {
		t.Errorf("Port = %d, want 22 (default)", got.Port)
	}
}

// TestResolveTarget_BackwardsCompatLegacyPortAsSSHPort — pre-
// SSHPort configs sometimes set Port to a non-7474 value expecting
// it to be the SSH port. We honor that interpretation as a
// migration kindness so users who hand-edited hosts.toml don't
// silently lose their custom port. New configs should set
// SSHPort explicitly.
func TestResolveTarget_BackwardsCompatLegacyPortAsSSHPort(t *testing.T) {
	cfg := config.Config{
		Hosts: []config.Host{
			{Name: "old", Address: "sputnik", User: "alice", Port: 2200, SSHPort: 0},
		},
	}
	got, _, err := resolveTarget("old", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 2200 {
		t.Errorf("Port = %d, want 2200 (legacy Port=2200 should be honored when SSHPort is zero)", got.Port)
	}
}
