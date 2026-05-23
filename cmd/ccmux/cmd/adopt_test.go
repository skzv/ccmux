package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveAdoptTarget_BareName uses HOME-based projects root so the
// "name only" form resolves to ~/Projects/<name> (config.Load returns
// defaults when no config.toml is present).
func TestResolveAdoptTarget_BareName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	got, err := resolveAdoptTarget("qc")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "Projects", "qc")
	if got != want {
		t.Errorf("bare name: got %q, want %q", got, want)
	}
}

func TestResolveAdoptTarget_AbsolutePath(t *testing.T) {
	abs := t.TempDir()
	got, err := resolveAdoptTarget(abs)
	if err != nil {
		t.Fatal(err)
	}
	if got != abs {
		t.Errorf("abs: got %q, want %q", got, abs)
	}
}

func TestResolveAdoptTarget_RelativePath(t *testing.T) {
	// Anything containing a separator or leading dot is treated as a
	// path, not a bare name. Use "./x" so the relative-marker branch
	// fires regardless of the test's CWD.
	got, err := resolveAdoptTarget("./relative-dir")
	if err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	want := filepath.Join(cwd, "relative-dir")
	if got != want {
		t.Errorf("relative: got %q, want %q", got, want)
	}
}

func TestResolveAdoptTarget_TildeExpansion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := resolveAdoptTarget("~/work/legacy")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "work", "legacy")
	if got != want {
		t.Errorf("tilde: got %q, want %q", got, want)
	}
}

func TestResolveAdoptTarget_EmptyErrors(t *testing.T) {
	if _, err := resolveAdoptTarget("   "); err == nil {
		t.Error("blank target should error")
	}
}

// TestRunAdoptCmd_WritesMarker is the end-to-end "the CLI actually
// adopts" check. Verifies that runAdoptCmd creates the `.ccmux/`
// marker so a subsequent project.Discover would surface the directory.
func TestRunAdoptCmd_WritesMarker(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	dir := filepath.Join(home, "Projects", "scratch")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runAdoptCmd("scratch"); err != nil {
		t.Fatalf("runAdoptCmd: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(dir, ".ccmux")); err != nil || !fi.IsDir() {
		t.Errorf(".ccmux/ marker not created: err=%v", err)
	}
}

func TestRunAdoptCmd_MissingDirErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if err := runAdoptCmd("definitely-not-here"); err == nil {
		t.Error("expected error for missing dir, got nil")
	}
}
