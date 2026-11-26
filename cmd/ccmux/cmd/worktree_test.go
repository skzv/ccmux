package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/worktree"
)

func TestWorktreeCLI_ListAndRemove(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := filepath.Join(home, "Projects", "demo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", repo}, args...)...)
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
	runGit("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "README.md")
	runGit("commit", "-m", "init")

	// Create a worktree via package API, then verify CLI list --json and remove --keep-session.
	if _, err := worktree.Create(context.Background(), repo, "feat/cli-wt", ""); err != nil {
		t.Fatalf("worktree.Create: %v", err)
	}

	listCmd := newWorktreeListCmd()
	var out bytes.Buffer
	listCmd.SetOut(&out)
	listCmd.SetArgs([]string{repo, "--json"})
	if err := listCmd.Execute(); err != nil {
		t.Fatalf("worktree list --json: %v", err)
	}
	var got []worktree.Worktree
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal json: %v (%s)", err, out.String())
	}
	if len(got) != 2 || got[1].Branch != "feat/cli-wt" {
		t.Fatalf("unexpected worktree list: %+v", got)
	}

	rmCmd := newWorktreeRemoveCmd()
	out.Reset()
	rmCmd.SetOut(&out)
	rmCmd.SetArgs([]string{repo, "feat/cli-wt", "--keep-session"})
	if err := rmCmd.Execute(); err != nil {
		t.Fatalf("worktree remove: %v", err)
	}
	if !strings.Contains(out.String(), "removed worktree feat/cli-wt") {
		t.Errorf("unexpected remove output: %q", out.String())
	}
}
