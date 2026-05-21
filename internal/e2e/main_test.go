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
	"testing"
)

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
	code := m.Run()
	if binDir != "" {
		_ = os.RemoveAll(binDir)
	}
	os.Exit(code)
}
