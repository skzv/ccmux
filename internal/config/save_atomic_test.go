package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestSave_FileMode0600 — config.toml carries secrets (API keys such as
// the OpenRouter key), so Save must create it owner-read/write only,
// never world-readable 0644.
func TestSave_FileMode0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits")
	}
	withFakeHome(t)
	if err := Save(Defaults()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	p, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("config.toml mode = %o, want 600", got)
	}
}

// TestSave_TightensExistingWorldReadableFile — a pre-existing 0644
// config.toml (written by an older ccmux) must come out of the next
// Save as 0600.
func TestSave_TightensExistingWorldReadableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits")
	}
	home := withFakeHome(t)
	p := filepath.Join(home, ".config", "ccmux", "config.toml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("theme = \"dracula\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Save(Defaults()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("config.toml mode after Save = %o, want 600", got)
	}
}

// TestSave_AtomicViaTempAndRename exercises the temp+rename seam. The
// old implementation opened the destination with os.Create — truncating
// it in place before a single byte was encoded, so any failure left an
// empty or partial file. The atomic path writes a sibling temp file and
// renames it over the destination:
//
//   - a read-only destination file is still replaceable (rename swaps
//     the directory entry; os.Create would have failed and, on other
//     failure shapes, truncated),
//   - no *.tmp litter remains after a successful Save,
//   - the destination parses cleanly after Save.
func TestSave_AtomicViaTempAndRename(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission semantics")
	}
	if os.Getuid() == 0 {
		t.Skip("root ignores file permission bits")
	}
	home := withFakeHome(t)
	p := filepath.Join(home, ".config", "ccmux", "config.toml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	// A read-only destination: in-place truncation (os.Create) fails on
	// it; an atomic rename replaces it without ever opening it.
	if err := os.WriteFile(p, []byte("theme = \"dracula\"\n"), 0o400); err != nil {
		t.Fatal(err)
	}

	in := Defaults()
	in.Theme = "nord"
	if err := Save(in); err != nil {
		t.Fatalf("Save over a read-only file must succeed via rename, got: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if got.Theme != "nord" {
		t.Errorf("Theme = %q, want nord (stale or truncated file?)", got.Theme)
	}

	entries, err := os.ReadDir(filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file left behind after Save: %s", e.Name())
		}
	}
}
