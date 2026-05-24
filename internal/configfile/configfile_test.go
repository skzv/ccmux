package configfile

import (
	"fmt"
	"os"
	"path/filepath"
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
