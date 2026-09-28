package tmux

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// sameBasenameDirs makes two existing directories named "api" under
// different parents — the collision ProjectSessionName resolves.
func sameBasenameDirs(t *testing.T) (first, second string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first = filepath.Join(base, "Projects", "api")
	second = filepath.Join(base, "work", "api")
	for _, d := range []string{first, second} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return first, second
}

// TestProjectSessionName — two projects with the same folder name both
// mapped to c-api, so opening the second attached to the first one's
// session, running in the wrong directory. The first keeps c-api; the
// second gets a path-tagged name, and each resolves to its own session
// from then on.
func TestProjectSessionName(t *testing.T) {
	first, second := sameBasenameDirs(t)
	tagged := PathTaggedSessionName(second)
	if !strings.HasPrefix(tagged, "c-api-") || len(tagged) != len("c-api-")+5 {
		t.Fatalf("PathTaggedSessionName(%s) = %q, want c-api-<5 letters>", second, tagged)
	}
	if PathTaggedSessionName(first) == tagged {
		t.Fatalf("both directories got the tag %q", tagged)
	}

	cases := []struct {
		name        string
		sessions    []Session
		dir         string
		want        string
		wantRunning bool
	}{
		{"no sessions: plain name, to create", nil, first, "c-api", false},
		{"plain name running here", []Session{{Name: "c-api", Path: first}}, first, "c-api", true},
		{"plain name running elsewhere: tagged, to create", []Session{{Name: "c-api", Path: first}}, second, tagged, false},
		{"tagged session running here", []Session{{Name: "c-api", Path: first}, {Name: tagged, Path: second}}, second, tagged, true},
		{"first project still gets its plain session", []Session{{Name: "c-api", Path: first}, {Name: tagged, Path: second}}, first, "c-api", true},
		{"tagged session outlives the plain one", []Session{{Name: tagged, Path: second}}, second, tagged, true},
		{"plain free again, other project's tagged doesn't matter", []Session{{Name: tagged, Path: second}}, first, "c-api", false},
		{"unknown path keeps the name-only behaviour", []Session{{Name: "c-api"}}, second, "c-api", true},
		{"a tagged-looking session elsewhere is not ours", []Session{{Name: "c-api", Path: first}, {Name: "c-api-hello", Path: first}}, second, tagged, false},
		{"a tagged-looking session with no path is not ours", []Session{{Name: "c-api", Path: first}, {Name: "c-api-hello"}}, second, tagged, false},
		{"another tag of this directory is ours", []Session{{Name: "c-api", Path: first}, {Name: "c-api-hello", Path: second}}, second, "c-api-hello", true},
		{"numbered sibling isn't a tagged session", []Session{{Name: "c-api", Path: first}, {Name: "c-api-2", Path: second}}, second, tagged, false},
		{"other sessions are ignored", []Session{{Name: "c-web", Path: second}, {Name: "work", Path: second}}, second, "c-api", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, running := ProjectSessionName(tc.sessions, tc.dir)
			if got != tc.want || running != tc.wantRunning {
				t.Errorf("ProjectSessionName(%v, %s) = %q, %v; want %q, %v", tc.sessions, tc.dir, got, running, tc.want, tc.wantRunning)
			}
		})
	}
}

// TestProjectSessionName_PathSpellings — tmux keeps a session's start
// directory as it was given, so the same directory reached through a
// symlink (or with a trailing slash) must still count as "here", and
// the tag must not depend on the spelling.
func TestProjectSessionName_PathSpellings(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	first, second := sameBasenameDirs(t)
	link := filepath.Join(filepath.Dir(filepath.Dir(second)), "worklink")
	if err := os.Symlink(filepath.Dir(second), link); err != nil {
		t.Fatal(err)
	}
	viaLink := filepath.Join(link, "api")
	if got, want := PathTaggedSessionName(viaLink), PathTaggedSessionName(second); got != want {
		t.Errorf("tag via symlink %q != tag of the real path %q", got, want)
	}
	// A directory that doesn't exist yet gets the tag it will have once
	// created through the symlink.
	future := filepath.Join(link, "later")
	before := PathTaggedSessionName(future)
	if err := os.Mkdir(filepath.Join(filepath.Dir(second), "later"), 0o755); err != nil {
		t.Fatal(err)
	}
	if after := PathTaggedSessionName(future); after != before {
		t.Errorf("tag changed once the directory existed: %q → %q", before, after)
	}
	if got, running := ProjectSessionName([]Session{{Name: "c-api", Path: viaLink}}, second+"/"); got != "c-api" || !running {
		t.Errorf("plain session started via a symlink: got %q, %v; want c-api running", got, running)
	}
	sessions := []Session{{Name: "c-api", Path: first}, {Name: PathTaggedSessionName(second), Path: viaLink}}
	if got, running := ProjectSessionName(sessions, second); got != PathTaggedSessionName(second) || !running {
		t.Errorf("tagged session started via a symlink: got %q, %v", got, running)
	}
}

// TestPathTaggedSessionName_Deterministic — the tag is a pure function
// of the path, so every process (CLI, TUI, daemon) derives the same
// name, and a sanitized basename keeps its own tag in front.
func TestPathTaggedSessionName_Deterministic(t *testing.T) {
	a := PathTaggedSessionName("/nonexistent/x/api")
	if a != PathTaggedSessionName("/nonexistent/x/api/") || a != PathTaggedSessionName("/nonexistent/x/../x/api") {
		t.Errorf("tag depends on the spelling of a clean path: %q", a)
	}
	if a == PathTaggedSessionName("/nonexistent/y/api") {
		t.Errorf("different directories share the tag %q", a)
	}
	dotted := PathTaggedSessionName("/nonexistent/x/my.app")
	if !strings.HasPrefix(dotted, SessionNameForPath("/nonexistent/x/my.app")+"-") {
		t.Errorf("tagged name %q should extend the sanitized plain name", dotted)
	}
	if !ValidSessionName(a) || !ValidSessionName(dotted) {
		t.Errorf("tagged names must be valid session names: %q %q", a, dotted)
	}
}

func TestIsPathTagged(t *testing.T) {
	for name, want := range map[string]bool{
		"c-api-abcde":  true,
		"c-api-abcd":   false,
		"c-api-abcdef": false,
		"c-api-ab2de":  false,
		"c-api-ABCDE":  false,
		"c-apix-abcde": false,
		"c-api_abcde":  false,
		"c-api":        false,
		"c-api-2":      false,
	} {
		if got := isPathTagged(name, "c-api"); got != want {
			t.Errorf("isPathTagged(%q, c-api) = %v, want %v", name, got, want)
		}
	}
}

func TestSamePath(t *testing.T) {
	first, second := sameBasenameDirs(t)
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{first, first, true},
		{first, first + "/", true},
		{first, second, false},
		{"", "", true},
		{"", first, false},
		{"/nonexistent/a", "/nonexistent/a/", true},
		{"/nonexistent/a", "/nonexistent/b", false},
	} {
		if got := SamePath(tc.a, tc.b); got != tc.want {
			t.Errorf("SamePath(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
