package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeRepo lays out a directory that resolveRepo will accept: a .git
// dir and a Makefile. Returns its absolute path.
func fakeRepo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("build:\n\techo fake\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestLooksLikeCcmuxRepo(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, dir string)
		want  bool
	}{
		{
			"both .git and Makefile",
			func(t *testing.T, d string) { fakeRepo(t, d) },
			true,
		},
		{
			"only .git, no Makefile",
			func(t *testing.T, d string) { _ = os.MkdirAll(filepath.Join(d, ".git"), 0o755) },
			false,
		},
		{
			"only Makefile, no .git",
			func(t *testing.T, d string) {
				_ = os.WriteFile(filepath.Join(d, "Makefile"), []byte("x"), 0o644)
			},
			false,
		},
		{
			"empty dir",
			func(t *testing.T, d string) {},
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(t, dir)
			if got := looksLikeCcmuxRepo(dir); got != tc.want {
				t.Fatalf("looksLikeCcmuxRepo(%s) = %v, want %v", dir, got, tc.want)
			}
		})
	}
}

func TestFindGitRoot_WalksUpAncestors(t *testing.T) {
	// Repo at <tmp>/repo, then a deeply nested binary path.
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	_ = os.MkdirAll(repo, 0o755)
	fakeRepo(t, repo)

	deep := filepath.Join(repo, "bin", "subdir", "more")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	if got := findGitRoot(deep); got != repo {
		t.Fatalf("findGitRoot(%s) = %q, want %q", deep, got, repo)
	}
}

func TestFindGitRoot_StopsAtFilesystemRoot(t *testing.T) {
	// Pick a path that almost certainly has no ccmux repo above it.
	if got := findGitRoot("/var/empty"); got != "" {
		t.Fatalf("expected no repo above /var/empty, got %q", got)
	}
}

func TestFindGitRoot_PrefersInnermostRepo(t *testing.T) {
	tmp := t.TempDir()
	outer := filepath.Join(tmp, "outer")
	inner := filepath.Join(outer, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeRepo(t, outer)
	fakeRepo(t, inner)

	if got := findGitRoot(inner); got != inner {
		t.Fatalf("findGitRoot from inner = %q, want %q", got, inner)
	}
	// Starting from outer still returns outer.
	if got := findGitRoot(outer); got != outer {
		t.Fatalf("findGitRoot from outer = %q, want %q", got, outer)
	}
}

func TestValidateRepo_ExplicitGood(t *testing.T) {
	dir := t.TempDir()
	want := fakeRepo(t, dir)
	got, err := validateRepo(dir)
	if err != nil {
		t.Fatalf("validateRepo(%s): %v", dir, err)
	}
	if got != want {
		t.Fatalf("validateRepo returned %q, want %q", got, want)
	}
}

func TestValidateRepo_RejectsNonRepo(t *testing.T) {
	if _, err := validateRepo(t.TempDir()); err == nil {
		t.Fatal("validateRepo should reject empty dir, got nil")
	}
}

func TestResolveRepo_ExplicitWins(t *testing.T) {
	dir := t.TempDir()
	want := fakeRepo(t, dir)
	got, err := resolveRepo(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("resolveRepo(explicit=%s) = %q, want %q", dir, got, want)
	}
}

func TestResolveRepo_ExplicitRejectsBadPath(t *testing.T) {
	if _, err := resolveRepo(t.TempDir()); err == nil {
		t.Fatal("resolveRepo should reject empty explicit dir")
	}
}

// TestResolveRepo_AutoDetectFromBinaryAncestor is exercised indirectly
// here: we can't easily move os.Executable() inside a test, but we can
// at least confirm the function returns *some* path when invoked from
// the project tree (which has both .git and a Makefile). If this test
// ever fails inside this repo, something is wrong with auto-detection.
func TestResolveRepo_AutoDetectInThisRepo(t *testing.T) {
	got, err := resolveRepo("")
	if err != nil {
		// Acceptable: when running tests outside the project tree (rare),
		// the fallback ~/Projects/ccmux may not exist either.
		t.Skipf("resolveRepo found nothing — likely running outside the ccmux tree: %v", err)
	}
	if !looksLikeCcmuxRepo(got) {
		t.Fatalf("resolveRepo returned %q which doesn't look like a ccmux repo", got)
	}
}

func TestRunStep_DryRunDoesNothing(t *testing.T) {
	// `false` would normally exit 1; under --dry-run we should not call
	// it. If the implementation regresses, this test fails because
	// `false` returns an error.
	if err := runStep(t.TempDir(), true, "false"); err != nil {
		t.Fatalf("dry-run still executed the command: %v", err)
	}
}

func TestRunStep_RunsAndReportsExit(t *testing.T) {
	// `true` exits 0.
	if err := runStep(t.TempDir(), false, "true"); err != nil {
		t.Fatalf("runStep(true): unexpected error %v", err)
	}
	// `false` exits 1 — runStep should surface that.
	if err := runStep(t.TempDir(), false, "false"); err == nil {
		t.Fatal("runStep(false): expected error, got nil")
	}
}
