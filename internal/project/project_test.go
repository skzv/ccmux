package project

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
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
		// Broader sanitization — matches the fuzz-driven update to
		// tmux.SessionNameForPath. Any character outside
		// [a-zA-Z0-9_-] becomes `_`.
		{"with:colon", "c-with_colon"},
		{"with space", "c-with_space"},
		{"with/slash", "c-with_slash"},
	}
	for _, tc := range cases {
		p := Project{Name: tc.in}
		if got := p.SessionName(); got != tc.want {
			t.Errorf("SessionName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestInspect_AcceptsEveryDirAndSurfacesFlags pins both halves of the
// post-marker-rule contract:
//   - Any directory passes (empty, marker-less, "CLAUDE.md is itself a
//     directory" — all surface as projects).
//   - HasGit / HasCM / HasDocs still populate when the markers are
//     present, because the Projects screen renders them as visual tags
//     so the user can tell "real software project" from "scratch dir."
//
// Only a non-directory or missing path is rejected.
func TestInspect_AcceptsEveryDirAndSurfacesFlags(t *testing.T) {
	root := t.TempDir()

	// has-git: only .git
	mkdir(t, filepath.Join(root, "has-git", ".git"))

	// has-cm: only CLAUDE.md
	writeFile(t, filepath.Join(root, "has-cm", "CLAUDE.md"), "# hi\n")

	// has-agents: only AGENTS.md (Codex / cross-agent convention).
	writeFile(t, filepath.Join(root, "has-agents", "AGENTS.md"), "# agents\n")

	// has-all: .git + CLAUDE.md + AGENTS.md + docs/
	mkdir(t, filepath.Join(root, "has-all", ".git"))
	writeFile(t, filepath.Join(root, "has-all", "CLAUDE.md"), "# hi\n")
	writeFile(t, filepath.Join(root, "has-all", "AGENTS.md"), "# agents\n")
	mkdir(t, filepath.Join(root, "has-all", "docs"))

	// empty: no markers — still a project.
	mkdir(t, filepath.Join(root, "empty"))

	// not-a-dir-claude: CLAUDE.md is a directory, not a file. Still
	// a project (the parent qualifies as a directory), but HasCM
	// must NOT be true — a directory isn't the CLAUDE.md memory file.
	mkdir(t, filepath.Join(root, "weird", "CLAUDE.md"))

	// not-a-dir: a regular file under the root is not a project.
	writeFile(t, filepath.Join(root, "loose.txt"), "x")

	cases := []struct {
		path                                  string
		wantOK                                bool
		wantGit, wantCM, wantAgents, wantDocs bool
	}{
		{filepath.Join(root, "has-git"), true, true, false, false, false},
		{filepath.Join(root, "has-cm"), true, false, true, false, false},
		{filepath.Join(root, "has-agents"), true, false, false, true, false},
		{filepath.Join(root, "has-all"), true, true, true, true, true},
		{filepath.Join(root, "empty"), true, false, false, false, false},
		{filepath.Join(root, "weird"), true, false, false, false, false},
		{filepath.Join(root, "loose.txt"), false, false, false, false, false},
		{filepath.Join(root, "missing"), false, false, false, false, false},
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
			if p.HasGit != tc.wantGit || p.HasCM != tc.wantCM || p.HasAgents != tc.wantAgents || p.HasDocs != tc.wantDocs {
				t.Errorf("flags: got git=%v cm=%v agents=%v docs=%v, want git=%v cm=%v agents=%v docs=%v",
					p.HasGit, p.HasCM, p.HasAgents, p.HasDocs,
					tc.wantGit, tc.wantCM, tc.wantAgents, tc.wantDocs)
			}
			if p.Name != filepath.Base(tc.path) || p.Path != tc.path {
				t.Errorf("Name/Path mismatch: %+v", p)
			}
		})
	}
}

func TestDiscover_SkipsHiddenAndNonDirs(t *testing.T) {
	root := t.TempDir()
	mkdir(t, filepath.Join(root, "a", ".git"))
	writeFile(t, filepath.Join(root, "b", "CLAUDE.md"), "# b\n")
	mkdir(t, filepath.Join(root, "c-no-markers"))          // surfaces too
	mkdir(t, filepath.Join(root, ".hidden", ".git"))       // hidden dir — skipped
	writeFile(t, filepath.Join(root, "loose.txt"), "junk") // not a dir — skipped

	got, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(got))
	for i, p := range got {
		names[i] = p.Name
	}
	sort.Strings(names)
	want := []string{"a", "b", "c-no-markers"}
	if len(names) != len(want) {
		t.Fatalf("Discover returned %v, want %v", names, want)
	}
	for i, n := range want {
		if names[i] != n {
			t.Errorf("Discover[%d] = %q, want %q (full: %v)", i, names[i], n, names)
		}
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

// projectScaffold builds a minimal directory that passes inspect()'s
// "must have .git or CLAUDE.md" gate so we can exercise Agent
// detection without pulling in the full scaffold package.
func projectScaffold(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	mkdir(t, filepath.Join(dir, ".git"))
	return dir
}

// TestReadAgent_MissingFile is the back-compat path: every project
// scaffolded before .ccmux/agent existed must resolve to claude. If
// this regresses, every existing user's project silently becomes
// "unknown agent" overnight.
func TestReadAgent_MissingFile(t *testing.T) {
	dir := projectScaffold(t, t.TempDir(), "p")
	if got := ReadAgent(dir); got != agent.IDClaude {
		t.Errorf("missing sidecar: got %q, want claude (back-compat)", got)
	}
}

// TestReadAgent_AllKnownIDs round-trips every shipped agent through
// disk so a future agent ID rename trips at least one test instead of
// silently breaking sidecars in the wild.
func TestReadAgent_AllKnownIDs(t *testing.T) {
	for _, id := range []agent.ID{agent.IDClaude, agent.IDCodex, agent.IDAntigravity, agent.IDCursor} {
		t.Run(string(id), func(t *testing.T) {
			dir := projectScaffold(t, t.TempDir(), "p")
			if err := SetAgent(dir, id); err != nil {
				t.Fatal(err)
			}
			if got := ReadAgent(dir); got != id {
				t.Errorf("ReadAgent after SetAgent(%q) = %q", id, got)
			}
		})
	}
}

// TestReadAgent_InvalidContentFallsBackToClaude — a user hand-editing
// the sidecar to a typo'd value (e.g. "claude-3-sonnet") must not
// crash the daemon. Falling back to claude is the safe degradation
// path.
func TestReadAgent_InvalidContentFallsBackToClaude(t *testing.T) {
	dir := projectScaffold(t, t.TempDir(), "p")
	mkdir(t, filepath.Join(dir, ".ccmux"))
	writeFile(t, filepath.Join(dir, ".ccmux", "agent"), "claude-3-sonnet\n")
	if got := ReadAgent(dir); got != agent.IDClaude {
		t.Errorf("invalid content: got %q, want claude", got)
	}
}

// TestReadAgent_TrimsWhitespace — editors that strip trailing newlines
// or add them shouldn't break detection. We always pass through
// agent.ParseID which is whitespace-tolerant; this test pins that
// behavior at the sidecar boundary too.
func TestReadAgent_TrimsWhitespace(t *testing.T) {
	cases := []string{"codex", "codex\n", "  codex  ", "CODEX\n\n"}
	for _, body := range cases {
		t.Run(body, func(t *testing.T) {
			dir := projectScaffold(t, t.TempDir(), "p")
			mkdir(t, filepath.Join(dir, ".ccmux"))
			writeFile(t, filepath.Join(dir, ".ccmux", "agent"), body)
			if got := ReadAgent(dir); got != agent.IDCodex {
				t.Errorf("body=%q: got %q, want codex", body, got)
			}
		})
	}
}

// TestSetAgent_CreatesSidecarDir — `.ccmux/` shouldn't have to exist
// up front. SetAgent is what most callers (scaffold, picker) reach
// for, so the mkdir has to be implicit.
func TestSetAgent_CreatesSidecarDir(t *testing.T) {
	dir := projectScaffold(t, t.TempDir(), "p")
	// No .ccmux yet.
	if _, err := os.Stat(filepath.Join(dir, ".ccmux")); err == nil {
		t.Fatal("test precondition violated: .ccmux already exists")
	}
	if err := SetAgent(dir, agent.IDAntigravity); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, ".ccmux", "agent"))
	if err != nil {
		t.Fatalf("sidecar not written: %v", err)
	}
	if got := strings.TrimSpace(string(body)); got != "antigravity" {
		t.Errorf("body = %q, want antigravity", got)
	}
	// Trailing newline kept — POSIX text-file convention. A diff
	// against a git-tracked file should be one-line, not a no-newline-
	// at-EOF warning.
	if !strings.HasSuffix(string(body), "\n") {
		t.Errorf("expected trailing newline in sidecar: %q", body)
	}
}

// TestSetAgent_Idempotent — re-writing the same agent must not corrupt
// the file or leave behind a half-written version. The simplest pin
// is "body unchanged across two writes."
func TestSetAgent_Idempotent(t *testing.T) {
	dir := projectScaffold(t, t.TempDir(), "p")
	if err := SetAgent(dir, agent.IDCodex); err != nil {
		t.Fatal(err)
	}
	body1, _ := os.ReadFile(filepath.Join(dir, ".ccmux", "agent"))
	if err := SetAgent(dir, agent.IDCodex); err != nil {
		t.Fatal(err)
	}
	body2, _ := os.ReadFile(filepath.Join(dir, ".ccmux", "agent"))
	if string(body1) != string(body2) {
		t.Errorf("repeated SetAgent changed sidecar:\n first:%q\n second:%q",
			body1, body2)
	}
}

// TestSetAgent_RejectsUnknown — a typo'd caller (e.g. forwarding a
// CLI flag literally) must surface as an error, not write garbage to
// disk that ReadAgent will then silently coerce to claude.
func TestSetAgent_RejectsUnknown(t *testing.T) {
	dir := projectScaffold(t, t.TempDir(), "p")
	if err := SetAgent(dir, agent.ID("imaginary")); err == nil {
		t.Error("expected error for unknown id, got nil")
	}
	// And no file should have been created.
	if _, err := os.Stat(filepath.Join(dir, ".ccmux", "agent")); err == nil {
		t.Error("SetAgent persisted unknown id to disk")
	}
}

// TestSetAgent_Switching is the user-facing "I want this project to
// run Codex now" path. After switching, ReadAgent must reflect the
// new value (not a stale cached one).
func TestSetAgent_Switching(t *testing.T) {
	dir := projectScaffold(t, t.TempDir(), "p")
	if err := SetAgent(dir, agent.IDClaude); err != nil {
		t.Fatal(err)
	}
	if got := ReadAgent(dir); got != agent.IDClaude {
		t.Fatalf("post-set claude: got %q", got)
	}
	if err := SetAgent(dir, agent.IDCodex); err != nil {
		t.Fatal(err)
	}
	if got := ReadAgent(dir); got != agent.IDCodex {
		t.Errorf("post-switch: got %q, want codex", got)
	}
}

// TestInspect_PopulatesAgentField — the integration moment: discover
// surfaces the sidecar's agent as Project.Agent. Without this the
// downstream dispatch (Phase 4) has no input to work from.
func TestInspect_PopulatesAgentField(t *testing.T) {
	dir := projectScaffold(t, t.TempDir(), "p")
	if err := SetAgent(dir, agent.IDAntigravity); err != nil {
		t.Fatal(err)
	}
	p, ok := inspect(dir)
	if !ok {
		t.Fatal("expected inspect to recognize project")
	}
	if p.Agent != agent.IDAntigravity {
		t.Errorf("Project.Agent = %q, want antigravity", p.Agent)
	}
}

// TestInspect_MissingSidecarDefaultsToClaude pins the back-compat
// path through Discover too. Every legacy project that lacks
// .ccmux/agent must surface as Agent=claude on the dashboard.
func TestInspect_MissingSidecarDefaultsToClaude(t *testing.T) {
	dir := projectScaffold(t, t.TempDir(), "p")
	p, ok := inspect(dir)
	if !ok {
		t.Fatal("expected inspect to recognize project")
	}
	if p.Agent != agent.IDClaude {
		t.Errorf("Project.Agent (no sidecar) = %q, want claude", p.Agent)
	}
}
