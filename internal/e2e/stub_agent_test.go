//go:build integration

package e2e

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// processAlive reports whether pid still runs (and isn't a zombie we
// could still signal: the stub is reparented, so nobody reaps it but
// init, which does so promptly).
func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// startStubUnder runs the stub agent script as a background child of a
// short-lived parent shell that exits after parentSeconds, and returns
// the stub's pid. The stub outlives its parent, as it does when a test
// run dies before its cleanup kills the sandbox tmux server.
func startStubUnder(t *testing.T, script string, parentSeconds int) int {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", fmt.Sprintf(`"$0" >/dev/null 2>&1 & echo $!; sleep %d`, parentSeconds), path)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatalf("read stub pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("stub pid %q: %v", line, err)
	}
	// Whatever happens below, don't leave the stub (or its sleep) behind.
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	if err := cmd.Wait(); err != nil {
		t.Fatalf("parent shell: %v", err)
	}
	return pid
}

// TestStubAgent_ExitsWithItsParent — the stub agent was `exec sleep
// 86400`, so a run that died before its cleanup (a -timeout panic, ^C)
// left it running for a day; orphaned `sleep 86400`s piled up across
// runs. It must now exit on its own once the process that started it
// is gone.
func TestStubAgent_ExitsWithItsParent(t *testing.T) {
	pid := startStubUnder(t, fmt.Sprintf(stubAgentScript, "", stubAgentMaxSeconds), 1)
	if !waitFor(5*time.Second, func() bool { return !processAlive(pid) }) {
		t.Fatalf("stub agent %d still running 5s after its parent exited", pid)
	}
}

// TestStubAgent_LifetimeCapped — even under a parent that stays up (a
// wedged tmux server), the stub gives up after its cap.
func TestStubAgent_LifetimeCapped(t *testing.T) {
	script := fmt.Sprintf(stubAgentScript, "", 2)
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := time.Now()
	if out, err := exec.CommandContext(ctx, path).CombinedOutput(); err != nil {
		t.Fatalf("stub: %v\n%s", err, out)
	}
	if d := time.Since(start); d > 6*time.Second {
		t.Errorf("stub with a 2s cap ran %v", d)
	}
}
