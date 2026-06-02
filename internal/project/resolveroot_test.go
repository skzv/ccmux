package project

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveRoot pins the projects-root resolution that every
// create/find path now shares. The tilde case is the regression: a
// config `root = "~/Projects"` must expand to an absolute path, not be
// passed through literally (which made the daemon create a "~" dir).
func TestResolveRoot(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty defaults to ~/Projects", "", filepath.Join(home, "Projects")},
		{"whitespace defaults too", "   ", filepath.Join(home, "Projects")},
		{"tilde expands and is absolute", "~/Projects", filepath.Join(home, "Projects")},
		{"tilde subdir", "~/code/work", filepath.Join(home, "code", "work")},
		{"absolute unchanged", "/srv/projects", "/srv/projects"},
		{"relative made absolute", filepath.Join("rel", "projects"), filepath.Join(cwd, "rel", "projects")},
	}
	for _, tc := range cases {
		if got := ResolveRoot(tc.in); got != tc.want {
			t.Errorf("%s: ResolveRoot(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}
