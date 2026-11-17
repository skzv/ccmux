//go:build !windows

package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestClipboardPipeHelperClient is not a real test: startFakeClient
// runs it as a stand-in tmux client process whose environment `ccmux
// clipboard-pipe` inspects. The test binary rather than /bin/sleep
// because macOS hides a platform binary's environment from `ps -E`.
func TestClipboardPipeHelperClient(t *testing.T) {
	if os.Getenv("CCMUX_CLIPBOARD_HELPER_CLIENT") != "1" {
		t.Skip("stand-in client process for TestCLI_ClipboardPipe_RoutesByCopyingClient")
	}
	time.Sleep(60 * time.Second)
}

// startFakeClient starts a process that looks like a tmux client
// attached from this machine (remote=false) or over SSH (remote=true)
// and returns its PID.
func startFakeClient(t *testing.T, remote bool) int {
	t.Helper()
	var env []string
	for _, kv := range os.Environ() {
		// A developer running the suite over SSH must not make the
		// "local" client look remote.
		if !strings.HasPrefix(kv, "SSH_CONNECTION=") && !strings.HasPrefix(kv, "CCMUX_REMOTE_CLIENT=") {
			env = append(env, kv)
		}
	}
	env = append(env, "CCMUX_CLIPBOARD_HELPER_CLIENT=1")
	if remote {
		env = append(env, "SSH_CONNECTION=100.64.0.2 50000 100.64.0.1 22")
	}
	c := exec.Command(os.Args[0], "-test.run=^TestClipboardPipeHelperClient$")
	c.Env = env
	if err := c.Start(); err != nil {
		t.Fatalf("start stand-in client: %v", err)
	}
	t.Cleanup(func() {
		_ = c.Process.Kill()
		_ = c.Wait()
	})
	return c.Process.Pid
}

// TestCLI_ClipboardPipe_RoutesByCopyingClient — `ccmux clipboard-pipe
// --client <tty>` decides by the client that made the selection. With
// a Mac (local) and a phone (SSH) attached at once, the phone's copy
// stays off this machine's clipboard and the Mac's lands on it. With no
// usable --client — an older binding, an unexpanded format, a tty that
// isn't an attached client — it falls back to "a local client is
// attached".
func TestCLI_ClipboardPipe_RoutesByCopyingClient(t *testing.T) {
	mac := startFakeClient(t, false)
	phone := startFakeClient(t, true)

	e := newCLIEnv(t)
	clip := filepath.Join(e.home, "clipboard")
	e.writeExe("tmux", `if [ "$1" = list-clients ]; then printf '%s\n' "$FAKE_CLIENTS"; fi
exit 0
`)
	e.env["FAKE_CLIENTS"] = fmt.Sprintf("%d /dev/ttys001\n%d /dev/ttys002", mac, phone)
	// The native clipboard tools the pipe looks up on PATH: pbcopy on
	// macOS, wl-copy first on Linux.
	for _, tool := range []string{"pbcopy", "wl-copy"} {
		e.writeExe(tool, `/bin/cat >> "$FAKE_CLIPBOARD"`)
	}
	e.env["FAKE_CLIPBOARD"] = clip
	if runtime.GOOS == "darwin" {
		e.writeExe("ps", `exec /bin/ps "$@"`) // reads the client's env
	}

	cases := []struct {
		name string
		args []string
		want bool // this machine's clipboard gets the selection
	}{
		{"phone copies while mac attached", []string{"--client", "/dev/ttys002"}, false},
		{"mac copies while phone attached", []string{"--client", "/dev/ttys001"}, true},
		{"older binding without --client", nil, true},
		{"unexpanded format", []string{"--client", "#{client_tty}"}, true},
		{"tty that is not an attached client", []string{"--client", "/dev/ttys999"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_ = os.Remove(clip)
			sel := filepath.Join(t.TempDir(), "selection")
			if err := os.WriteFile(sel, []byte("copied text"), 0o600); err != nil {
				t.Fatal(err)
			}
			in, err := os.Open(sel)
			if err != nil {
				t.Fatal(err)
			}
			defer in.Close()
			res := e.runStdin("", in, append([]string{"clipboard-pipe"}, tc.args...)...)
			if res.code != 0 {
				t.Fatalf("clipboard-pipe exited %d: %s", res.code, res.stderr)
			}
			got, _ := os.ReadFile(clip)
			if written := string(got) == "copied text"; written != tc.want {
				t.Errorf("local clipboard written = %v (%q), want %v", written, got, tc.want)
			}
		})
	}
}
