//go:build integration && unix

package tmux

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestIntegration_StoppedServerDoesNotHangCalls — with the tmux server
// SIGSTOP'd, a call whose context expired kept waiting on the output
// pipe the client had handed the (stopped) server, so the daemon's poll
// loop and every /v1/sessions request stalled for as long as the server
// stayed stopped. Each call must now come back shortly after its
// context does.
func TestIntegration_StoppedServerDoesNotHangCalls(t *testing.T) {
	ctx := isolatedServer(t)
	if err := New(ctx, "c-stop", os.TempDir(), "sleep 300"); err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(ctx, "tmux", "display-message", "-p", "-t", exactSession("c-stop"), "#{pid}").Output()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || pid <= 1 {
		t.Fatalf("tmux server pid = %q", out)
	}
	if err := syscall.Kill(pid, syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	// Registered after isolatedServer's cleanup, so it runs first: the
	// server must be running again for kill-server to reach it.
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGCONT) })

	for name, call := range map[string]func(context.Context) error{
		"List":        func(ctx context.Context) error { _, err := List(ctx); return err },
		"CapturePane": func(ctx context.Context) error { _, err := CapturePane(ctx, "c-stop", 10); return err },
		"SendKeys":    func(ctx context.Context) error { return SendKeys(ctx, "c-stop", "x") },
	} {
		callCtx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		done := make(chan error, 1)
		go func() { done <- call(callCtx) }()
		select {
		case err := <-done:
			if err == nil {
				t.Errorf("%s against a stopped server reported success", name)
			}
		case <-time.After(300*time.Millisecond + commandWaitDelay + 3*time.Second):
			t.Errorf("%s still blocked well after its 300ms context expired", name)
		}
		cancel()
	}
}
