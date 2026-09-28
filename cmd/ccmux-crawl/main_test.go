package main

import (
	"fmt"
	"os"
	"testing"
)

// TestMain runs the package's tests inside the same throwaway home the
// binary uses: the scenario tests drive the real TUI (and drain its
// commands), which would otherwise read and write the developer's own
// config and reach their tmux server during `go test ./...`.
func TestMain(m *testing.M) {
	_, cleanup, err := enterSandbox()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccmux-crawl tests:", err)
		os.Exit(1)
	}
	code := m.Run()
	cleanup()
	os.Exit(code)
}
