// Package scaffold creates a new project's on-disk structure and starts
// its Claude session.
//
// Deliberate omission: we do NOT pre-write CLAUDE.md. Earlier versions of
// this workflow wrote an opinionated CLAUDE.md and then sent `/init`, which
// asks Claude to write CLAUDE.md — and Claude then needed permission to
// overwrite the file we just put there. By leaving CLAUDE.md absent and
// telling Claude (via the initial prompt) what directory layout to honor,
// `/init` creates the file cleanly with one consent rather than two.
package scaffold

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/skzv/ccmux/internal/tmux"
)

// Options is the input shape for both Scaffold and StartSession.
type Options struct {
	Name        string // project name; becomes the directory basename
	Description string // user's one-line "what are you building"
	Dir         string // target directory (absolute). If empty, ./<Name> resolved to absolute.
	SkipGit     bool   // upgrade-an-existing-project case: don't touch .git
	NoSession   bool   // scaffold only; don't start tmux
}

const gitignoreSeed = `.DS_Store
node_modules/
.venv/
bin/
obj/
*.log
.obsidian/workspace.json
.obsidian/workspace-mobile.json
.obsidian/graph.json
`

// Scaffold creates the directory and the convention layout. Idempotent:
// existing files are left alone.
func Scaffold(opts *Options) error {
	if opts.Name == "" {
		return errors.New("scaffold: name required")
	}
	if opts.Dir == "" {
		abs, err := filepath.Abs(opts.Name)
		if err != nil {
			return err
		}
		opts.Dir = abs
	}
	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return err
	}
	for _, sub := range []string{
		"src", "tests",
		"docs/01_Specs", "docs/02_Architecture", "docs/03_Agent_Logs",
	} {
		if err := os.MkdirAll(filepath.Join(opts.Dir, sub), 0o755); err != nil {
			return err
		}
	}
	if err := writeIfMissing(filepath.Join(opts.Dir, "README.md"), "# "+opts.Name+"\n"); err != nil {
		return err
	}
	if err := writeIfMissing(filepath.Join(opts.Dir, ".gitignore"), gitignoreSeed); err != nil {
		return err
	}
	if opts.SkipGit {
		return nil
	}
	if _, err := os.Stat(filepath.Join(opts.Dir, ".git")); errors.Is(err, os.ErrNotExist) {
		if err := exec.Command("git", "-C", opts.Dir, "init").Run(); err != nil {
			return fmt.Errorf("git init: %w", err)
		}
		_ = exec.Command("git", "-C", opts.Dir, "add", ".").Run()
		_ = exec.Command("git", "-C", opts.Dir, "commit", "-m", "initial commit: scaffolded structure").Run()
	}
	return nil
}

// InitialPrompt is the single composite message ccmux sends to a fresh
// Claude session in a new project. It tells Claude to /init (which works
// cleanly because CLAUDE.md doesn't exist yet), explains the layout we
// already created, and asks Claude to engage the user about the concept.
func InitialPrompt(opts Options) string {
	desc := opts.Description
	if desc == "" {
		desc = "(no description yet — please ask me what I'm building)"
	}
	return fmt.Sprintf(
		`I'm starting a new project called "%s". %s `+
			`Please: (1) Run /init to scaffold CLAUDE.md from scratch — there is no existing CLAUDE.md, so this should be one clean write. `+
			`(2) The project already has these directories: src/, tests/, docs/01_Specs/ (specs/PRDs), docs/02_Architecture/ (ADRs), docs/03_Agent_Logs/ (daily scratchpad). Reflect this in CLAUDE.md's Directory Layout section. `+
			`(3) Ask me 2-3 targeted questions about the concept, stack, and immediate goals, then write docs/01_Specs/00_Initial_Concept.md from my answers. `+
			`(4) Create a PRIVATE GitHub repo named "%s" and push the initial commit.`,
		opts.Name, desc, opts.Name,
	)
}

// StartSession runs Scaffold, then opens a detached tmux session with
// `claude`, waits for it to boot, and injects the initial prompt. Returns
// the tmux session name. The caller is responsible for attaching (either
// via tmux.Attach which exec's, or via tea.ExecProcess from the TUI).
func StartSession(ctx context.Context, opts Options) (string, error) {
	if err := Scaffold(&opts); err != nil {
		return "", err
	}
	session := tmux.SessionNameForPath(opts.Dir)
	if err := tmux.New(ctx, session, opts.Dir, "claude"); err != nil {
		return "", fmt.Errorf("start tmux session: %w", err)
	}
	if opts.NoSession {
		return session, nil
	}
	// Wait for Claude Code to boot. 3s matches the existing mkproj zsh
	// function which has been reliable.
	time.Sleep(3 * time.Second)
	if err := tmux.SendKeys(ctx, session, InitialPrompt(opts)); err != nil {
		return session, fmt.Errorf("send initial prompt: %w", err)
	}
	if err := tmux.SendKeys(ctx, session, "Enter"); err != nil {
		return session, err
	}
	return session, nil
}

func writeIfMissing(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
