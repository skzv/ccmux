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
