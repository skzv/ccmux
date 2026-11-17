package codexconfig

import (
	"path/filepath"
	"testing"
)

// TestPaths_ExpandsTildeInCodexHome — Paths, the transcript listing
// and the usage walker resolve $CODEX_HOME through the one resolver in
// internal/agenthome, so an unexpanded ~ means the same directory to
// all of them (it used to become a relative directory named "~").
func TestPaths_ExpandsTildeInCodexHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "~/codex-work")
	got, err := Paths()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "codex-work"); got.Root != want {
		t.Errorf("Root = %s, want %s", got.Root, want)
	}
}
