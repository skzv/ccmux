// Package worktree manages Git worktrees under <project>/.worktrees/<branch>
// so multiple coding agents (Claude, Codex, Antigravity) can work on parallel
// branches of the same repository without stashing or colliding.
package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/skzv/ccmux/internal/tmux"
)

// DirName is the hidden subdirectory inside a project where ccmux creates
// linked Git worktrees. Automatically added to .git/info/exclude so repos
// stay clean without modifying .gitignore.
const DirName = ".worktrees"

// Worktree describes one Git worktree belonging to a repository.
type Worktree struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	Head   string `json:"head,omitempty"`
	Main   bool   `json:"main,omitempty"`
}

// ValidateBranch checks that branch is a safe, valid Git branch name and
// cannot escape <project>/.worktrees/.
func ValidateBranch(branch string) error {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return fmt.Errorf("branch name cannot be empty")
	}
	if strings.HasPrefix(branch, "-") || strings.HasPrefix(branch, "/") || strings.HasSuffix(branch, "/") {
		return fmt.Errorf("invalid branch name %q", branch)
	}
	if strings.Contains(branch, "..") || strings.Contains(branch, "\\") || strings.ContainsAny(branch, " \t\r\n~^:?*[") {
		return fmt.Errorf("invalid branch name %q", branch)
	}
	for _, seg := range strings.Split(branch, "/") {
		if seg == "" || seg == "." || seg == ".." || strings.HasSuffix(seg, ".lock") {
			return fmt.Errorf("invalid branch name %q", branch)
		}
	}
	return nil
}

// SanitizeBranchDir maps a branch name (e.g. "feat/auth") to a single flat
// directory name under .worktrees/ (e.g. "feat-auth").
func SanitizeBranchDir(branch string) string {
	s := strings.TrimSpace(branch)
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, "\\", "-")
	return s
}

// SessionName returns the deterministic tmux session name for a worktree of
// projectName on branch (e.g. "c-ccmux-feat-auth").
func SessionName(projectName, branch string) string {
	return tmux.SessionNameForBase(projectName + "-" + SanitizeBranchDir(branch))
}

// BranchFromPath extracts the worktree directory slug from a session path
// if it lives inside <repo>/.worktrees/<slug>, without spawning git.
func BranchFromPath(sessionPath string) (string, bool) {
	if sessionPath == "" {
		return "", false
	}
	slashed := filepath.ToSlash(filepath.Clean(sessionPath))
	marker := "/" + DirName + "/"
	idx := strings.Index(slashed, marker)
	if idx < 0 {
		return "", false
	}
	rest := slashed[idx+len(marker):]
	if rest == "" {
		return "", false
	}
	slug, _, _ := strings.Cut(rest, "/")
	if slug == "" || slug == "." || slug == ".." {
		return "", false
	}
	return slug, true
}

// Create creates (or reuses an existing) Git worktree for branch at
// <repoDir>/.worktrees/<slug>. It ensures .worktrees/ is listed in
// <git-common-dir>/info/exclude so git status stays clean.
func Create(ctx context.Context, repoDir, branch, startPoint string) (Worktree, error) {
	branch = strings.TrimSpace(branch)
	if err := ValidateBranch(branch); err != nil {
		return Worktree{}, err
	}
	commonDir, err := gitCommonDir(ctx, repoDir)
	if err != nil {
		return Worktree{}, fmt.Errorf("%s is not a git repository: %w", repoDir, err)
	}
	if err := ensureExcluded(commonDir); err != nil {
		return Worktree{}, fmt.Errorf("exclude %s: %w", DirName, err)
	}

	wtRoot := filepath.Join(repoDir, DirName)
	if err := os.MkdirAll(wtRoot, 0o755); err != nil {
		return Worktree{}, fmt.Errorf("create %s: %w", wtRoot, err)
	}
	slug := SanitizeBranchDir(branch)
	wtPath := filepath.Join(wtRoot, slug)

	// If this worktree already exists in git's list, reuse it idempotently.
	if existing, err := List(ctx, repoDir); err == nil {
		wantPath := evalPath(wtPath)
		for _, w := range existing {
			if evalPath(w.Path) == wantPath || (w.Branch == branch && !w.Main) {
				return w, nil
			}
		}
	}

	var args []string
	if branchExists(ctx, repoDir, branch) {
		args = []string{"-C", repoDir, "worktree", "add", wtPath, branch}
	} else {
		args = []string{"-C", repoDir, "worktree", "add", "-b", branch, wtPath}
		if sp := strings.TrimSpace(startPoint); sp != "" {
			args = append(args, sp)
		}
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return Worktree{}, fmt.Errorf("git worktree add: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return Worktree{Path: wtPath, Branch: branch}, nil
}

// List enumerates all Git worktrees registered for repoDir.
func List(ctx context.Context, repoDir string) ([]Worktree, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "worktree", "list", "--porcelain")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return parseWorktreeList(string(out)), nil
}

// Remove removes a linked worktree matching branchOrPath (by branch name,
// slug under .worktrees/, or path). Refuses removing the primary checkout.
func Remove(ctx context.Context, repoDir, branchOrPath string, force bool) (Worktree, error) {
	target := strings.TrimSpace(branchOrPath)
	if target == "" {
		return Worktree{}, fmt.Errorf("branch or path required")
	}
	list, err := List(ctx, repoDir)
	if err != nil {
		return Worktree{}, err
	}
	slug := SanitizeBranchDir(target)
	wantPath := evalPath(filepath.Join(repoDir, DirName, slug))
	var matched *Worktree
	for i := range list {
		w := list[i]
		if w.Branch == target || evalPath(w.Path) == wantPath || evalPath(w.Path) == evalPath(target) {
			matched = &w
			break
		}
	}
	if matched == nil {
		return Worktree{}, fmt.Errorf("no worktree found for %q in %s", target, repoDir)
	}
	if matched.Main {
		return Worktree{}, fmt.Errorf("refusing to remove main repository checkout %s", matched.Path)
	}
	args := []string{"-C", repoDir, "worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, matched.Path)
	cmd := exec.CommandContext(ctx, "git", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return Worktree{}, fmt.Errorf("git worktree remove: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return *matched, nil
}

func gitCommonDir(ctx context.Context, repoDir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	out, err := cmd.Output()
	if err != nil {
		// Fallback for older git without --path-format=absolute.
		cmd2 := exec.CommandContext(ctx, "git", "-C", repoDir, "rev-parse", "--git-common-dir")
		out2, err2 := cmd2.Output()
		if err2 != nil {
			return "", err2
		}
		p := strings.TrimSpace(string(out2))
		if !filepath.IsAbs(p) {
			p = filepath.Join(repoDir, p)
		}
		return filepath.Clean(p), nil
	}
	return filepath.Clean(strings.TrimSpace(string(out))), nil
}

func branchExists(ctx context.Context, repoDir, branch string) bool {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return cmd.Run() == nil
}

func ensureExcluded(commonDir string) error {
	infoDir := filepath.Join(commonDir, "info")
	if err := os.MkdirAll(infoDir, 0o755); err != nil {
		return err
	}
	excludePath := filepath.Join(infoDir, "exclude")
	data, err := os.ReadFile(excludePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	pattern := DirName + "/"
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == pattern || strings.TrimSpace(line) == DirName {
			return nil
		}
	}
	var b strings.Builder
	b.Write(data)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		b.WriteByte('\n')
	}
	b.WriteString(pattern + "\n")
	return os.WriteFile(excludePath, []byte(b.String()), 0o644)
}

func parseWorktreeList(raw string) []Worktree {
	var out []Worktree
	var cur *Worktree
	flush := func() {
		if cur != nil && cur.Path != "" {
			if len(out) == 0 {
				cur.Main = true
			}
			out = append(out, *cur)
		}
		cur = nil
	}
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			flush()
			continue
		}
		key, val, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		switch key {
		case "worktree":
			flush()
			cur = &Worktree{Path: val}
		case "HEAD":
			if cur != nil {
				cur.Head = val
			}
		case "branch":
			if cur != nil {
				cur.Branch = strings.TrimPrefix(val, "refs/heads/")
			}
		}
	}
	flush()
	return out
}

func evalPath(p string) string {
	clean := filepath.Clean(p)
	if real, err := filepath.EvalSymlinks(clean); err == nil {
		return real
	}
	return clean
}
