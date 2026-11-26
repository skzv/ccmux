package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "init")
	return dir
}

func TestWorktree_CreateListRemoveLifecycle(t *testing.T) {
	repo := initTestRepo(t)
	ctx := context.Background()

	wt, err := Create(ctx, repo, "feat/auth", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	wantPath := filepath.Join(repo, DirName, "feat-auth")
	if wt.Path != wantPath || wt.Branch != "feat/auth" {
		t.Fatalf("Create = %+v, want path=%q branch=feat/auth", wt, wantPath)
	}

	// Verify .worktrees/ was added to .git/info/exclude so git status stays clean.
	excludeBytes, err := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if err != nil {
		t.Fatalf("read info/exclude: %v", err)
	}
	if !strings.Contains(string(excludeBytes), ".worktrees/") {
		t.Errorf("info/exclude missing .worktrees/: %q", string(excludeBytes))
	}

	// Calling Create again for the same branch reuses the existing worktree.
	wt2, err := Create(ctx, repo, "feat/auth", "")
	if err != nil {
		t.Fatalf("idempotent Create: %v", err)
	}
	if evalPath(wt2.Path) != evalPath(wt.Path) {
		t.Errorf("second Create returned %q, want %q", wt2.Path, wt.Path)
	}

	// List returns main + linked worktree.
	list, err := List(ctx, repo)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 || !list[0].Main || list[1].Branch != "feat/auth" {
		t.Fatalf("List = %+v", list)
	}

	// Refuses removing main checkout.
	if _, err := Remove(ctx, repo, "main", false); err == nil {
		t.Fatal("expected error removing main checkout")
	}

	// Remove linked worktree cleanly.
	removed, err := Remove(ctx, repo, "feat/auth", false)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if removed.Branch != "feat/auth" {
		t.Errorf("removed = %+v", removed)
	}
	listAfter, err := List(ctx, repo)
	if err != nil {
		t.Fatalf("List after remove: %v", err)
	}
	if len(listAfter) != 1 {
		t.Fatalf("len(List) after remove = %d, want 1", len(listAfter))
	}
}

func TestValidateBranchAndBranchFromPath(t *testing.T) {
	for _, bad := range []string{"", "-flag", "../escape", "a..b", "bad branch", "/leading"} {
		if err := ValidateBranch(bad); err == nil {
			t.Errorf("ValidateBranch(%q) = nil, want error", bad)
		}
	}
	for _, good := range []string{"main", "feat/auth", "fix-123", "release/v0.7.0"} {
		if err := ValidateBranch(good); err != nil {
			t.Errorf("ValidateBranch(%q) = %v, want nil", good, err)
		}
	}

	if slug, ok := BranchFromPath("/Users/demo/Projects/ccmux/.worktrees/feat-auth"); !ok || slug != "feat-auth" {
		t.Errorf("BranchFromPath = (%q, %v), want (feat-auth, true)", slug, ok)
	}
	if _, ok := BranchFromPath("/Users/demo/Projects/ccmux"); ok {
		t.Error("BranchFromPath should return false for non-worktree path")
	}
}
