package notes

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeSearchVault writes a small fixture: a Specs file, an Architecture
// file, an Agent Log, all with overlapping search terms. Returns the
// Vault rooted in the project's docs/.
func makeSearchVault(t *testing.T) Vault {
	t.Helper()
	root := t.TempDir()
	v := Open(root)
	files := map[string]string{
		"01_Specs/00_Auth.md": "# Auth flow\n\nWe rebuild login with passkeys.\nNotes about passkeys go here.\n",
		"02_Architecture/00_System.md": "# System design\n\nccmuxd is the daemon.\nPasskeys are stored in the keychain.\n",
		"03_Agent_Logs/2026-05-11.md": "# Log\n\nFigured out the passkey flow today.\n",
	}
	for rel, body := range files {
		full := filepath.Join(v.Root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return v
}

func TestSearch_FallbackFindsAllMatches(t *testing.T) {
	// Force the fallback path by overriding $PATH so `rg` can't be
	// found. The Vault.Search code falls back to the Go scanner.
	t.Setenv("PATH", "/var/empty")

	v := makeSearchVault(t)
	hits, err := v.Search(context.Background(), "passkey", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) < 3 {
		t.Fatalf("expected at least 3 hits across 3 files, got %d: %+v", len(hits), hits)
	}

	// Every hit's Rel should be a docs/-relative path, never absolute.
	for _, h := range hits {
		if filepath.IsAbs(h.Rel) {
			t.Errorf("Rel is absolute: %s", h.Rel)
		}
		if h.LineNum <= 0 {
			t.Errorf("LineNum %d <= 0", h.LineNum)
		}
		if h.Snippet == "" {
			t.Errorf("empty snippet for %s:%d", h.Rel, h.LineNum)
		}
	}
}

func TestSearch_CaseInsensitiveFallback(t *testing.T) {
	t.Setenv("PATH", "/var/empty")
	v := makeSearchVault(t)
	hits, err := v.Search(context.Background(), "PASSKEY", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("uppercase query should match the lowercase words via case-insensitive search")
	}
}

func TestSearch_EmptyQueryReturnsNothing(t *testing.T) {
	v := makeSearchVault(t)
	got, err := v.Search(context.Background(), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("empty query should return no hits, got %v", got)
	}
	if got, _ := v.Search(context.Background(), "   ", 100); len(got) != 0 {
		t.Fatalf("whitespace-only query should return no hits, got %v", got)
	}
}

func TestSearch_MissingVaultDirReturnsNothing(t *testing.T) {
	v := Vault{Root: filepath.Join(t.TempDir(), "does-not-exist")}
	got, err := v.Search(context.Background(), "anything", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("missing vault should return empty, got %v", got)
	}
}

func TestSearch_LimitHonored(t *testing.T) {
	t.Setenv("PATH", "/var/empty")
	v := makeSearchVault(t)
	got, err := v.Search(context.Background(), "passkey", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > 2 {
		t.Fatalf("limit=2 should cap results, got %d", len(got))
	}
}

func TestSearch_RespectsHiddenDirs(t *testing.T) {
	t.Setenv("PATH", "/var/empty")
	root := t.TempDir()
	v := Open(root)
	// Hidden dir under the vault: should be skipped.
	hidden := filepath.Join(v.Root, ".obsidian", "config.md")
	if err := os.MkdirAll(filepath.Dir(hidden), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hidden, []byte("# secret thing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A visible file with the same term.
	visible := filepath.Join(v.Root, "01_Specs", "spec.md")
	if err := os.MkdirAll(filepath.Dir(visible), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(visible, []byte("# secret thing\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	hits, err := v.Search(context.Background(), "secret", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if strings.Contains(h.Rel, ".obsidian") {
			t.Errorf("hit from hidden dir leaked: %s", h.Rel)
		}
	}
	if len(hits) == 0 {
		t.Error("visible spec.md hit missing")
	}
}

// TestSearch_PrefersRipgrep_WhenAvailable runs Search in the host's
// real $PATH so rg is picked if installed. We can't assume rg exists
// in CI, so this only asserts that hits come back — proving the rg
// code path doesn't crash. If rg isn't installed, the fallback path
// is exercised instead (same expected behavior).
func TestSearch_PrefersRipgrepWhenAvailable(t *testing.T) {
	v := makeSearchVault(t)
	got, err := v.Search(context.Background(), "passkey", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Error("expected hits regardless of which backend ran")
	}
}
