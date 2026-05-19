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

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tmux"
)

// Options is the input shape for both Scaffold and StartSession.
type Options struct {
	Name        string // project name; becomes the directory basename
	Description string // user's one-line "what are you building"
	Dir         string // target directory (absolute). If empty, ./<Name> resolved to absolute.
	SkipGit     bool   // upgrade-an-existing-project case: don't touch .git
	NoSession   bool   // scaffold only; don't start tmux

	// Agent is the AI agent this project will run (claude / codex /
	// antigravity). Empty means caller didn't care — Scaffold defaults to
	// agent.IDClaude so back-compat with pre-multi-agent callers
	// (every existing call site) is preserved without changing them.
	Agent agent.ID
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
//     and `ccmux new` auto-templates today's log
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

// Result describes what Scaffold actually did. Callers (the CLI's
// `ccmux upgrade` printout, the TUI's "u" toast) use it to tell the
// user whether the project was already up to date vs. what got added
// — without that, an idempotent re-run is indistinguishable from a
// no-op bug, which is exactly the friction reported in the field.
type Result struct {
	Dir          string
	CreatedDirs  []string // paths relative to Dir
	SkippedDirs  []string // existed already
	CreatedFiles []string // README.md, .gitignore
	SkippedFiles []string // existed already
	GitInit      bool     // we ran `git init` (and the initial commit)
}

// Changed reports whether Scaffold made any on-disk modification.
func (r *Result) Changed() bool {
	return len(r.CreatedDirs)+len(r.CreatedFiles) > 0 || r.GitInit
}

// Summary returns a single-line human-readable description of what
// changed, suitable for a CLI line or a TUI toast.
func (r *Result) Summary() string {
	if r == nil || !r.Changed() {
		return "already up to date"
	}
	parts := []string{}
	if n := len(r.CreatedDirs); n > 0 {
		parts = append(parts, fmt.Sprintf("%d dir", n))
		if n != 1 {
			parts[len(parts)-1] += "s"
		}
	}
	if len(r.CreatedFiles) > 0 {
		parts = append(parts, strings.Join(r.CreatedFiles, ", "))
	}
	if r.GitInit {
		parts = append(parts, "git init")
	}
	return "added " + strings.Join(parts, ", ")
}

// Scaffold creates the directory and the convention layout. Idempotent:
// existing files are left alone. Reads the [scaffold] config section
// for overrides on dirs / gitignore body / initial-commit behavior;
// falls back to the package-level defaults when unset. The returned
// Result describes exactly what was touched so callers can render
// meaningful output instead of staying silent.
func Scaffold(opts *Options) (*Result, error) {
	res := &Result{}
	if opts.Name == "" {
		return res, errors.New("scaffold: name required")
	}
	if opts.Dir == "" {
		abs, err := filepath.Abs(opts.Name)
		if err != nil {
			return res, err
		}
		opts.Dir = abs
	}
	res.Dir = opts.Dir
	cfg, _ := config.Load()

	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return res, err
	}
	dirs := cfg.Scaffold.Dirs
	if len(dirs) == 0 {
		dirs = DefaultDirs
	}
	for _, sub := range dirs {
		p := filepath.Join(opts.Dir, sub)
		existed := false
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			existed = true
		}
		if err := os.MkdirAll(p, 0o755); err != nil {
			return res, err
		}
		if existed {
			res.SkippedDirs = append(res.SkippedDirs, sub)
		} else {
			res.CreatedDirs = append(res.CreatedDirs, sub)
		}
	}

	gitignoreBody := cfg.Scaffold.GitignoreBody
	if strings.TrimSpace(gitignoreBody) == "" {
		gitignoreBody = DefaultGitignore
	}
	if created, err := writeIfMissing(filepath.Join(opts.Dir, "README.md"), "# "+opts.Name+"\n"); err != nil {
		return res, err
	} else if created {
		res.CreatedFiles = append(res.CreatedFiles, "README.md")
	} else {
		res.SkippedFiles = append(res.SkippedFiles, "README.md")
	}
	if created, err := writeIfMissing(filepath.Join(opts.Dir, ".gitignore"), gitignoreBody); err != nil {
		return res, err
	} else if created {
		res.CreatedFiles = append(res.CreatedFiles, ".gitignore")
	} else {
		res.SkippedFiles = append(res.SkippedFiles, ".gitignore")
	}

	// Write the per-project agent sidecar so the dashboard, daemon
	// poll loop, and "attach" path all know which agent to launch.
	//
	// Three behaviors interlock here:
	//
	//  1. Caller specified a valid Agent → write it, overwriting any
	//     existing sidecar. This is the "user picked Codex in the new-
	//     project form" or "scaffold StartSession got an explicit
	//     choice from the daemon's POST /v1/projects" path.
	//
	//  2. Caller specified an invalid Agent (typo) → fall back to
	//     claude. Better than persisting garbage and confusing the
	//     dispatcher.
	//
	//  3. Caller didn't specify an Agent (empty) AND a sidecar already
	//     exists → leave the existing choice alone. This is the
	//     critical upgrade-path behavior: `ccmux upgrade` runs Scaffold
	//     with Options{Agent: ""}, and a user who had previously
	//     chosen Codex must not be silently flipped back to Claude.
	//
	//  4. Caller didn't specify an Agent AND no sidecar yet → write
	//     claude. This is the legacy callers (every existing call site
	//     before Phase 3) plus brand-new projects that didn't go
	//     through the new-project form's picker.
	chosen, explicit := agent.ParseID(string(opts.Agent))
	if !explicit && string(opts.Agent) != "" {
		// Typo → coerce to default, but treat as explicit so we
		// overwrite. Callers that pass garbage deserve correction,
		// not silent preservation.
		chosen = agent.IDClaude
		explicit = true
	}
	sidecarPath := filepath.Join(opts.Dir, ".ccmux", "agent")
	sidecarExisted := false
	if _, err := os.Stat(sidecarPath); err == nil {
		sidecarExisted = true
	}
	switch {
	case explicit:
		if err := project.SetAgent(opts.Dir, chosen); err != nil {
			return res, err
		}
		if sidecarExisted {
			res.SkippedFiles = append(res.SkippedFiles, ".ccmux/agent")
		} else {
			res.CreatedFiles = append(res.CreatedFiles, ".ccmux/agent")
		}
	case !sidecarExisted:
		// No caller preference, no existing sidecar → seed with claude.
		if err := project.SetAgent(opts.Dir, agent.IDClaude); err != nil {
			return res, err
		}
		res.CreatedFiles = append(res.CreatedFiles, ".ccmux/agent")
	default:
		// No caller preference, existing sidecar → preserve user's
		// previous choice. Don't even mention it in the report —
		// it's a non-event.
		res.SkippedFiles = append(res.SkippedFiles, ".ccmux/agent")
	}

	if opts.SkipGit {
		return res, nil
	}
	if !cfg.Scaffold.CreateInitialCommit && !defaultIfZero(cfg.Scaffold.CreateInitialCommit) {
		// Config explicitly disables the initial commit.
		return res, nil
	}
	if _, err := os.Stat(filepath.Join(opts.Dir, ".git")); errors.Is(err, os.ErrNotExist) {
		if err := exec.Command("git", "-C", opts.Dir, "init").Run(); err != nil {
			return res, fmt.Errorf("git init: %w", err)
		}
		_ = exec.Command("git", "-C", opts.Dir, "add", ".").Run()
		_ = exec.Command("git", "-C", opts.Dir, "commit", "-m", "initial commit: scaffolded structure").Run()
		res.GitInit = true
	}
	return res, nil
}

// defaultIfZero treats a freshly-decoded zero-value bool as the default
// (true). TOML can't distinguish "unset" from "false" without a pointer,
// so we keep CreateInitialCommit defaulting to true in Defaults() and
// only honor an explicit false here when the config file was saved with
// it after Defaults() ran.
func defaultIfZero(b bool) bool { return b }

// InitialPrompt is the single composite message ccmux sends to a fresh
// agent session in a new project. Resolution order:
//
//  1. cfg.Scaffold.InitialPrompt (user override) — applied to every
//     agent, since a user who set a custom template meant it.
//  2. The picked agent's own InitialPrompt — Claude asks for /init +
//     CLAUDE.md, Antigravity asks for AGENTS.md, etc. Each agent's
//     bootstrap ritual is different and the per-agent string lives in
//     internal/agent/<id>.go.
//
// The Claude-specific DefaultInitialPrompt constant is retained as a
// back-compat fallback for the config-override path (existing
// installations referencing it via expanded copies in config.toml
// keep working).
func InitialPrompt(opts Options) string {
	desc := opts.Description
	if desc == "" {
		desc = "(no description yet — please ask me what I'm building)"
	}
	cfg, _ := config.Load()
	if tmpl := strings.TrimSpace(cfg.Scaffold.InitialPrompt); tmpl != "" {
		return strings.NewReplacer(
			"{{name}}", opts.Name,
			"{{description}}", desc,
		).Replace(tmpl)
	}
	return agent.ByID(opts.Agent).InitialPrompt(opts.Name, opts.Description)
}

// LaunchCmd is the tmux launch command for a fresh scaffold. Routes
// through agent.ByID(opts.Agent).LaunchCmd(false) — `false` because a
// brand-new project has no prior conversation to resume; passing
// --continue here would make the agent look for a transcript that
// doesn't exist. Exposed (and tested) separately from StartSession so
// the "every agent's binary actually runs" invariant has a unit-test
// home that doesn't need a live tmux server.
func LaunchCmd(opts Options) string {
	return agent.ByID(opts.Agent).LaunchCmd(false)
}

// StartSession runs Scaffold, then opens a detached tmux session with
// the picked agent's binary, waits for it to boot, and injects the
// initial prompt. Returns the tmux session name. The caller is
// responsible for attaching (either via tmux.Attach which exec's, or
// via tea.ExecProcess from the TUI).
//
// Bug history: this used to hardcode "claude", which meant projects
// created with Codex / Antigravity in the picker still launched claude
// in tmux while the sidecar said otherwise. Now resolves the launch
// command via agent.ByID(opts.Agent) so the agent the user picked is
// the agent that runs.
func StartSession(ctx context.Context, opts Options) (string, error) {
	if _, err := Scaffold(&opts); err != nil {
		return "", err
	}
	session := tmux.SessionNameForPath(opts.Dir)
	if err := tmux.New(ctx, session, opts.Dir, LaunchCmd(opts)); err != nil {
		return "", fmt.Errorf("start tmux session: %w", err)
	}
	if opts.NoSession {
		return session, nil
	}
	// Wait for the agent to boot. 3s matches the existing mkproj zsh
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

// writeIfMissing writes `content` to `path` only when the file does not
// already exist. Returns created=true iff a new file was written so the
// caller can attribute it to CreatedFiles vs SkippedFiles.
func writeIfMissing(path, content string) (created bool, err error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	}
	return true, os.WriteFile(path, []byte(content), 0o644)
}
