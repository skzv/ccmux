package configfile

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestWriteAtomic_RoundTrip — round-trips data through WriteAtomic and
// the file ends up with exactly the bytes we wrote.
func TestWriteAtomic_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "settings.json")
	want := []byte(`{"model":"opus"}`)
	if err := WriteAtomic(dst, want, 0o644); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("got %q, want %q", got, want)
	}
	// No temp file should linger.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".configfile-") {
			t.Errorf("temp file %q leaked after successful write", e.Name())
		}
	}
}

// TestWriteAtomic_OverwritesExisting — replacing existing content is
// the common case (every Set* in the agent-config packages).
func TestWriteAtomic_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(dst, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(dst, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != "new" {
		t.Errorf("after overwrite got %q, want %q", got, "new")
	}
}

// TestBackup_CreatesTimestampedCopy — the source file is copied to a
// timestamped sibling under backupDir.
func TestBackup_CreatesTimestampedCopy(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "settings.json")
	backups := filepath.Join(dir, "backups")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Backup(src, backups)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(got), "settings.json.") {
		t.Errorf("backup name = %q, want settings.json.<ts>", filepath.Base(got))
	}
	body, _ := os.ReadFile(got)
	if string(body) != "hello" {
		t.Errorf("backup body = %q, want %q", body, "hello")
	}
}

// TestBackup_SyncFailurePropagates — the backup is the rollback of
// last resort, so a failed fsync must surface to the caller instead of
// being silently swallowed (a crash after an unflushed copy could
// leave BOTH the original and the "backup" truncated).
func TestBackup_SyncFailurePropagates(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := syncBackup
	syncBackup = func(*os.File) error { return fmt.Errorf("disk full") }
	t.Cleanup(func() { syncBackup = orig })

	if _, err := Backup(src, filepath.Join(dir, "backups")); err == nil {
		t.Fatal("Backup must propagate a failed fsync, got nil")
	}
}

// TestBackup_NoopOnMissingSource — first write has nothing to back up.
func TestBackup_NoopOnMissingSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "not-yet.json")
	got, err := Backup(src, filepath.Join(dir, "backups"))
	if err != nil {
		t.Errorf("Backup on missing src returned %v, want nil", err)
	}
	if got != "" {
		t.Errorf("Backup on missing src returned path %q, want empty", got)
	}
}

// TestBackup_RotatesBeyondCap — pre-seed cap+5 backups, then ask for
// one more; rotation prunes the oldest down to the cap.
func TestBackup_RotatesBeyondCap(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "settings.json")
	backups := filepath.Join(dir, "backups")
	if err := os.WriteFile(src, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxBackupsPerFile+5; i++ {
		name := fmt.Sprintf("settings.json.%06d", i)
		if err := os.WriteFile(filepath.Join(backups, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Backup(src, backups); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	entries, _ := os.ReadDir(backups)
	matches := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "settings.json.") {
			matches++
		}
	}
	if matches != MaxBackupsPerFile {
		t.Errorf("after rotation: %d backup files, want %d", matches, MaxBackupsPerFile)
	}
}

// TestWriteAtomic_NeverLoosensMode — a settings file the user locked to
// 0600 (it can carry API keys) must not come back 0644 because the
// caller passed 0644; a caller passing 0600 still tightens a 0644 file.
func TestWriteAtomic_NeverLoosensMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits")
	}
	dir := t.TempDir()
	for _, tc := range []struct {
		existing, requested, want os.FileMode
	}{
		{0o600, 0o644, 0o600},
		{0o644, 0o600, 0o600},
		{0o644, 0o644, 0o644},
	} {
		p := filepath.Join(dir, "settings.json")
		_ = os.Remove(p)
		if err := os.WriteFile(p, []byte("{}"), tc.existing); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, tc.existing); err != nil {
			t.Fatal(err)
		}
		if err := WriteAtomic(p, []byte(`{"a":1}`), tc.requested); err != nil {
			t.Fatal(err)
		}
		info, _ := os.Stat(p)
		if got := info.Mode().Perm(); got != tc.want {
			t.Errorf("existing %o, requested %o: mode = %o, want %o", tc.existing, tc.requested, got, tc.want)
		}
	}
}

// TestWriteAtomic_KeepsSymlink — a dotfiles-managed settings file is a
// symlink; writing must update the target and leave the link intact.
func TestWriteAtomic_KeepsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles", "settings.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "settings.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(link, []byte(`{"new":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink replaced by a regular file (err=%v)", err)
	}
	if got, _ := os.ReadFile(target); string(got) != `{"new":true}` {
		t.Errorf("target not updated: %q", got)
	}
}

// TestBackup_IsOwnerOnly — backups copy files that can hold secrets.
func TestBackup_IsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(src, []byte(`{"env":{"ANTHROPIC_API_KEY":"x"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	dst, err := Backup(src, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(dst)
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("backup mode = %o, want 600", got)
	}
}
