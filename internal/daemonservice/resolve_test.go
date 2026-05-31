package daemonservice

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestResolveCcmuxdFrom pins the ccmuxd-location logic across install
// layouts: a sibling next to ccmux (Homebrew /opt/homebrew/bin, make
// install ~/.local/bin), a PATH fallback, and the legacy default when
// nothing is found. Regression for a Homebrew install where Probe
// hardcoded ~/.local/bin/ccmuxd and reported "not installed."
func TestResolveCcmuxdFrom(t *testing.T) {
	const home = "/home/u"
	exists := func(set ...string) func(string) bool {
		m := map[string]bool{}
		for _, s := range set {
			m[s] = true
		}
		return func(p string) bool { return m[p] }
	}
	notOnPath := func(string) (string, error) { return "", errors.New("not found") }

	tests := []struct {
		name      string
		exe       string
		exists    func(string) bool
		lookPath  func(string) (string, error)
		wantPath  string
		wantFound bool
	}{
		{
			name:      "sibling next to ccmux (Homebrew)",
			exe:       "/opt/homebrew/bin/ccmux",
			exists:    exists("/opt/homebrew/bin/ccmuxd"),
			lookPath:  notOnPath,
			wantPath:  "/opt/homebrew/bin/ccmuxd",
			wantFound: true,
		},
		{
			name:      "sibling next to ccmux (make install)",
			exe:       "/home/u/.local/bin/ccmux",
			exists:    exists("/home/u/.local/bin/ccmuxd"),
			lookPath:  notOnPath,
			wantPath:  "/home/u/.local/bin/ccmuxd",
			wantFound: true,
		},
		{
			name:      "no sibling -> PATH",
			exe:       "/weird/place/ccmux",
			exists:    exists(),
			lookPath:  func(string) (string, error) { return "/usr/local/bin/ccmuxd", nil },
			wantPath:  "/usr/local/bin/ccmuxd",
			wantFound: true,
		},
		{
			name:      "nowhere -> legacy default, not installed",
			exe:       "/weird/place/ccmux",
			exists:    exists(),
			lookPath:  notOnPath,
			wantPath:  filepath.Join(home, ".local", "bin", "ccmuxd"),
			wantFound: false,
		},
		{
			name:      "empty exe -> PATH then default",
			exe:       "",
			exists:    exists(),
			lookPath:  notOnPath,
			wantPath:  filepath.Join(home, ".local", "bin", "ccmuxd"),
			wantFound: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, found := resolveCcmuxdFrom(tc.exe, home, tc.exists, tc.lookPath)
			if got != tc.wantPath || found != tc.wantFound {
				t.Errorf("resolveCcmuxdFrom(%q) = (%q, %v), want (%q, %v)",
					tc.exe, got, found, tc.wantPath, tc.wantFound)
			}
		})
	}
}

// TestResolveCcmuxd_BrewSymlinkLayout exercises the real Homebrew shape
// end to end with on-disk symlinks: the prefix bin (e.g. /opt/homebrew/bin)
// symlinks both ccmux and ccmuxd into the version-pinned Cellar. The
// resolved ccmuxd MUST be the stable prefix-bin symlink (which survives
// `brew upgrade`), not the Cellar path — so the launchd/systemd service
// keeps working after an upgrade.
func TestResolveCcmuxd_BrewSymlinkLayout(t *testing.T) {
	root := t.TempDir()
	prefixBin := filepath.Join(root, "opt", "homebrew", "bin")
	cellarBin := filepath.Join(root, "Cellar", "ccmux", "1.2.3", "bin")
	for _, d := range []string{prefixBin, cellarBin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"ccmux", "ccmuxd"} {
		real := filepath.Join(cellarBin, name)
		if err := os.WriteFile(real, []byte("#!/bin/true\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, filepath.Join(prefixBin, name)); err != nil {
			t.Fatal(err)
		}
	}

	// candidateBinDirs: invoked (stable) dir first, resolved Cellar second.
	// EvalSymlinks canonicalises the second entry (on macOS /tmp -> /private/tmp),
	// so compare against the resolved Cellar dir rather than the raw temp path.
	resolvedCellar, err := filepath.EvalSymlinks(cellarBin)
	if err != nil {
		t.Fatal(err)
	}
	dirs := candidateBinDirs(filepath.Join(prefixBin, "ccmux"))
	if len(dirs) != 2 || dirs[0] != prefixBin || dirs[1] != resolvedCellar {
		t.Fatalf("candidateBinDirs = %v, want [%q %q]", dirs, prefixBin, resolvedCellar)
	}

	// Full resolution via the real filesystem probe.
	notOnPath := func(string) (string, error) { return "", errors.New("not found") }
	got, found := resolveCcmuxdFrom(filepath.Join(prefixBin, "ccmux"), root, fileExists, notOnPath)
	want := filepath.Join(prefixBin, "ccmuxd")
	if !found || got != want {
		t.Fatalf("resolveCcmuxdFrom(brew symlink) = (%q, %v), want (%q, true) — must be the stable prefix path, not %q",
			got, found, want, filepath.Join(cellarBin, "ccmuxd"))
	}
}
