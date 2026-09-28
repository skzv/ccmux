package tui

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveProjectsOverride_ExpandsTilde — `ccmux --projects=~/work`
// (or a quoted "~/work", which the shell leaves alone) resolved the
// root with filepath.Abs, so the TUI looked for a directory literally
// named "~" under the working directory and refused to start, while
// the CLI subcommands (project.ResolveRoot) accepted the same flag.
func TestResolveProjectsOverride_ExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	work := filepath.Join(home, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := resolveProjectsOverride("~/work")
	if err != nil {
		t.Fatalf("~/work: %v", err)
	}
	if got != work {
		t.Errorf("~/work resolved to %q, want %q", got, work)
	}

	if _, err := resolveProjectsOverride(filepath.Join(home, "missing")); err == nil {
		t.Error("a missing directory was accepted")
	}
	file := filepath.Join(home, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveProjectsOverride(file); err == nil {
		t.Error("a regular file was accepted as the projects root")
	}
}
