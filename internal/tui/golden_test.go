package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goldenAssert compares `got` against the file at the given relative
// path under internal/tui/testdata/golden/. When the environment
// variable CCMUX_UPDATE_GOLDEN=1 is set, it rewrites the file with
// `got` and passes — that's the regenerate workflow used after a
// deliberate visual change.
//
// `got` is rendered into the file as-is (including ANSI escapes
// from lipgloss). Snapshots are intentionally raw so they pin the
// exact terminal output a user would see; a future "human-readable
// diff" pass can strip ANSI for review without changing the artifact.
//
// See testdata/golden/README.md for the full workflow.
func goldenAssert(t *testing.T, relPath, got string) {
	t.Helper()
	full := filepath.Join("testdata", "golden", relPath)

	if os.Getenv("CCMUX_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir golden: %v", err)
		}
		if err := os.WriteFile(full, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		// Pass — regenerate workflow.
		return
	}

	want, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("read golden %s (run with CCMUX_UPDATE_GOLDEN=1 to create): %v", full, err)
	}
	if string(want) != got {
		t.Errorf("golden mismatch: %s\nrun `CCMUX_UPDATE_GOLDEN=1 go test ./internal/tui/...` to regenerate.\n%s",
			full, unifiedDiff(string(want), got))
	}
}

// unifiedDiff produces a tiny per-line diff readable in test output.
// Not RFC-grade, just enough to point a human at where the snapshot
// drifted.
func unifiedDiff(want, got string) string {
	w := strings.Split(want, "\n")
	g := strings.Split(got, "\n")
	var b strings.Builder
	n := len(w)
	if len(g) > n {
		n = len(g)
	}
	for i := 0; i < n; i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			b.WriteString("- ")
			b.WriteString(wl)
			b.WriteString("\n+ ")
			b.WriteString(gl)
			b.WriteString("\n")
		}
	}
	return b.String()
}
