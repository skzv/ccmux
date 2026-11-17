package claudeconfig

import (
	"path/filepath"
	"testing"
)

// TestPaths_ExpandsTildeInConfigDir — Paths took $CLAUDE_CONFIG_DIR
// verbatim, so a value that reached ccmux with its ~ unexpanded (a
// quoted shell export, a service definition) became a relative
// directory literally named "~". It now resolves through
// internal/agenthome like the transcript readers, so the config tab
// and the conversation list agree on one directory.
func TestPaths_ExpandsTildeInConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "~/.claude-work/")
	got, err := Paths()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".claude-work")
	if got.Root != want || got.Settings != filepath.Join(want, "settings.json") {
		t.Errorf("Paths = %+v, want root %s", got, want)
	}
}
