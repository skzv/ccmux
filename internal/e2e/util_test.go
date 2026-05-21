//go:build integration

package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/skzv/ccmux/internal/daemon"
)

// mkdirAll creates a directory tree, failing the test on error.
func mkdirAll(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
}

// writeFile writes content to p, creating parent directories.
func writeFile(t *testing.T, p, content string) {
	t.Helper()
	mkdirAll(t, filepath.Dir(p))
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

// readFile reads p, failing the test on error.
func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

// exists reports whether a path exists.
func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// sessionNamesOf extracts just the Name field from a slice of SessionState.
func sessionNamesOf(ss []daemon.SessionState) []string {
	names := make([]string, len(ss))
	for i, s := range ss {
		names[i] = s.Name
	}
	return names
}

// containsSession reports whether any SessionState in ss has the given name.
func containsSession(ss []daemon.SessionState, name string) bool {
	for _, s := range ss {
		if s.Name == name {
			return true
		}
	}
	return false
}
