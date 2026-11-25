package configfile

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteAtomic_PreservesDanglingSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	dir := t.TempDir()
	dotfiles := filepath.Join(dir, "dotfiles")
	if err := os.MkdirAll(dotfiles, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dotfiles, "config.toml")
	link := filepath.Join(dir, "config.toml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(link, []byte("hello = true\n"), 0o600); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("dangling symlink was replaced with a regular file")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("target file not created: %v", err)
	}
	if string(got) != "hello = true\n" {
		t.Errorf("target content = %q, want %q", got, "hello = true\n")
	}
}

func TestBackup_RemovesPartialFileOnSyncError(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "settings.json")
	backups := filepath.Join(dir, "backups")
	if err := os.WriteFile(src, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	orig := syncBackup
	syncBackup = func(*os.File) error { return fmt.Errorf("disk full") }
	t.Cleanup(func() { syncBackup = orig })

	dst, err := Backup(src, backups)
	if err == nil {
		t.Fatal("expected error from syncBackup")
	}
	if dst != "" {
		t.Errorf("failed Backup returned non-empty path %q", dst)
	}
	entries, _ := os.ReadDir(backups)
	if len(entries) != 0 {
		t.Errorf("partial backup file left behind: %v", entries)
	}
}

func TestPruneBackups_DoesNotMatchUnrelatedExtension(t *testing.T) {
	dir := t.TempDir()
	// Unrelated file sharing prefix "config." must never be pruned when
	// rotating backups for base "config".
	unrelated := filepath.Join(dir, "config.toml.20260101-000000.000")
	if err := os.WriteFile(unrelated, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("config.20260101-00000%d.000", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pruneBackups(dir, "config", 2)
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("unrelated file %q was pruned: %v", unrelated, err)
	}
	entries, _ := os.ReadDir(dir)
	var kept int
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "config.2026") {
			kept++
		}
	}
	if kept != 2 {
		t.Errorf("kept %d backups, want 2", kept)
	}
}
