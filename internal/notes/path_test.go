package notes

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCleanRel(t *testing.T) {
	for in, want := range map[string]string{
		"docs/a.md":      "docs/a.md",
		"./docs//a.md":   "docs/a.md",
		"docs/x/../a.md": "docs/a.md",
		"..notes.md":     "..notes.md", // a name, not a traversal
		"  spaced.md  ":  "spaced.md",
	} {
		got, err := CleanRel(in)
		if err != nil || got != want {
			t.Errorf("CleanRel(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "/etc/passwd", "../secret.md", "docs/../../x.md", "..", "a\x00b.md"} {
		if _, err := CleanRel(bad); err == nil {
			t.Errorf("CleanRel(%q) accepted a path outside the vault", bad)
		}
	}
}

// TestVaultRead_RejectsTraversal — Read is reachable from the TUI as well
// as the daemon, so it enforces the containment itself.
func TestVaultRead_RejectsTraversal(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secret.md"), []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(proj).Read("../secret.md"); !errors.Is(err, ErrOutsideVault) {
		t.Errorf("Read(../secret.md) err = %v, want ErrOutsideVault", err)
	}
}
