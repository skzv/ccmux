package conversations

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
)

func mkdirs(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestResolveEncodedPath(t *testing.T) {
	root := t.TempDir()
	mkdirs(t,
		filepath.Join(root, "Users", "me", "Projects", "my-app", "src"),
		filepath.Join(root, "Users", "me", "Projects", "my"), // decoy: no "app" inside
		filepath.Join(root, "Users", "me", "Projects", "site.io"),
		filepath.Join(root, "Users", "me", "Projects", "snake_case"),
		filepath.Join(root, "Users", "me", ".config", "tool"),
		filepath.Join(root, "Users", "me", "a", "b-c"),
	)
	cases := []struct{ enc, want string }{
		{"Users-me-Projects-my-app", filepath.Join(root, "Users", "me", "Projects", "my-app")},
		{"Users-me-Projects-my-app-src", filepath.Join(root, "Users", "me", "Projects", "my-app", "src")},
		{"Users-me-Projects-my", filepath.Join(root, "Users", "me", "Projects", "my")},
		{"Users-me-Projects-site-io", filepath.Join(root, "Users", "me", "Projects", "site.io")},
		{"Users-me-Projects-site.io", filepath.Join(root, "Users", "me", "Projects", "site.io")},
		{"Users-me-Projects-snake-case", filepath.Join(root, "Users", "me", "Projects", "snake_case")},
		{"Users-me--config-tool", filepath.Join(root, "Users", "me", ".config", "tool")},
		{"Users-me-a-b-c", filepath.Join(root, "Users", "me", "a", "b-c")},
		{"users-ME-projects-MY-APP", filepath.Join(root, "Users", "me", "Projects", "my-app")},
	}
	for _, tc := range cases {
		got, ok := resolveEncodedPath(root, encodePathSegment(tc.enc))
		if !ok || got != tc.want {
			t.Errorf("resolveEncodedPath(%q) = %q, %v; want %q", tc.enc, got, ok, tc.want)
		}
	}
	for _, enc := range []string{"Users-me-Projects-my-other-app", "Nope", "Users-me-Projects-my-app-src-x"} {
		if got, ok := resolveEncodedPath(root, encodePathSegment(enc)); ok {
			t.Errorf("resolveEncodedPath(%q) = %q, want no match", enc, got)
		}
	}
}

// TestListCursor_ResolvesHyphenatedProjectDir — regression: Cursor
// encodes /…/Projects/my-app as "…-Projects-my-app"; the naive decode
// produced /…/Projects/my/app, a directory that doesn't exist, and
// `tmux new -c` on a missing dir silently started the resumed session
// in $HOME. The project must resolve to the real directory, and a name
// nothing on disk matches must make ValidateResume refuse.
func TestListCursor_ResolvesHyphenatedProjectDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("encodes a POSIX absolute path")
	}
	base := t.TempDir()
	project := filepath.Join(base, "Projects", "my-app")
	mkdirs(t, project, filepath.Join(base, "Projects", "my")) // decoy "my"

	home := t.TempDir()
	encode := func(p string) string { return strings.ReplaceAll(strings.TrimPrefix(p, "/"), "/", "-") }
	transcript := `{"role":"user","message":{"content":[{"type":"text","text":"hi"}]},"timestamp":"2026-05-24T10:00:00Z"}` + "\n"
	writeFile(t, filepath.Join(home, ".cursor/projects", encode(project), "agent-transcripts/c1/c1.jsonl"), transcript)
	gone := filepath.Join(base, "Projects", "deleted-app")
	writeFile(t, filepath.Join(home, ".cursor/projects", encode(gone), "agent-transcripts/c2/c2.jsonl"), transcript)

	got, err := ListCursor(home)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Conversation{}
	for _, c := range got {
		byID[c.ID] = c
	}
	c1, c2 := byID["c1"], byID["c2"]
	if c1.Project != project {
		t.Errorf("c1 Project = %q, want %q", c1.Project, project)
	}
	if err := c1.ValidateResume(); err != nil {
		t.Errorf("c1 ValidateResume: %v", err)
	}
	if c2.Agent != agent.IDCursor {
		t.Fatalf("c2 missing: %+v", got)
	}
	if err := c2.ValidateResume(); err == nil {
		t.Errorf("c2 ValidateResume accepted non-existent project %q (tmux would start in $HOME)", c2.Project)
	}
}
