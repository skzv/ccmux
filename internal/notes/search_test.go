package notes

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// makeSearchVault writes a small fixture: a Specs file, an Architecture
// file, an Agent Log, all with overlapping search terms. Returns the
// Vault rooted in the project's docs/.
func makeSearchVault(t *testing.T) Vault {
	t.Helper()
	root := t.TempDir()
	v := Open(root)
	files := map[string]string{
		"01_Specs/00_Auth.md":          "# Auth flow\n\nWe rebuild login with passkeys.\nNotes about passkeys go here.\n",
		"02_Architecture/00_System.md": "# System design\n\nccmuxd is the daemon.\nPasskeys are stored in the keychain.\n",
		"03_Agent_Logs/2026-05-11.md":  "# Log\n\nFigured out the passkey flow today.\n",
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

// TestHitFor_RelUsesSlashes — SearchHit.Rel must follow Entry.Rel's
// slash-separated convention on every OS (List already normalizes via
// filepath.ToSlash; hitFor must too or fallback hits diverge on
// Windows).
func TestHitFor_RelUsesSlashes(t *testing.T) {
	root := filepath.Join("a", "b")
	abs := filepath.Join(root, "docs", "note.md")
	h := hitFor(root, abs, 3, "  snippet text\n")
	if h.Rel != "docs/note.md" {
		t.Errorf("Rel = %q, want %q", h.Rel, "docs/note.md")
	}
	if h.Path != abs || h.LineNum != 3 {
		t.Errorf("hit fields wrong: %+v", h)
	}
	if h.Snippet != "snippet text" {
		t.Errorf("Snippet = %q, want trimmed", h.Snippet)
	}
}

func TestSearch_BackendQueryBehavior(t *testing.T) {
	for _, backend := range []string{"fallback", "ripgrep"} {
		t.Run(backend, func(t *testing.T) {
			v := Vault{Root: t.TempDir()}
			body := "passkey\naXb\na.b\n[brackets]\n" + strings.Repeat("repeated\n", 8)
			if err := os.WriteFile(filepath.Join(v.Root, "note.md"), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			search := v.searchFallback
			if backend == "ripgrep" {
				if _, err := exec.LookPath("rg"); err != nil {
					t.Skip("ripgrep unavailable")
				}
				search = v.searchRipgrep
			}
			for _, tc := range []struct {
				query       string
				limit, want int
			}{
				{"PASSKEY", 100, 1}, {"[", 100, 1}, {"a.b", 100, 1},
				{"missing", 100, 0}, {"repeated", 100, 5}, {"repeated", 2, 2},
			} {
				hits, err := search(context.Background(), tc.query, tc.limit)
				if err != nil || len(hits) != tc.want {
					t.Errorf("query=%q limit=%d: got %d hits, %v; want %d", tc.query, tc.limit, len(hits), err, tc.want)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := search(ctx, "passkey", 100); !errors.Is(err, context.Canceled) {
				t.Errorf("canceled search: %v", err)
			}
		})
	}
}

func TestSearch_OversizedLineReturnsError(t *testing.T) {
	for _, backend := range []string{"fallback", "ripgrep"} {
		t.Run(backend, func(t *testing.T) {
			v := Vault{Root: t.TempDir()}
			body := "match\n" + strings.Repeat("x", 1<<22) + "match\n"
			if err := os.WriteFile(filepath.Join(v.Root, "note.md"), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			search := v.searchFallback
			if backend == "ripgrep" {
				if _, err := exec.LookPath("rg"); err != nil {
					t.Skip("ripgrep unavailable")
				}
				search = v.searchRipgrep
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if hits, err := search(ctx, "match", 100); err == nil || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expected scanner error, got hits=%d err=%v", len(hits), err)
			}
		})
	}
}

func TestSearch_FallbackStopsBeforeUnreadResults(t *testing.T) {
	v := Vault{Root: t.TempDir()}
	// The oversized line must not be read after either limit is satisfied.
	for _, tc := range []struct{ matches, limit int }{{1, 1}, {5, 100}} {
		body := strings.Repeat("match\n", tc.matches) + strings.Repeat("x", 1<<22)
		if err := os.WriteFile(filepath.Join(v.Root, "note.md"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		hits, err := v.searchFallback(context.Background(), "match", tc.limit)
		if err != nil || len(hits) != tc.matches {
			t.Fatalf("limit=%d: hits=%d err=%v", tc.limit, len(hits), err)
		}
	}
}
