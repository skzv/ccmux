package agent

import (
	"regexp"

	"github.com/skzv/ccmux/internal/claude"
)

// InForeground names the agent running in a pane's foreground, from the
// process name tmux reports for it (see ForProcess). A `node` process
// is Claude Code (installed from npm) only when the pane shows it —
// claude.LooksLikeClaude over the pane body and title.
func InForeground(command, pane, title string) (ID, bool) {
	if id, ok := ForProcess(command); ok {
		return id, true
	}
	if command == "node" && claude.LooksLikeClaude(pane, title) {
		return IDClaude, true
	}
	return "", false
}

// ForProcess names the agent a foreground process is, from the process
// name tmux reports for a pane (#{pane_current_command}): an agent's
// Binary() name, or — for Claude Code's native installer, whose binary
// file is named after its version and which macOS reports by that file
// name even when started through the `claude` symlink — a bare version
// number such as `2.1.281`.
//
// Interpreters are not agents: `node` runs Claude Code installed from
// npm, but also Gemini CLI, Codex's npm wrapper and anything else
// written in JavaScript, so a caller has to find other evidence before
// calling a `node` process Claude (see claude.LooksLikeClaude).
func ForProcess(name string) (ID, bool) {
	if name == "" {
		return "", false
	}
	for _, a := range All() {
		if a.Binary() == name {
			return a.ID(), true
		}
	}
	if versionNameRE.MatchString(name) {
		return IDClaude, true
	}
	return "", false
}

// versionNameRE matches a semver-like process name: what the Claude
// Code native installer's binary is called (versions/2.1.281), with an
// optional pre-release or build suffix.
var versionNameRE = regexp.MustCompile(`^\d+\.\d+\.\d+(?:[-+.][0-9A-Za-z.+-]*)?$`)
