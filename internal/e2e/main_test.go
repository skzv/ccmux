//go:build integration

// Package e2e holds ccmux's end-to-end tests. They build the real
// `ccmux` and `ccmuxd` binaries and drive them against an isolated
// tmux server, a temp $HOME, and a temp projects root — so a run
// never touches the developer's real sessions, transcripts, or config.
//
// Run with: make test-e2e  (or `go test -tags=integration ./internal/e2e/...`)
package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
)

// dirsWatchdog removes its arguments once the test binary ($PPID) has
// exited.
const dirsWatchdog = `while kill -0 "$PPID" 2>/dev/null; do
	sleep 1 & wait $!
done
rm -rf "$0" "$@"
`

// TestMain builds the binaries once for the whole package run, then
// cleans the build dir. Every test reuses the same artifacts.
func TestMain(m *testing.M) {
	if _, err := exec.LookPath("go"); err != nil {
		fmt.Fprintln(os.Stderr, "e2e: go toolchain not on PATH")
		os.Exit(1)
	}
	if err := buildBinaries(); err != nil {
		fmt.Fprintln(os.Stderr, "e2e: "+err.Error())
		os.Exit(1)
	}
	if err := installStubAgents(); err != nil {
		fmt.Fprintln(os.Stderr, "e2e: install stub agents: "+err.Error())
		os.Exit(1)
	}
	// The removals below never run when the package dies early (a
	// -timeout panic, ^C), so a watchdog removes the dirs once this
	// process is gone, whichever way it went (after a normal exit they
	// are already removed). Its own process group keeps a ^C aimed at
	// the tests from killing it first.
	watchdog := exec.Command("sh", "-c", dirsWatchdog, binDir, stubBinDir)
	watchdog.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	_ = watchdog.Start()
	code := m.Run()
	if binDir != "" {
		_ = os.RemoveAll(binDir)
	}
	if stubBinDir != "" {
		_ = os.RemoveAll(stubBinDir)
	}
	os.Exit(code)
}
