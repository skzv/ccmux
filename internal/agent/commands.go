package agent

// This file is the per-agent command catalog: the built-in commands
// each agent CLI understands, surfaced over Telegram (and `ccmux agent
// commands`) as autocomplete + an inline picker so you can drive the
// agent from your phone without memorizing its slash-commands.
//
// Built-ins are pure data here (no filesystem reads). User-authored
// commands/skills — Claude's ~/.claude/commands and skills — are merged
// on top by internal/agentcatalog, which is allowed to read the host's
// config dirs. Keeping I/O out of this layer means the agent package
// stays a leaf and the catalog is trivially testable.

// AgentCommand is one command the agent CLI understands.
type AgentCommand struct {
	// Name is sent verbatim to the agent, including any leading slash
	// (e.g. "/model", "/compact").
	Name string
	// Description is a one-line preview shown next to the command.
	Description string
	// TakesArg is true when the command expects an argument
	// (e.g. "/model <name>"), so callers prompt for/offer a value
	// before sending rather than sending it bare.
	TakesArg bool
	// Source groups the command for display: "" or "builtin" for a
	// built-in, "command"/"skill" for a user-authored entry.
	Source string
}

// CommandfulAgent is the optional interface an Agent implements to
// advertise its built-in command catalog. Optional — like
// TitleAwareAgent — so adding catalogs doesn't force every agent to
// change at once. Agents that don't implement it are prompt-only: the
// bridge still accepts free-form prompts for their sessions.
type CommandfulAgent interface {
	Agent
	BuiltinCommands() []AgentCommand
}

// BuiltinCommands returns an agent's built-in command catalog, or nil
// when the agent advertises none. Pure: no I/O.
func BuiltinCommands(a Agent) []AgentCommand {
	if c, ok := a.(CommandfulAgent); ok {
		return c.BuiltinCommands()
	}
	return nil
}

// claudeBuiltinCommands is a curated subset of Claude Code's built-in
// slash-commands — the ones worth driving from a phone. Not exhaustive
// (Claude has more), but the high-value control surface. User-authored
// commands/skills are appended by internal/agentcatalog.
var claudeBuiltinCommands = []AgentCommand{
	{Name: "/model", Description: "Switch the model for this session", TakesArg: true, Source: "builtin"},
	{Name: "/effort", Description: "Set reasoning effort (low/medium/high/max)", TakesArg: true, Source: "builtin"},
	{Name: "/compact", Description: "Summarize and compact the conversation", Source: "builtin"},
	{Name: "/clear", Description: "Clear the conversation and free up context", Source: "builtin"},
	{Name: "/context", Description: "Show what's currently in the context window", Source: "builtin"},
	{Name: "/cost", Description: "Show token usage and cost for this session", Source: "builtin"},
	{Name: "/review", Description: "Review the current changes", Source: "builtin"},
	{Name: "/agents", Description: "Manage subagents", Source: "builtin"},
	{Name: "/mcp", Description: "Manage MCP servers", Source: "builtin"},
	{Name: "/resume", Description: "Resume a previous conversation", Source: "builtin"},
	{Name: "/init", Description: "Initialize project memory (CLAUDE.md)", Source: "builtin"},
	{Name: "/memory", Description: "Edit memory files", Source: "builtin"},
	{Name: "/config", Description: "Open settings", Source: "builtin"},
	{Name: "/status", Description: "Show session and account status", Source: "builtin"},
	{Name: "/export", Description: "Export the conversation", Source: "builtin"},
	{Name: "/help", Description: "List available commands", Source: "builtin"},
}

// BuiltinCommands satisfies CommandfulAgent for Claude.
func (Claude) BuiltinCommands() []AgentCommand { return claudeBuiltinCommands }

// codexBuiltinCommands is a curated subset of Codex CLI slash-commands.
var codexBuiltinCommands = []AgentCommand{
	{Name: "/model", Description: "Choose the model and reasoning effort", TakesArg: true, Source: "builtin"},
	{Name: "/approvals", Description: "Choose what Codex can do without approval", TakesArg: true, Source: "builtin"},
	{Name: "/new", Description: "Start a new chat", Source: "builtin"},
	{Name: "/init", Description: "Create an AGENTS.md with instructions for Codex", Source: "builtin"},
	{Name: "/compact", Description: "Summarize the conversation to save context", Source: "builtin"},
	{Name: "/diff", Description: "Show git diff (including untracked files)", Source: "builtin"},
	{Name: "/status", Description: "Show current session configuration", Source: "builtin"},
	{Name: "/mcp", Description: "List configured MCP tools", Source: "builtin"},
	{Name: "/undo", Description: "Revert the last edits made by Codex", Source: "builtin"},
}

// BuiltinCommands satisfies CommandfulAgent for Codex.
func (Codex) BuiltinCommands() []AgentCommand { return codexBuiltinCommands }
