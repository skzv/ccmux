package notes

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestVaultCreate_AppendsExtCreatesDirsAndRefusesOverwrite(t *testing.T) {
	root := t.TempDir()
	v := Open(root)

	rel, abs, err := v.Create("docs/specs/plan", "Launch Plan")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if rel != "docs/specs/plan.md" {
		t.Errorf("rel = %q, want docs/specs/plan.md", rel)
	}
	if abs != filepath.Join(root, "docs", "specs", "plan.md") {
		t.Errorf("abs = %q", abs)
	}
	got, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read created note: %v", err)
	}
	if string(got) != "# Launch Plan\n\n" {
		t.Errorf("body = %q, want %q", string(got), "# Launch Plan\n\n")
	}

	// Second create must refuse overwrite via O_EXCL.
	if _, _, err := v.Create("docs/specs/plan.md", "Other"); !errors.Is(err, os.ErrExist) {
		t.Errorf("second Create err = %v, want os.ErrExist", err)
	}
}

func TestVaultCreate_RejectsTraversalAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	v := Open(root)

	if _, _, err := v.Create("../escape.md", "bad"); !errors.Is(err, ErrOutsideVault) {
		t.Errorf("traversal err = %v, want ErrOutsideVault", err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	if err := os.Symlink(outside, filepath.Join(root, "linkdir")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := v.Create("linkdir/pwn.md", "bad"); err == nil {
		t.Fatal("Create through symlinked directory succeeded; want error")
	}
	if _, err := os.Stat(filepath.Join(outside, "pwn.md")); !os.IsNotExist(err) {
		t.Fatal("symlink escape wrote file outside vault")
	}
	if _, _, err := v.Create("   ", ""); err == nil || !strings.Contains(err.Error(), "required") {
		t.Errorf("empty filename err = %v, want required", err)
	}
}
