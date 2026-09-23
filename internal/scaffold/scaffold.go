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
	Name     string         // project name; becomes the directory basename when Dir is empty
	Dir      string         // target directory (absolute). Empty → ./<Name> resolved to absolute.
	Agent    agent.ID       // which agent to launch; empty defaults to claude
	Commands agent.Commands // optional configured agent executable paths
}

// LaunchCmd is the tmux launch command for a new project's session.
// Routes through agent.LaunchCmd(..., false, ...) — false because a
// brand-new project has no prior conversation to resume; passing
// --continue would make the agent hunt for a transcript that doesn't
// exist. Exposed (and tested) separately from StartSession so the
// "every agent's binary actually runs" invariant has a unit-test home
// that doesn't need a live tmux server.
func LaunchCmd(opts Options) string {
	return agent.LaunchCmd(opts.Agent, false, opts.Commands)
}

// PrepareDir resolves the project directory, creates it if missing, and
// records the chosen agent in the `.ccmux/agent` sidecar when the
// directory has none yet. It writes nothing else. Returned separately
// from StartSession so the filesystem behavior is unit-testable without
// a tmux server.
func PrepareDir(opts Options) (string, error) {
	dir, _, err := prepareDir(opts)
	return dir, err
}

// prepareDir is PrepareDir that also reports whether this call wrote
// the agent sidecar, so StartSession can undo it if the session never
// starts.
func prepareDir(opts Options) (dir string, wroteSidecar bool, err error) {
	if opts.Name == "" && opts.Dir == "" {
		return "", false, errors.New("scaffold: name required")
	}
	dir = opts.Dir
	if dir == "" {
		abs, err := filepath.Abs(opts.Name)
		if err != nil {
			return "", false, err
		}
		dir = abs
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false, fmt.Errorf("create project dir: %w", err)
	}
	// Record the chosen agent in ccmux's own sidecar — but only when
	// the caller named a real one, and only when the directory has no
	// recorded agent yet. An existing project keeps its agent:
	// `ccmux new <existing> --agent X` used to rewrite the sidecar and
	// silently switch the project for good (even when the session then
	// failed to start). Switching is an explicit action (Projects
	// screen `a`). An empty or bogus Agent leaves no sidecar, and
	// project.ReadAgent then falls back to Claude.
	if id, ok := agent.ParseID(string(opts.Agent)); ok {
		if _, statErr := os.Lstat(project.AgentSidecarPath(dir)); errors.Is(statErr, os.ErrNotExist) {
			if err := project.SetAgent(dir, id); err != nil {
				return "", false, err
			}
			wroteSidecar = true
		}
	}
	return dir, wroteSidecar, nil
}

// newSession and setSessionAgent are the tmux calls StartSession makes,
// swappable so its sidecar bookkeeping is testable without a tmux
// server.
var (
	newSession      = tmux.New
	setSessionAgent = tmux.SetSessionAgent
)

// StartSession creates the project directory (PrepareDir) and opens a
// detached tmux session running the chosen agent. It writes no project
// files. Returns the tmux session name; the caller attaches (via
// tmux.Attach which exec's, or tea.ExecProcess from the TUI).
func StartSession(ctx context.Context, opts Options) (string, error) {
	dir, wroteSidecar, err := prepareDir(opts)
	if err != nil {
		return "", err
	}
	session := tmux.SessionNameForPath(dir)
	if err := newSession(ctx, session, dir, LaunchCmd(opts)); err != nil {
		if wroteSidecar {
			// Don't leave the project recorded as an agent it never ran.
			_ = os.Remove(project.AgentSidecarPath(dir))
			_ = os.Remove(filepath.Dir(project.AgentSidecarPath(dir))) // only if now empty
		}
		return "", fmt.Errorf("start tmux session: %w", err)
	}
	// An existing project kept its recorded agent. If this session runs
	// a different one, pin it on the session (as a resumed conversation
	// does) so the daemon classifies the agent that's actually running.
	// Best effort: on failure detection falls back to the project's
	// recorded agent, and the session itself is fine.
	launched := agent.IDClaude // what LaunchCmd runs for an empty Agent
	if id, ok := agent.ParseID(string(opts.Agent)); ok {
		launched = id
	}
	if launched != project.ReadAgent(dir) {
		_ = setSessionAgent(ctx, session, string(launched))
	}
	return session, nil
}
