package tmux

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeTmux installs an executable `tmux` shim on PATH that prints
// `stderrMsg` to stderr and exits with `code`.
func fakeTmux(t *testing.T, stderrMsg string, code int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("sh-script tmux fake is unix-only")
	}
	dir := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\necho %q >&2\nexit %d\n", stderrMsg, code)
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake tmux: %v", err)
	}
	t.Setenv("PATH", dir)
}

// loggingTmux installs a `tmux` shim on PATH that succeeds and appends
// each invocation's arguments to the returned log, one line per call,
// every argument followed by "|".
func loggingTmux(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("sh-script tmux fake is unix-only")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "tmux.log")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s|' \"$a\"; done >> '" + logPath + "'\necho >> '" + logPath + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake tmux: %v", err)
	}
	t.Setenv("PATH", dir)
	return logPath
}

// TestNewWithAgent_TagsInTheSameInvocation — the agent tag used to be
// set by a second tmux call after new-session, so a daemon poll tick in
// between saw the session untagged and classified it by its project's
// agent. The tag must ride on the invocation that creates the session.
func TestNewWithAgent_TagsInTheSameInvocation(t *testing.T) {
	logPath := loggingTmux(t)
	if err := NewWithAgent(context.Background(), "c-x", "/work", "zsh", ShellAgentTag); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	calls := strings.Split(strings.TrimSpace(string(raw)), "\n")
	want := "new-session|-d|-s|c-x|-c|/work|zsh|;|set-option|-t|=c-x:|@ccmux_agent|shell|"
	if len(calls) != 1 || calls[0] != want {
		t.Errorf("tmux calls = %q, want exactly [%q]", calls, want)
	}

	// No tag: a plain new-session, no separator.
	_ = os.Remove(logPath)
	if err := NewWithAgent(context.Background(), "c-y", "", "", ""); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(logPath)
	if got := strings.TrimSpace(string(raw)); got != "new-session|-d|-s|c-y|" {
		t.Errorf("untagged call = %q", got)
	}
}

// TestCapturePane_ErrorIncludesStderr — finding: CapturePane used
// cmd.Output(), whose *exec.ExitError stringifies as a bare
// "exit status 1" with tmux's diagnostic hidden in ExitError.Stderr.
// The daemon's preview handler matched err.Error() against
// "can't find session" and therefore never mapped a dead session to
// 404. The wrapped error must now carry the stderr text.
func TestCapturePane_ErrorIncludesStderr(t *testing.T) {
	fakeTmux(t, "can't find session: nope", 1)
	_, err := CapturePane(context.Background(), "nope", 10)
	if err == nil {
		t.Fatal("expected an error from the failing tmux fake")
	}
	if !strings.Contains(err.Error(), "can't find session") {
		t.Errorf("error %q should include tmux's stderr diagnostic", err)
	}
}

// TestList_ErrorIncludesStderr — same contract for List. Exit 1 is the
// documented "no server running" success path, so the fake exits 2 to
// force a real error.
func TestList_ErrorIncludesStderr(t *testing.T) {
	fakeTmux(t, "server exited unexpectedly", 2)
	_, err := List(context.Background())
	if err == nil {
		t.Fatal("expected an error from the failing tmux fake")
	}
	if !strings.Contains(err.Error(), "server exited unexpectedly") {
		t.Errorf("error %q should include tmux's stderr diagnostic", err)
	}
}

// TestList_ExitOneStillMeansNoServer — pin that the stderr wrapping
// did not disturb the "tmux exits 1 when no server is running" success
// mapping.
func TestList_ExitOneStillMeansNoServer(t *testing.T) {
	fakeTmux(t, "no server running on /tmp/tmux-501/default", 1)
	tss, err := List(context.Background())
	if err != nil {
		t.Fatalf("exit 1 must map to (nil, nil), got err %v", err)
	}
	if tss != nil {
		t.Errorf("expected no sessions, got %v", tss)
	}
}

// TestList_ExitOneOnlyMeansNoServerWhenTmuxSaysSo — tmux exits 1 for
// every failure. List read any exit 1 as "no sessions", so a socket the
// daemon can't open (permissions) or a server that died mid-command made
// the daemon's cleanup pass forget every session and publish "killed"
// for each. Only tmux's two "there is no server" messages mean empty.
func TestList_ExitOneOnlyMeansNoServerWhenTmuxSaysSo(t *testing.T) {
	for _, tc := range []struct {
		stderr    string
		wantEmpty bool
	}{
		{"no server running on /tmp/tmux-501/default", true},
		{"error connecting to /tmp/tmux-501/default (No such file or directory)", true},
		{"error connecting to /tmp/tmux-501/default (Permission denied)", false},
		{"error connecting to /tmp/tmux-501/default (Connection reset by peer)", false},
		{"server exited unexpectedly", false},
		{"lost server", false},
	} {
		t.Run(tc.stderr, func(t *testing.T) {
			fakeTmux(t, tc.stderr, 1)
			tss, err := List(context.Background())
			if tc.wantEmpty {
				if err != nil || tss != nil {
					t.Errorf("List = %v, %v; want no sessions and no error", tss, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("List treated %q as an empty server", tc.stderr)
			}
			if !strings.Contains(err.Error(), tc.stderr) {
				t.Errorf("error %q should carry tmux's message", err)
			}
		})
	}
}
