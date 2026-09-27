//go:build unix

package claudemodels

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestClaudeCLIFetcher_CancelKillsChildrenAndReturns — a `claude -p`
// whose child outlived it held the output pipes open, so a timed-out or
// cancelled Fetch kept blocking (the daemon logged "background loops
// still running after 5s" on shutdown) and the child survived. Cancel
// must now kill the whole tree and Fetch return promptly.
func TestClaudeCLIFetcher_CancelKillsChildrenAndReturns(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	bin := filepath.Join(dir, "claude")
	script := "#!/bin/sh\nsleep 30 &\necho $! > '" + pidFile + ".tmp'\nmv '" + pidFile + ".tmp' '" + pidFile + "'\nsleep 30\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	childPID := func() int {
		raw, err := os.ReadFile(pidFile)
		if err != nil {
			return 0
		}
		pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
		return pid
	}
	t.Cleanup(func() {
		if pid := childPID(); pid > 0 {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := ClaudeCLIFetcher{Binary: bin}.Fetch(ctx)
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for childPID() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("fake claude never started its child")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a cancelled fetch reported success")
		}
	case <-time.After(cliWaitDelay + 5*time.Second):
		t.Fatal("Fetch still blocked well after its context was cancelled")
	}
	// The child is killed with the group; allow a moment for the reap.
	gone := false
	for range 50 {
		if err := syscall.Kill(childPID(), 0); errors.Is(err, syscall.ESRCH) {
			gone = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !gone {
		t.Error("claude's child process survived the cancelled fetch")
	}
}
