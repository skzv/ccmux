package project

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// mkdir is a tiny test helper that creates a directory and t.Fatals on
// failure so call sites stay one-line.
func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSessionName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"foo", "c-foo"},
		{"foo.bar", "c-foo_bar"},
		{"a.b.c", "c-a_b_c"},
		{"no-dots-here", "c-no-dots-here"},
	}
	for _, tc := range cases {
		p := Project{Name: tc.in}
		if got := p.SessionName(); got != tc.want {
			t.Errorf("SessionName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestInspect_AcceptsAndRejects(t *testing.T) {
	root := t.TempDir()

	// has-git: only .git
	mkdir(t, filepath.Join(root, "has-git", ".git"))

	// has-cm: only CLAUDE.md
	writeFile(t, filepath.Join(root, "has-cm", "CLAUDE.md"), "# hi\n")

	// has-both: .git + CLAUDE.md + docs/
	mkdir(t, filepath.Join(root, "has-both", ".git"))
	writeFile(t, filepath.Join(root, "has-both", "CLAUDE.md"), "# hi\n")
	mkdir(t, filepath.Join(root, "has-both", "docs"))

	// empty: neither — should be rejected
	mkdir(t, filepath.Join(root, "empty"))

	// not-a-dir-claude: CLAUDE.md is a directory, not a file — rejected
	mkdir(t, filepath.Join(root, "weird", "CLAUDE.md"))

	cases := []struct {
		path           string
		wantOK         bool
		wantGit, wantCM, wantDocs bool
	}{
		{filepath.Join(root, "has-git"), true, true, false, false},
		{filepath.Join(root, "has-cm"), true, false, true, false},
		{filepath.Join(root, "has-both"), true, true, true, true},
		{filepath.Join(root, "empty"), false, false, false, false},
		{filepath.Join(root, "weird"), false, false, false, false},
		{filepath.Join(root, "missing"), false, false, false, false},
	}
	for _, tc := range cases {
		t.Run(filepath.Base(tc.path), func(t *testing.T) {
			p, ok := inspect(tc.path)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v, want %v (project=%+v)", ok, tc.wantOK, p)
			}
			if !ok {
				return
			}
			if p.HasGit != tc.wantGit || p.HasCM != tc.wantCM || p.HasDocs != tc.wantDocs {
				t.Errorf("flags: got git=%v cm=%v docs=%v, want git=%v cm=%v docs=%v",
					p.HasGit, p.HasCM, p.HasDocs, tc.wantGit, tc.wantCM, tc.wantDocs)
			}
			if p.Name != filepath.Base(tc.path) || p.Path != tc.path {
				t.Errorf("Name/Path mismatch: %+v", p)
			}
		})
	}
}

func TestDiscover_SkipsHiddenAndNonProjects(t *testing.T) {
	root := t.TempDir()
	mkdir(t, filepath.Join(root, "a", ".git"))
	writeFile(t, filepath.Join(root, "b", "CLAUDE.md"), "# b\n")
	mkdir(t, filepath.Join(root, "not-a-project"))         // no markers
	mkdir(t, filepath.Join(root, ".hidden", ".git"))       // hidden dir
	writeFile(t, filepath.Join(root, "loose.txt"), "junk") // not a dir

	got, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(got))
	for i, p := range got {
		names[i] = p.Name
	}
	sort.Strings(names)
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Fatalf("Discover returned %v, want [a b]", names)
	}
}

func TestDiscover_MissingRootReturnsNil(t *testing.T) {
	got, err := Discover(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("missing root should be nil error, got %v", err)
	}
	if got != nil {
		t.Fatalf("missing root should return nil slice, got %v", got)
	}
}

func TestDiscover_SortedByMostRecentMtime(t *testing.T) {
	root := t.TempDir()
	mkdir(t, filepath.Join(root, "older", ".git"))
	mkdir(t, filepath.Join(root, "newer", ".git"))

	older := time.Now().Add(-2 * time.Hour)
	newer := time.Now()
	if err := os.Chtimes(filepath.Join(root, "older"), older, older); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(root, "newer"), newer, newer); err != nil {
		t.Fatal(err)
	}

	got, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "newer" || got[1].Name != "older" {
		t.Fatalf("unsorted: %v", got)
	}
}

func TestLookup(t *testing.T) {
	root := t.TempDir()
	mkdir(t, filepath.Join(root, "foo", ".git"))
	if p, ok := Lookup(filepath.Join(root, "foo")); !ok || p.Name != "foo" {
		t.Fatalf("Lookup(foo) ok=%v p=%+v", ok, p)
	}
	if _, ok := Lookup(filepath.Join(root, "no-such-dir")); ok {
		t.Fatal("Lookup of missing dir should be false")
	}
}

func TestExpandHome_TildePrefixOnly(t *testing.T) {
	home, _ := os.UserHomeDir()
	got, err := expandHome("~/foo")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(home, "foo") {
		t.Errorf("~/foo -> %q, want %q", got, filepath.Join(home, "foo"))
	}
	// Tilde only counts at the very start with a slash.
	for _, in := range []string{"/abs/path", "rel/path", "~user/foo", "~"} {
		out, err := expandHome(in)
		if err != nil {
			t.Errorf("expandHome(%q) error: %v", in, err)
		}
		if out != in {
			t.Errorf("expandHome(%q) modified the path: %q", in, out)
		}
	}
}

// TestInspect_PrefersCLAUDEmdMtime — the Modified field should reflect
// the project's documentation, not just the directory's metadata, so the
// dashboard sort matches "most recently worked on".
func TestInspect_PrefersCLAUDEmdMtime(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "proj")
	mkdir(t, dir)
	writeFile(t, filepath.Join(dir, "CLAUDE.md"), "# x\n")

	dirOld := time.Now().Add(-4 * time.Hour)
	cmNew := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(dir, dirOld, dirOld); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, "CLAUDE.md"), cmNew, cmNew); err != nil {
		t.Fatal(err)
	}

	p, ok := inspect(dir)
	if !ok {
		t.Fatal("expected project")
	}
	if !p.Modified.Equal(cmNew) {
		t.Errorf("Modified = %v, want CLAUDE.md mtime %v", p.Modified, cmNew)
	}
}
