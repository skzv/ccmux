package selfupdate

import (
	"os"
	"path/filepath"
	"testing"
)

func mkCheckout(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("all:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestIsMakeInstallBinary: only ~/.local/bin/ccmux (what `make install`
// produces) counts; Homebrew and other locations do not.
func TestIsMakeInstallBinary(t *testing.T) {
	const home = "/home/u"
	cases := []struct {
		exe  string
		want bool
	}{
		{"/home/u/.local/bin/ccmux", true},
		{"/opt/homebrew/bin/ccmux", false},
		{"/usr/local/bin/ccmux", false},
		{"/home/u/Projects/ccmux/bin/ccmux", false},
		{"/home/u/bin/ccmux", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isMakeInstallBinary(c.exe, home); got != c.want {
			t.Errorf("isMakeInstallBinary(%q) = %v, want %v", c.exe, got, c.want)
		}
	}
}

// TestRepoRootFrom: a binary inside a checkout resolves it; a make
// install binary falls back to ~/Projects/ccmux; a packaged binary
// (Homebrew / tarball) outside both NEVER borrows the ~/Projects/ccmux
// clone — regression for a brew install reporting "N commits behind
// main" against a stale leftover checkout.
func TestRepoRootFrom(t *testing.T) {
	home := t.TempDir()
	checkout := filepath.Join(home, "Projects", "ccmux")
	mkCheckout(t, checkout)

	t.Run("binary inside a checkout (source/dev)", func(t *testing.T) {
		repo := t.TempDir()
		mkCheckout(t, repo)
		exe := filepath.Join(repo, "bin", "ccmux") // need not exist on disk
		got, err := repoRootFrom(exe, home)
		if err != nil || got != repo {
			t.Fatalf("got (%q, %v), want (%q, nil)", got, err, repo)
		}
	})

	t.Run("make install binary -> ~/Projects/ccmux fallback", func(t *testing.T) {
		exe := filepath.Join(home, ".local", "bin", "ccmux")
		got, err := repoRootFrom(exe, home)
		if err != nil || got != checkout {
			t.Fatalf("got (%q, %v), want (%q, nil)", got, err, checkout)
		}
	})

	t.Run("packaged binary outside checkout & ~/.local/bin -> no fallback", func(t *testing.T) {
		pkg := t.TempDir() // stands in for /opt/homebrew/bin etc.
		exe := filepath.Join(pkg, "bin", "ccmux")
		if got, err := repoRootFrom(exe, home); err == nil {
			t.Fatalf("got (%q, nil), want error (packaged install must not borrow ~/Projects/ccmux)", got)
		}
	})

	t.Run("empty exe -> error", func(t *testing.T) {
		if _, err := repoRootFrom("", home); err == nil {
			t.Fatal("empty exe should error")
		}
	})
}
