package tui

import (
	"errors"
	"testing"
)

// TestRemoteAttachTargetFromErr_ClassifiesAuthFailures pins the
// post-attach probe classifier. Substrings cover the three shapes
// ssh / mosh emit on auth failures across openssh versions.
func TestRemoteAttachTargetFromErr_ClassifiesAuthFailures(t *testing.T) {
	cases := []struct {
		name    string
		msg     attachExitedMsg
		wantNil bool
	}{
		{
			name: "permission-denied-publickey",
			msg: attachExitedMsg{
				Err:             errors.New("exit status 255: Permission denied (publickey)"),
				RemoteSSHTarget: &attachRemoteTarget{User: "alice", Host: "sputnik", Port: 22},
			},
			wantNil: false,
		},
		{
			name: "publickey-bare",
			msg: attachExitedMsg{
				Err:             errors.New("publickey,password,keyboard-interactive"),
				RemoteSSHTarget: &attachRemoteTarget{User: "alice", Host: "sputnik", Port: 22},
			},
			wantNil: false,
		},
		{
			name: "too-many-auth-failures",
			msg: attachExitedMsg{
				Err:             errors.New("exit status 255"),
				Stderr:          "Received disconnect from 10.0.0.2 port 22:2: Too many authentication failures\r\n",
				RemoteSSHTarget: &attachRemoteTarget{User: "alice", Host: "sputnik", Port: 22},
			},
			wantNil: false,
		},
		// ssh exits 255 for every failure of its own; only an auth
		// refusal is something the SSH setup wizard can fix. These used
		// to open the full-screen wizard over the real error.
		{
			name: "exit-255-bare",
			msg: attachExitedMsg{
				Err:             errors.New("ssh: exit status 255"),
				RemoteSSHTarget: &attachRemoteTarget{User: "alice", Host: "sputnik", Port: 22},
			},
			wantNil: true,
		},
		{
			name: "connection-refused",
			msg: attachExitedMsg{
				Err:             errors.New("exit status 255"),
				Stderr:          "ssh: connect to host 127.0.0.9 port 22: Connection refused\r\n",
				RemoteSSHTarget: &attachRemoteTarget{User: "me", Host: "127.0.0.9", Port: 22},
			},
			wantNil: true,
		},
		{
			name: "host-key-changed",
			msg: attachExitedMsg{
				Err:             errors.New("exit status 255"),
				Stderr:          "@@@ WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED! @@@\nHost key verification failed.\n",
				RemoteSSHTarget: &attachRemoteTarget{User: "me", Host: "mini", Port: 22},
			},
			wantNil: true,
		},
		{
			name: "remote-tmux-socket-permission",
			msg: attachExitedMsg{
				Err:             errors.New("exit status 1"),
				Stderr:          "error connecting to /tmp/tmux-501/default (Permission denied)\n",
				RemoteSSHTarget: &attachRemoteTarget{User: "me", Host: "mini", Port: 22},
			},
			wantNil: true,
		},
		{
			name: "non-auth-error",
			msg: attachExitedMsg{
				Err:             errors.New("session not found: foo"),
				RemoteSSHTarget: &attachRemoteTarget{User: "alice", Host: "sputnik", Port: 22},
			},
			wantNil: true,
		},
		{
			name: "no-target-context",
			msg: attachExitedMsg{
				Err: errors.New("Permission denied (publickey)"),
			},
			wantNil: true,
		},
		{
			name: "no-error",
			msg: attachExitedMsg{
				RemoteSSHTarget: &attachRemoteTarget{User: "alice", Host: "sputnik"},
			},
			wantNil: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := remoteAttachTargetFromErr(c.msg)
			if c.wantNil && got != nil {
				t.Errorf("expected nil target, got %+v", got)
			}
			if !c.wantNil && got == nil {
				t.Errorf("expected non-nil target, got nil")
			}
			if got != nil {
				if got.User != c.msg.RemoteSSHTarget.User || got.Host != c.msg.RemoteSSHTarget.Host {
					t.Errorf("target = %+v, want User=%q Host=%q", got, c.msg.RemoteSSHTarget.User, c.msg.RemoteSSHTarget.Host)
				}
			}
		})
	}
}

// TestNetworkHostShortName — pins the short-name derivation used
// for auto-generated host entries.
func TestNetworkHostShortName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"sputnik", "sputnik"},
		{"sputnik.tail-1234.ts.net", "sputnik"},
		{"100.64.1.1", "100"},
		{"", ""},
	}
	for _, c := range cases {
		if got := networkHostShortName(c.in); got != c.want {
			t.Errorf("networkHostShortName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
