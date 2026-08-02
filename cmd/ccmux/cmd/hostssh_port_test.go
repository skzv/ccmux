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

// TestSSHTargetForHost_PortAndUserPrecedence — the shared helper behind
// both `host setup-ssh` and `ccmux doctor`. Regression for doctor
// hardcoding Port: 22 and ignoring ssh_port: the doctor probe must use
// the exact same target construction as every other SSH call site.
func TestSSHTargetForHost_PortAndUserPrecedence(t *testing.T) {
	cases := []struct {
		name     string
		host     config.Host
		wantPort int
		wantUser string
	}{
		{
			name:     "ssh_port wins",
			host:     config.Host{Name: "mini", Address: "sputnik", User: "alice", Port: 7474, SSHPort: 2222},
			wantPort: 2222,
			wantUser: "alice",
		},
		{
			name:     "ccmuxd port 7474 does not hijack ssh port",
			host:     config.Host{Name: "mini", Address: "sputnik", User: "alice", Port: 7474},
			wantPort: 22,
			wantUser: "alice",
		},
		{
			name:     "legacy non-7474 Port honored when ssh_port unset",
			host:     config.Host{Name: "old", Address: "sputnik", User: "alice", Port: 2200},
			wantPort: 2200,
			wantUser: "alice",
		},
		{
			name:     "bare host defaults to 22",
			host:     config.Host{Name: "bare", Address: "sputnik", User: "alice"},
			wantPort: 22,
			wantUser: "alice",
		},
		{
			name:     "empty user falls back to local user",
			host:     config.Host{Name: "mini", Address: "sputnik", SSHPort: 2222},
			wantPort: 2222,
			wantUser: currentUser(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sshTargetForHost(tc.host)
			if got.Port != tc.wantPort {
				t.Errorf("Port = %d, want %d", got.Port, tc.wantPort)
			}
			if got.User != tc.wantUser {
				t.Errorf("User = %q, want %q", got.User, tc.wantUser)
			}
			if got.Host != tc.host.Address {
				t.Errorf("Host = %q, want %q", got.Host, tc.host.Address)
			}
		})
	}
}
