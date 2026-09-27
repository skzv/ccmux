//go:build unix

package tmux

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// orphanHoldingTmux installs a `tmux` shim that leaves a background
// child holding its stdout and stderr and then blocks, the way a tmux
// client talking to a SIGSTOP'd server leaves the server holding the
// client's output pipes. It returns the file the shim writes the
// child's pid to once the child is running.
func orphanHoldingTmux(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "orphan.pid")
	script := "#!/bin/sh\nsleep 30 &\necho $! > '" + pidFile + ".tmp'\nmv '" + pidFile + ".tmp' '" + pidFile + "'\nsleep 30\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":/bin:/usr/bin")
	t.Cleanup(func() {
		if raw, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	return pidFile
}

// TestCommand_CancelledCallReturnsWhilePipeIsHeld — a cancelled tmux
// call kept waiting for EOF on an output pipe something else still held
// open, so the context didn't bound it at all: with a stopped tmux
// server, the daemon's poll loop and /v1/sessions hung until the server
// ran again.
func TestCommand_CancelledCallReturnsWhilePipeIsHeld(t *testing.T) {
	for name, call := range map[string]func(context.Context) error{
		"CapturePane": func(ctx context.Context) error { _, err := CapturePane(ctx, "c-x", 10); return err },
		"List":        func(ctx context.Context) error { _, err := List(ctx); return err },
		"Kill":        func(ctx context.Context) error { return Kill(ctx, "c-x") },
	} {
		t.Run(name, func(t *testing.T) {
			pidFile := orphanHoldingTmux(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- call(ctx) }()
			// Cancel only once the pipe-holding child exists.
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(pidFile); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("tmux shim never started its child")
				}
				time.Sleep(10 * time.Millisecond)
			}
			cancel()
			cancelled := time.Now()
			select {
			case err := <-done:
				if err == nil {
					t.Error("a killed tmux call reported success")
				}
				if took := time.Since(cancelled); took > commandWaitDelay+2*time.Second {
					t.Errorf("call took %s to return after its context was cancelled", took)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("tmux call still blocked 10s after its context was cancelled")
			}
		})
	}
}
