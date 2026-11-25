package notes

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// symlinkedVault builds <tmp>/elsewhere holding the real notes and a
// project entry <tmp>/Projects/linked -> elsewhere, the shape of a
// project symlinked into ~/Projects. Inside the target there is also a
// symlink to a directory outside the project, which must stay unwalked.
func symlinkedVault(t *testing.T) (Vault, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	base := t.TempDir()
	target := filepath.Join(base, "elsewhere")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{filepath.Join(target, "docs", "specs"), outside, filepath.Join(base, "Projects")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, body string) {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(target, "README.md"), "# Linked\n\nneedle in the root\n")
	write(filepath.Join(target, "docs", "specs", "one.md"), "# Spec\n\nneedle in a spec\n")
	write(filepath.Join(outside, "secret.md"), "needle outside the project\n")
	if err := os.Symlink(outside, filepath.Join(target, "escape")); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "Projects", "linked")
	if err := os.Symlink(target, root); err != nil {
		t.Fatal(err)
	}
	return Open(root), root
}

// TestList_FollowsSymlinkedRoot — filepath.WalkDir doesn't follow a
// symlinked root, so a project symlinked into ~/Projects showed no notes
// at all. The listing must see the target's files, keep Rel relative to
// the vault and Path under the project path the user knows, and still
// not follow symlinks inside the tree.
func TestList_FollowsSymlinkedRoot(t *testing.T) {
	v, root := symlinkedVault(t)
	entries, err := v.List()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range entries {
		got[e.Rel] = e.Path
	}
	want := map[string]string{
		"README.md":         filepath.Join(root, "README.md"),
		"docs/specs/one.md": filepath.Join(root, "docs", "specs", "one.md"),
	}
	if len(got) != len(want) {
		t.Fatalf("List = %v, want exactly %v", got, want)
	}
	for rel, path := range want {
		if got[rel] != path {
			t.Errorf("entry %q path = %q, want %q", rel, got[rel], path)
		}
	}
	// Read goes through the same vault-relative path.
	if body, err := v.Read("docs/specs/one.md"); err != nil || len(body) == 0 {
		t.Errorf("Read(docs/specs/one.md) = %q, %v", body, err)
	}
	if _, err := v.Read("../outside/secret.md"); err != ErrOutsideVault {
		t.Errorf("Read escaped the vault: err = %v", err)
	}
	// Directory symlink escape inside the vault must also return ErrOutsideVault.
	if _, err := v.Read("escape/secret.md"); err != ErrOutsideVault {
		t.Errorf("Read(escape/secret.md) via dir symlink = %v, want ErrOutsideVault", err)
	}
}

// TestSymlinkedFileInsideVault_BlockedAcrossListReadSearch verifies that
// a file symlink inside a project pointing outside the vault is skipped by
// List and Search, and rejected with ErrOutsideVault by Read.
func TestSymlinkedFileInsideVault_BlockedAcrossListReadSearch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	base := t.TempDir()
	proj := filepath.Join(base, "proj")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{proj, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(proj, "NOTES.MD"), []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(outside, "secret.toml")
	if err := os.WriteFile(secret, []byte("topsecret needle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(proj, "leak.md")); err != nil {
		t.Fatal(err)
	}

	v := Open(proj)
	entries, err := v.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Rel != "NOTES.MD" {
		t.Fatalf("List = %+v, want only NOTES.MD (leak.md symlink must be skipped)", entries)
	}
	if entries[0].Display != "NOTES" {
		t.Errorf("Display for NOTES.MD = %q, want %q (case-insensitive .md trim)", entries[0].Display, "NOTES")
	}
	if _, err := v.Read("leak.md"); err != ErrOutsideVault {
		t.Errorf("Read(leak.md) = %v, want ErrOutsideVault", err)
	}
	hits, err := v.searchFallback(context.Background(), "topsecret", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("searchFallback leaked symlinked file: %+v", hits)
	}
}

// TestSearch_FollowsSymlinkedRoot — both search backends must search a
// symlinked project like List lists it, and report the same Rel/Path.
func TestSearch_FollowsSymlinkedRoot(t *testing.T) {
	v, root := symlinkedVault(t)
	backends := map[string]func(context.Context, string, int) ([]SearchHit, error){
		"fallback": v.searchFallback,
	}
	if _, err := exec.LookPath("rg"); err == nil {
		backends["ripgrep"] = v.searchRipgrep
	}
	for name, search := range backends {
		t.Run(name, func(t *testing.T) {
			hits, err := search(context.Background(), "needle", 100)
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]string{}
			for _, h := range hits {
				got[h.Rel] = h.Path
			}
			if len(got) != 2 {
				t.Fatalf("hits = %v, want README.md and docs/specs/one.md only", got)
			}
			if got["docs/specs/one.md"] != filepath.Join(root, "docs", "specs", "one.md") {
				t.Errorf("hit path = %q, want it under the project path %q", got["docs/specs/one.md"], root)
			}
		})
	}
}
