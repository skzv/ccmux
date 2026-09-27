package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tmux"
)

// projectLaunchCmd resolves which agent a new project session runs and
// the command that launches it: the agent the request named, else the
// project's .ccmux/agent sidecar (Claude when there is none). Pure
// helper so a test can pin "Antigravity project → agy launch" without
// standing up tmux.
//
// The requested agent wins outright rather than being written to the
// sidecar and read back: when the sidecar couldn't be written (a
// read-only project, a file in its place) a {"agent":"codex"} request
// launched claude.
//
// continueFlag=true matches the existing UX: every "attach to known
// project" path passes --continue so the user resumes their prior
// conversation; only fresh scaffolds start without --continue.
func projectLaunchCmd(projectPath string, requested agent.ID, continueFlag bool, commands agent.Commands) (agent.ID, string) {
	id := requested
	if id == "" {
		id = project.ReadAgent(projectPath)
	}
	return id, agent.LaunchCmd(id, continueFlag, commands)
}

// requestAgent parses the optional agent id of a create request: ""
// means none was given, a known id (or alias) is that agent, and
// anything else is an error the handler answers with 400. An unknown
// id used to be ignored, silently launching the default agent in place
// of a mistyped one.
func requestAgent(raw string) (agent.ID, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	if id, ok := agent.ParseID(s); ok {
		return id, nil
	}
	return "", fmt.Errorf("unknown agent %q", s)
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
// IDs are normalized via agent.ParseID, keeping Gemini distinct from
// Antigravity. Exposed for tests so the precedence is
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

// bareSessionAgentTag is the @ccmux_agent tag for a bare session,
// resolved with the same precedence as bareSessionLaunchCmd: the
// requested agent, then the configured default, then a plain shell.
func bareSessionAgentTag(reqAgent, configDefault string) string {
	for _, s := range []string{reqAgent, configDefault} {
		trimmed := strings.TrimSpace(s)
		if strings.EqualFold(trimmed, tmux.ShellAgentTag) {
			return tmux.ShellAgentTag
		}
		if id, ok := agent.ParseID(trimmed); ok {
			return string(id)
		}
	}
	return tmux.ShellAgentTag
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
