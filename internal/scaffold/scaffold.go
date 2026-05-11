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
	"strings"
	"time"

	"github.com/skzv/ccmux/internal/config"
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

// DefaultDirs is the ccmux-opinionated layout. Just the docs/ vault —
// no src/ or tests/, because those are language-specific (cmd+internal
// for Go, src+__tests__ for Node, the package dir for Python, …) and
// `/init` does a better job choosing the layout once Claude knows what
// you're building. The three numbered subdirs are deliberate:
//
//   - 01_Specs/         PRDs and feature specs
//   - 02_Architecture/  ADRs and system design
//   - 03_Agent_Logs/    daily scratchpad — AI sessions append here,
//                       and `ccmux new` auto-templates today's log
//
// All of this is overridable via [scaffold].dirs in
// ~/.config/ccmux/config.toml — set it to whatever shape you actually
// want and ccmux will create exactly that.
var DefaultDirs = []string{
	"docs/01_Specs",
	"docs/02_Architecture",
	"docs/03_Agent_Logs",
}

// DefaultGitignore is written to .gitignore on new projects.
const DefaultGitignore = `.DS_Store
node_modules/
.venv/
bin/
obj/
*.log
.obsidian/workspace.json
.obsidian/workspace-mobile.json
.obsidian/graph.json
`

// DefaultInitialPrompt is what ccmux sends to Claude after the new
// session boots. {{name}} and {{description}} are substituted.
//
// Deliberately local-only: we no longer ask Claude to create a GitHub
// repo here. The first session should be uninterrupted thinking, not
// network-touching side quests; the user can push later with `gh repo
// create --private --source=. --remote=origin --push` whenever they're
// ready. (Overridable via [scaffold].initial_prompt in config.toml.)
const DefaultInitialPrompt = `I'm starting a new project called "{{name}}". {{description}} ` +
	`Please: (1) Run /init to scaffold CLAUDE.md from scratch — there is no existing CLAUDE.md, so this should be one clean write. ` +
	`(2) The project already has these documentation directories: docs/01_Specs/ (specs/PRDs), docs/02_Architecture/ (ADRs), docs/03_Agent_Logs/ (daily scratchpad). Reflect this in CLAUDE.md's Directory Layout section. ` +
	`(3) Pick the right source-code layout for the language/stack we choose — e.g. cmd+internal for Go, src for Node/Python — and create those directories yourself. Don't assume src/+tests/. ` +
	`(4) Ask me 2-3 targeted questions about the concept, stack, and immediate goals, then write docs/01_Specs/00_Initial_Concept.md from my answers.`

// gitignoreSeed is kept for backward-compat with callers that referenced
// the old name; it points at DefaultGitignore.
const gitignoreSeed = DefaultGitignore

// Scaffold creates the directory and the convention layout. Idempotent:
// existing files are left alone. Reads the [scaffold] config section
// for overrides on dirs / gitignore body / initial-commit behavior;
// falls back to the package-level defaults when unset.
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
	cfg, _ := config.Load()

	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return err
	}
	dirs := cfg.Scaffold.Dirs
	if len(dirs) == 0 {
		dirs = DefaultDirs
	}
	for _, sub := range dirs {
		if err := os.MkdirAll(filepath.Join(opts.Dir, sub), 0o755); err != nil {
			return err
		}
	}

	gitignoreBody := cfg.Scaffold.GitignoreBody
	if strings.TrimSpace(gitignoreBody) == "" {
		gitignoreBody = DefaultGitignore
	}
	if err := writeIfMissing(filepath.Join(opts.Dir, "README.md"), "# "+opts.Name+"\n"); err != nil {
		return err
	}
	if err := writeIfMissing(filepath.Join(opts.Dir, ".gitignore"), gitignoreBody); err != nil {
		return err
	}

	if opts.SkipGit {
		return nil
	}
	if !cfg.Scaffold.CreateInitialCommit && !defaultIfZero(cfg.Scaffold.CreateInitialCommit) {
		// Config explicitly disables the initial commit.
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

// defaultIfZero treats a freshly-decoded zero-value bool as the default
// (true). TOML can't distinguish "unset" from "false" without a pointer,
// so we keep CreateInitialCommit defaulting to true in Defaults() and
// only honor an explicit false here when the config file was saved with
// it after Defaults() ran.
func defaultIfZero(b bool) bool { return b }

// InitialPrompt is the single composite message ccmux sends to a fresh
// Claude session in a new project. Reads the config's
// scaffold.initial_prompt if set; otherwise uses DefaultInitialPrompt.
// {{name}} and {{description}} are substituted in the chosen template.
func InitialPrompt(opts Options) string {
	desc := opts.Description
	if desc == "" {
		desc = "(no description yet — please ask me what I'm building)"
	}
	cfg, _ := config.Load()
	tmpl := cfg.Scaffold.InitialPrompt
	if strings.TrimSpace(tmpl) == "" {
		tmpl = DefaultInitialPrompt
	}
	return strings.NewReplacer(
		"{{name}}", opts.Name,
		"{{description}}", desc,
	).Replace(tmpl)
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
