// Package agentcatalog resolves the full command catalog for an agent:
// its built-in commands (from internal/agent) plus, for Claude, the
// host user's own commands and skills read from ~/.claude (via
// internal/claudeconfig).
//
// It lives in its own package — not in internal/agent — so the agent
// package stays a pure leaf with no filesystem dependency. The catalog
// is resolved on the host that runs the session, so a peer surfaces its
// own user-authored commands rather than the controlling machine's.
package agentcatalog

import (
	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/claudeconfig"
)

// Resolve returns the command catalog for an agent: built-in commands
// first, then (for Claude) the host's user-defined commands and skills.
// Best-effort about the user layer — a missing or unreadable ~/.claude
// just yields the built-ins.
func Resolve(a agent.Agent) []agent.AgentCommand {
	out := append([]agent.AgentCommand(nil), agent.BuiltinCommands(a)...)
	if a.ID() == agent.IDClaude {
		out = append(out, claudeUserCommands()...)
	}
	return out
}

// ResolveByID is Resolve keyed by agent ID (what the daemon endpoint and
// CLI have on hand). Unknown/empty IDs resolve to the default agent.
func ResolveByID(id agent.ID) []agent.AgentCommand {
	resolved, ok := agent.ParseID(string(id))
	if !ok {
		return Resolve(agent.Default())
	}
	return Resolve(agent.ByID(resolved))
}

// claudeUserCommands reads the host user's slash-command aliases and
// skills from ~/.claude and maps them onto AgentCommand entries. Each
// alias `foo.md` is invoked as `/foo`. Errors are swallowed: the user
// layer is additive, so a read failure must not blank out the built-ins.
func claudeUserCommands() []agent.AgentCommand {
	var out []agent.AgentCommand
	if cmds, err := claudeconfig.ListCommands(); err == nil {
		for _, c := range cmds {
			out = append(out, agent.AgentCommand{
				Name:        "/" + c.Name,
				Description: c.Description,
				Source:      "command",
			})
		}
	}
	if skills, err := claudeconfig.ListSkills(); err == nil {
		for _, s := range skills {
			out = append(out, agent.AgentCommand{
				Name:        "/" + s.Name,
				Description: s.Description,
				Source:      "skill",
			})
		}
	}
	return out
}
