package selfupdate

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestRealGit_NeverPrompts — the startup update check ran `git fetch`
// with the user's plain environment, so a remote needing credentials
// or an ssh key passphrase prompted on the terminal the TUI was about
// to take over. Every git call must carry GIT_TERMINAL_PROMPT=0 and an
// ssh command with BatchMode=yes, appended to (not replacing) the ssh
// command git would otherwise use.
func TestRealGit_NeverPrompts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a /bin/sh git stub")
	}
	bin := t.TempDir()
	// The stub answers `git -C <dir> config --get core.sshCommand` from
	// $FAKE_CORE_SSH (exit 1 = unset, like git) and prints the
	// prompt-related environment for anything else.
	stub := `#!/bin/sh
if [ "$3" = "config" ]; then
  [ -n "$FAKE_CORE_SSH" ] || exit 1
  printf '%s\n' "$FAKE_CORE_SSH"
  exit 0
fi
printf '%s|%s\n' "$GIT_TERMINAL_PROMPT" "$GIT_SSH_COMMAND"
`
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	cases := []struct {
		name string
		env  map[string]string // "" value = unset
		want string
	}{
		{"defaults", map[string]string{}, "0|ssh -o BatchMode=yes"},
		{"prompt explicitly on", map[string]string{"GIT_TERMINAL_PROMPT": "1"}, "0|ssh -o BatchMode=yes"},
		{"GIT_SSH_COMMAND kept", map[string]string{"GIT_SSH_COMMAND": "ssh -i /keys/deploy"}, "0|ssh -i /keys/deploy -o BatchMode=yes"},
		{"core.sshCommand kept", map[string]string{"FAKE_CORE_SSH": "ssh -p 2222"}, "0|ssh -p 2222 -o BatchMode=yes"},
		{"GIT_SSH_COMMAND beats core.sshCommand", map[string]string{"GIT_SSH_COMMAND": "ssh -4", "FAKE_CORE_SSH": "ssh -p 2222"}, "0|ssh -4 -o BatchMode=yes"},
		{"bare GIT_SSH left alone", map[string]string{"GIT_SSH": "/usr/local/bin/plink"}, "0|"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range []string{"GIT_TERMINAL_PROMPT", "GIT_SSH_COMMAND", "GIT_SSH", "FAKE_CORE_SSH"} {
				t.Setenv(k, tc.env[k])
				if tc.env[k] == "" {
					_ = os.Unsetenv(k)
				}
			}
			got, err := realGit(context.Background(), t.TempDir(), "fetch", "--quiet", "origin")
			if err != nil {
				t.Fatalf("realGit: %v", err)
			}
			if got != tc.want {
				t.Errorf("git saw GIT_TERMINAL_PROMPT|GIT_SSH_COMMAND = %q, want %q", got, tc.want)
			}
		})
	}
}
