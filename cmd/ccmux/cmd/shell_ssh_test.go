package cmd

import (
	"reflect"
	"testing"

	"github.com/skzv/ccmux/internal/config"
)

// TestShellSSHArgs — `ccmux shell --host` must honor the host's
// configured SSH user and port, matching the TUI attach path. The old
// inline code used the bare address with no user@ and no -p, so a host
// with a non-local username or non-22 sshd port failed.
func TestShellSSHArgs(t *testing.T) {
	const cmd = "tmux attach -t c-x"
	cases := []struct {
		name string
		host config.Host
		want []string
	}{
		{
			"user + default port",
			config.Host{User: "skz", Address: "100.64.0.5"},
			[]string{"-t", "skz@100.64.0.5", cmd},
		},
		{
			"no user falls back to bare address",
			config.Host{Address: "100.64.0.5"},
			[]string{"-t", "100.64.0.5", cmd},
		},
		{
			"custom sshd port adds -p",
			config.Host{User: "skz", Address: "mini", SSHPort: 2222},
			[]string{"-t", "-p", "2222", "skz@mini", cmd},
		},
		{
			"explicit port 22 stays bare",
			config.Host{User: "skz", Address: "mini", SSHPort: 22},
			[]string{"-t", "skz@mini", cmd},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shellSSHArgs(tc.host, cmd)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("shellSSHArgs = %v, want %v", got, tc.want)
			}
		})
	}
}
