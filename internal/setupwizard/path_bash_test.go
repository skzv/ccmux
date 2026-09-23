package setupwizard

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// asDarwin makes the rc-file choice behave as on macOS for this test.
func asDarwin(t *testing.T) {
	t.Helper()
	orig := hostGOOS
	hostGOOS = "darwin"
	t.Cleanup(func() { hostGOOS = orig })
}

// TestEnsureCcmuxOnPath_MacBashKeepsProfile — regression: on macOS with
// bash the wizard created ~/.bash_profile. bash login shells read only
// the first of ~/.bash_profile, ~/.bash_login, ~/.profile that exists,
// so a user whose setup lived in ~/.profile silently lost all of it.
// With only ~/.profile present the block must go into ~/.profile, the
// file's mode must be kept, and re-running must not add a second block.
func TestEnsureCcmuxOnPath_MacBashKeepsProfile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes")
	}
	asDarwin(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/bash")
	t.Setenv("PATH", "/usr/bin:/bin")
	profile := filepath.Join(home, ".profile")
	const prior = "export EDITOR=vim\n. \"$HOME/.cargo/env\"\n"
	if err := os.WriteFile(profile, []byte(prior), 0o600); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		var out bytes.Buffer
		if err := ensureCcmuxOnPath(&out); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}

	if _, err := os.Stat(filepath.Join(home, ".bash_profile")); !os.IsNotExist(err) {
		t.Fatalf("~/.bash_profile was created (err=%v) — bash login shells would stop reading ~/.profile", err)
	}
	body, err := os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(body), prior) {
		t.Errorf("existing ~/.profile content not preserved:\n%s", body)
	}
	if n := strings.Count(string(body), rcGuardOpen); n != 1 {
		t.Errorf("~/.profile has %d managed blocks, want 1:\n%s", n, body)
	}
	info, err := os.Stat(profile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("~/.profile mode = %v, want 0600 preserved", info.Mode().Perm())
	}
}

// TestBashLoginRC — the rc chosen for a macOS bash login shell follows
// bash's own lookup order, creating ~/.bash_profile only as a last
// resort.
func TestBashLoginRC(t *testing.T) {
	cases := []struct {
		existing []string
		want     string
	}{
		{nil, ".bash_profile"},
		{[]string{".profile"}, ".profile"},
		{[]string{".bash_login", ".profile"}, ".bash_login"},
		{[]string{".bash_profile", ".profile"}, ".bash_profile"},
	}
	for _, tc := range cases {
		home := t.TempDir()
		for _, n := range tc.existing {
			if err := os.WriteFile(filepath.Join(home, n), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if got := bashLoginRC(home); got != filepath.Join(home, tc.want) {
			t.Errorf("existing %v: rc = %s, want %s", tc.existing, filepath.Base(got), tc.want)
		}
	}
}
