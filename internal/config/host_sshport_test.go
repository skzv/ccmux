package config

import "testing"

// TestHost_EffectiveSSHPort pins the single canonical fallback:
// SSHPort=0 → 22 (openssh default), otherwise the explicit value.
// Every TUI/CLI dial site goes through this helper so a future
// "change default to 2200 because reasons" is a one-line edit.
func TestHost_EffectiveSSHPort(t *testing.T) {
	cases := []struct {
		name string
		in   Host
		want int
	}{
		{"zero-defaults-to-22", Host{}, 22},
		{"explicit-22", Host{SSHPort: 22}, 22},
		{"explicit-2222", Host{SSHPort: 2222}, 2222},
		{"explicit-1", Host{SSHPort: 1}, 1},
		{"explicit-65535", Host{SSHPort: 65535}, 65535},
		// Port (the ccmuxd HTTP port) being set does NOT affect
		// SSHPort — they're independent. A host with Port=7474 +
		// SSHPort=0 still SSH-defaults to 22.
		{"ccmuxd-port-set-ssh-default", Host{Port: 7474}, 22},
		{"both-set-independent", Host{Port: 7474, SSHPort: 2222}, 2222},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.in.EffectiveSSHPort(); got != c.want {
				t.Errorf("EffectiveSSHPort = %d, want %d", got, c.want)
			}
		})
	}
}
