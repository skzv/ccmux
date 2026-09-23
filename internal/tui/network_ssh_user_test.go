package tui

import (
	"os/exec"
	"testing"
)

// TestNetwork_SSHCmd_HonorsConfiguredUser — Enter on the Network tab
// dialed the bare host, so ssh logged in as the LOCAL username even
// when the host is configured with `user = "…"` (which the Sessions
// attach and `ccmux shell` both honor). The configured user must reach
// ssh, and the post-failure wizard target must carry it too.
func TestNetwork_SSHCmd_HonorsConfiguredUser(t *testing.T) {
	cases := []struct {
		name       string
		host       hostStatus
		wantTarget string
		wantUser   string // attachRemoteTarget.User; "" = don't check
	}{
		{"configured user", hostStatus{Name: "mini", Source: "configured", DialHost: "mac-mini", User: "sasha", SSHPort: 2222, OK: true},
			"sasha@mac-mini", "sasha"},
		{"no configured user", hostStatus{Name: "mini", Discovered: true, DialHost: "mac-mini", OK: true},
			"mac-mini", ""},
		{"dial already has user", hostStatus{Name: "mini", DialHost: "admin@mac-mini", User: "sasha", OK: true},
			"admin@mac-mini", "admin"},
		{"address fallback with user", hostStatus{Name: "mini", Address: "100.64.0.2:7474", User: "sasha", OK: true},
			"sasha@100.64.0.2", "sasha"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotTarget string
			var gotPort int
			orig := sshInteractiveCmd
			sshInteractiveCmd = func(target string, port int) *exec.Cmd {
				gotTarget, gotPort = target, port
				return exec.Command("true")
			}
			t.Cleanup(func() { sshInteractiveCmd = orig })

			m := newNetwork(mustStyles(t), DefaultKeymap())
			m.SetHosts([]hostStatus{tc.host})
			if cmd := m.SSHCmd(); cmd == nil {
				t.Fatal("SSHCmd returned nil for an ssh-able host")
			}
			if gotTarget != tc.wantTarget {
				t.Errorf("ssh target = %q, want %q", gotTarget, tc.wantTarget)
			}
			if gotPort != tc.host.SSHPort {
				t.Errorf("ssh port = %d, want %d", gotPort, tc.host.SSHPort)
			}
			if tc.wantUser != "" {
				rt := remoteTargetForSSH(tc.host, gotTarget)
				if rt.User != tc.wantUser {
					t.Errorf("wizard target user = %q, want %q", rt.User, tc.wantUser)
				}
			}
		})
	}
}
