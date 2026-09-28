//go:build !windows

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeEditors puts stub editors on a sandbox PATH. Each records its
// own name and arguments in <dir>/ran, so a test can see which editor
// ran and how it was invoked.
func fakeEditors(t *testing.T, names ...string) (dir string, ran func() string) {
	t.Helper()
	dir = t.TempDir()
	out := filepath.Join(dir, "ran")
	for _, name := range names {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		// Shell builtins only: PATH holds nothing but the stubs.
		script := "#!/bin/sh\nprintf '%s' \"${0##*/}\" > '" + out + "'\nfor a in \"$@\"; do printf ' %s' \"$a\" >> '" + out + "'; done\n"
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	return dir, func() string {
		b, _ := os.ReadFile(out)
		_ = os.Remove(out)
		return string(b)
	}
}

// TestEditorProcess_RunsEditorCommandLines — the editor was exec'd as a
// bare binary name: EDITOR="code --wait" looked for a program literally
// called "code --wait" and failed, and a stale `editor = "nvim"` in
// config.toml failed on a machine without nvim. The command line is
// now split into words, and an editor that isn't on PATH falls back to
// pickEditor's choice.
func TestEditorProcess_RunsEditorCommandLines(t *testing.T) {
	dir, ran := fakeEditors(t, "code", "vi", "Sub Editor/subl")
	note := filepath.Join(t.TempDir(), "note.md")
	cases := []struct{ editor, want string }{
		{"code --wait", "code --wait " + note},
		{`"` + filepath.Join(dir, "Sub Editor", "subl") + `" -w`, "subl -w " + note},
		{"nvim", "vi " + note}, // not installed → pickEditor's fallback
		{"", "vi " + note},
	}
	for _, tc := range cases {
		if err := (&editorProcess{editor: tc.editor, path: note}).Run(); err != nil {
			t.Errorf("editor %q: %v", tc.editor, err)
			continue
		}
		if got := ran(); got != tc.want {
			t.Errorf("editor %q ran %q, want %q", tc.editor, got, tc.want)
		}
	}

	// $EDITOR with arguments, reached through the pickEditor fallback.
	t.Setenv("EDITOR", "code -n")
	if err := (&editorProcess{path: note}).Run(); err != nil {
		t.Fatalf("EDITOR=%q: %v", "code -n", err)
	}
	if got, want := ran(), "code -n "+note; got != want {
		t.Errorf("EDITOR=\"code -n\" ran %q, want %q", got, want)
	}
}

// TestSplitCommandLine pins the small shell-words split editorArgv
// relies on.
func TestSplitCommandLine(t *testing.T) {
	cases := map[string][]string{
		"nvim":             {"nvim"},
		"  code   --wait ": {"code", "--wait"},
		`"/Applications/Sublime Text.app/subl" -w`: {"/Applications/Sublime Text.app/subl", "-w"},
		`'my editor' --flag`:                       {"my editor", "--flag"},
		`emacsclient -c -a ''`:                     {"emacsclient", "-c", "-a", ""},
		`vim -c "set tw=72"`:                       {"vim", "-c", "set tw=72"},
		`path\ with\ spaces/ed`:                    {"path with spaces/ed"},
		"":                                         nil,
	}
	for in, want := range cases {
		got := splitCommandLine(in)
		if strings.Join(got, "|") != strings.Join(want, "|") || len(got) != len(want) {
			t.Errorf("splitCommandLine(%q) = %q, want %q", in, got, want)
		}
	}
}
