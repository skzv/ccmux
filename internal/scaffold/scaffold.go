// Package scaffold creates a new project's directory and starts its
// agent session.
//
// It deliberately creates NO project files — no CLAUDE.md, no docs/
// tree, no .gitignore, no README.md, no git init, no GitHub repo.
// Bootstrapping a project is the user's job, done inside the session
// (`/init`, `openspec`, `git init`, …). ccmux only opens the door:
// it makes the directory and launches the agent.
//
// The one thing it does write is ccmux's own metadata — the
// `.ccmux/agent` sidecar — so the dashboard, daemon poll loop, and
// future attaches all launch the agent the project was created with.
// That is infrastructure, not project scaffolding.
package scaffold

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tmux"
)

// Options is the input shape for StartSession.
type Options struct {
	Name  string   // project name; becomes the directory basename when Dir is empty
	Dir   string   // target directory (absolute). Empty → ./<Name> resolved to absolute.
	Agent agent.ID // which agent to launch; empty defaults to claude
}

// LaunchCmd is the tmux launch command for a new project's session.
// Routes through agent.ByID(opts.Agent).LaunchCmd(false) — false
// because a brand-new project has no prior conversation to resume;
// passing --continue would make the agent hunt for a transcript that
// doesn't exist. Exposed (and tested) separately from StartSession so
// the "every agent's binary actually runs" invariant has a unit-test
// home that doesn't need a live tmux server.
func LaunchCmd(opts Options) string {
	return agent.ByID(opts.Agent).LaunchCmd(false)
}

// PrepareDir resolves the project directory, creates it if missing, and
// records the chosen agent in the `.ccmux/agent` sidecar. It writes
// nothing else. Returned separately from StartSession so the
// filesystem behavior is unit-testable without a tmux server.
func PrepareDir(opts Options) (string, error) {
	if opts.Name == "" && opts.Dir == "" {
		return "", errors.New("scaffold: name required")
	}
	dir := opts.Dir
	if dir == "" {
		abs, err := filepath.Abs(opts.Name)
		if err != nil {
			return "", err
		}
		dir = abs
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create project dir: %w", err)
	}
	// Record the chosen agent in ccmux's own sidecar — but only when
	// the caller named a real one. An empty or bogus Agent leaves no
	// sidecar, and project.ReadAgent then falls back to Claude.
	if id, ok := agent.ParseID(string(opts.Agent)); ok {
		if err := project.SetAgent(dir, id); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// StartSession creates the project directory (PrepareDir) and opens a
// detached tmux session running the chosen agent. It writes no project
// files. Returns the tmux session name; the caller attaches (via
// tmux.Attach which exec's, or tea.ExecProcess from the TUI).
func StartSession(ctx context.Context, opts Options) (string, error) {
	dir, err := PrepareDir(opts)
	if err != nil {
		return "", err
	}
	session := tmux.SessionNameForPath(dir)
	if err := tmux.New(ctx, session, dir, LaunchCmd(opts)); err != nil {
		return "", fmt.Errorf("start tmux session: %w", err)
	}
	return session, nil
}
