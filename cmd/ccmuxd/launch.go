package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/project"
)

// projectLaunchCmd resolves the launch command for a project's tmux
// session from its .ccmux/agent sidecar. Pure helper so a test can
// pin "Antigravity project → agy launch" without standing up tmux.
//
// continueFlag=true matches the existing UX: every "attach to known
// project" path passes --continue so the user resumes their prior
// conversation; only fresh scaffolds start without --continue.
func projectLaunchCmd(projectPath string, continueFlag bool, commands agent.Commands) string {
	return agent.LaunchCmd(project.ReadAgent(projectPath), continueFlag, commands)
}

// bareSessionLaunchCmd resolves which command tmux new-session runs
// inside a new bare session. Precedence:
//
//  1. explicit request agent — the picker selection or
//     `ccmux shell --agent`. The literal "shell" short-circuits to
//     $SHELL so a conscious "no agent" pick isn't second-guessed by
//     the config default.
//  2. daemon's sessions.default_agent config (same rules).
//  3. $SHELL (or /bin/sh if $SHELL is unset).
//
// IDs are normalized via agent.ParseID so the daemon accepts the
// "gemini" back-compat alias. Exposed for tests so the precedence is
// pinned without standing up an http server.
func bareSessionLaunchCmd(reqAgent, configDefault string, commands agent.Commands) string {
	if cmd := agentLaunchCmdOrShell(reqAgent, false, commands); cmd != "" {
		return cmd
	}
	if cmd := agentLaunchCmdOrShell(configDefault, false, commands); cmd != "" {
		return cmd
	}
	return shellLaunchCmd()
}

// agentLaunchCmdOrShell decodes a single agent-id-or-"shell" string.
// Returns the LaunchCmd for a known agent, the shell command for an
// explicit "shell" pick, and "" for an empty or unrecognized value so
// the caller can fall through to the next precedence level.
func agentLaunchCmdOrShell(s string, continueFlag bool, commands agent.Commands) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return ""
	}
	if strings.EqualFold(trimmed, "shell") {
		return shellLaunchCmd()
	}
	if id, ok := agent.ParseID(trimmed); ok {
		return agent.LaunchCmd(id, continueFlag, commands)
	}
	return ""
}

// shellLaunchCmd is the bare-shell escape hatch.
func shellLaunchCmd() string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	return shell
}

// resolveBarePath picks the working directory for a bare session.
// Order: explicit req.Path → daemon's configured DefaultDir → $HOME.
// Exported as a helper so the unit tests can pin the priority.
func resolveBarePath(reqPath, configDefault string) string {
	for _, candidate := range []string{reqPath, configDefault} {
		if c := strings.TrimSpace(candidate); c != "" {
			return expandTilde(c)
		}
	}
	home, _ := os.UserHomeDir()
	return home
}

// expandTilde rewrites a leading "~/" to the daemon's $HOME. Bare-
// path strings come straight from config.toml and the wire; users
// expect "~/foo" to mean the daemon's home, not the client's. Other
// shell expansions ($VAR, *, …) are deliberately NOT handled —
// that's a recipe for surprises in a daemon process.
func expandTilde(p string) string {
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
