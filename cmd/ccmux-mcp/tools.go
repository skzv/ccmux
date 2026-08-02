package main

import (
	"context"
	"encoding/json"

	"github.com/skzv/ccmux/internal/daemon"
)

// DaemonClient is the subset of internal/daemon.Client that ccmux-mcp
// uses. Defining it as an interface keeps tests independent of the
// real daemon — handler_test.go drops in a fake that records calls
// and returns canned data.
type DaemonClient interface {
	Sessions(ctx context.Context) ([]daemon.SessionState, error)
	Preview(ctx context.Context, name string, lines int) (daemon.PreviewResponse, error)
	Projects(ctx context.Context) ([]daemon.ProjectInfo, error)
	Conversations(ctx context.Context) ([]daemon.Conversation, error)
	Usage(ctx context.Context) (daemon.AgentUsage, error)
	Peers(ctx context.Context) ([]daemon.PeerInfo, error)
	Notes(ctx context.Context, project string) ([]daemon.NoteEntry, error)
	NoteContent(ctx context.Context, project, rel string) (daemon.NoteContent, error)
	SearchNotes(ctx context.Context, project, query string) ([]daemon.SearchHit, error)
	Health(ctx context.Context) (daemon.HealthInfo, error)

	NewSession(ctx context.Context, req daemon.NewSessionRequest) (daemon.SessionState, error)
	NewBareSession(ctx context.Context, req daemon.NewBareSessionRequest) (daemon.NewBareSessionResponse, error)
	SendKeys(ctx context.Context, name, keys string) error
	Kill(ctx context.Context, name string) error
}

// Tool describes one MCP tool — its name (registry key), one-line
// description shown to the agent, JSON Schema for its arguments, and
// the handler that runs it.
type Tool struct {
	Description string
	InputSchema map[string]any
	Handler     ToolHandler
}

// ToolHandler is the unified shape every tool handler implements.
// Receives raw JSON args (validate inside) and returns a serializable
// result. Return an *invalidArgs error for bad arguments — the
// dispatcher converts it to a JSON-RPC -32602.
type ToolHandler func(ctx context.Context, raw json.RawMessage) (any, error)

// buildTools is the tool registry. The mutating tools are only added
// when Server.allowMutate is true.
func buildTools(s *Server) map[string]Tool {
	t := map[string]Tool{
		"list_sessions": {
			Description: "List every ccmux/tmux session known to the local ccmuxd, with state (active / idle / needs_input / error), agent, project, host, and last-change time. Use this to answer 'what's running' across all your projects and machines.",
			InputSchema: emptySchema(),
			Handler:     wrap(s.handleListSessions),
		},
		"read_pane": {
			Description: "Return the last N lines of a session's active tmux pane (default 40, max 500). Lets you inspect a session's current screen content without attaching. Useful for 'what is this session doing right now?'",
			InputSchema: object(map[string]any{
				"name": stringSchema("tmux session name (from list_sessions[].name)", true),
				"lines": numberSchema(
					"how many trailing lines to capture (default 40, max 500)",
					false,
				),
			}, []string{"name"}),
			Handler: wrap(s.handleReadPane),
		},
		"list_projects": {
			Description: "List every project ccmux knows about (every directory under the configured projects root with CLAUDE.md or a .git directory). Includes agent assignment, last-modified time, and whether it has a docs/ tree or CLAUDE.md.",
			InputSchema: emptySchema(),
			Handler:     wrap(s.handleListProjects),
		},
		"list_conversations": {
			Description: "List past coding-agent conversations (Claude / Codex / Antigravity / Cursor / Pi / Grok) sorted by recency. Each entry has the resumable agent ID, the project, the working directory, and a preview of the first user message.",
			InputSchema: emptySchema(),
			Handler:     wrap(s.handleListConversations),
		},
		"get_usage": {
			Description: "Get aggregated per-agent token + estimated cost over a rolling window. Returns per-agent prompt counts, input/output tokens, and a USD estimate from published API rates.",
			InputSchema: emptySchema(),
			Handler:     wrap(s.handleGetUsage),
		},
		"list_machines": {
			Description: "List every tailnet peer the daemon can see, with whether each runs ccmuxd. Use this to discover other machines that can host sessions.",
			InputSchema: emptySchema(),
			Handler:     wrap(s.handleListMachines),
		},
		"list_notes": {
			Description: "List every markdown note in the named project's tree, grouped by directory. Returns per-note relative path, display label, and mtime.",
			InputSchema: object(map[string]any{
				"project": stringSchema("project name (from list_projects[].name)", true),
			}, []string{"project"}),
			Handler: wrap(s.handleListNotes),
		},
		"read_note": {
			Description: "Read one markdown note's full contents.",
			InputSchema: object(map[string]any{
				"project": stringSchema("project name (from list_projects[].name)", true),
				"path":    stringSchema("project-relative slash-separated path (from list_notes[].rel)", true),
			}, []string{"project", "path"}),
			Handler: wrap(s.handleReadNote),
		},
		"search_notes": {
			Description: "Ripgrep-style search across a project's notes tree. Returns matching file path, line number, and the matching line trimmed.",
			InputSchema: object(map[string]any{
				"project": stringSchema("project name (from list_projects[].name)", true),
				"query":   stringSchema("search pattern (literal substring by default; ripgrep regex syntax)", true),
			}, []string{"project", "query"}),
			Handler: wrap(s.handleSearchNotes),
		},
		"get_daemon_health": {
			Description: "Return ccmuxd health: hostname, version, session count, and sleep-prevention mode. Useful as a first probe to confirm the daemon is alive.",
			InputSchema: emptySchema(),
			Handler:     wrap(s.handleGetHealth),
		},
	}
	if s.allowMutate {
		t["spawn_session"] = Tool{
			Description: "Spawn a new agent session in an existing project. Equivalent to opening the Projects tab in the TUI and hitting 'n'. Returns the new tmux session name. Requires ccmux-mcp --allow-mutate.",
			InputSchema: object(map[string]any{
				"project":  stringSchema("project name (from list_projects[].name)", true),
				"path":     stringSchema("working directory; defaults to the project's path on the daemon's host", false),
				"agent":    stringSchema("agent to launch (claude | codex | antigravity | cursor | pi | grok). Empty = project's recorded default.", false),
				"continue": boolSchema("resume the latest session in this project instead of starting fresh", false),
				"name":     stringSchema("explicit tmux session name. Empty = derived from project path.", false),
			}, []string{"project"}),
			Handler: wrap(s.handleSpawnSession),
		}
		t["spawn_bare_session"] = Tool{
			Description: "Spawn a bare (project-less) agent session. Just a tmux session running the picked agent (or $SHELL) at the given path. Requires ccmux-mcp --allow-mutate.",
			InputSchema: object(map[string]any{
				"name":  stringSchema("explicit tmux session name. Empty = daemon picks one.", false),
				"path":  stringSchema("working directory on the daemon's host. Empty = $HOME on the daemon.", false),
				"agent": stringSchema("agent to launch, or 'shell' for no agent. Empty = daemon default.", false),
			}, nil),
			Handler: wrap(s.handleSpawnBareSession),
		}
		t["send_keys"] = Tool{
			Description: "Send a literal keystroke string into a session's active pane. tmux interprets named keys (Enter, C-c, Escape, …). Use with care — this is the same as typing into the user's session. Requires ccmux-mcp --allow-mutate.",
			InputSchema: object(map[string]any{
				"name": stringSchema("tmux session name (from list_sessions[].name)", true),
				"keys": stringSchema("keystroke string (e.g. 'hello' or 'C-c'). Use 'Enter' for newline.", true),
			}, []string{"name", "keys"}),
			Handler: wrap(s.handleSendKeys),
		}
		t["kill_session"] = Tool{
			Description: "Terminate a tmux session. The agent process inside is SIGKILLed by tmux. Requires ccmux-mcp --allow-mutate.",
			InputSchema: object(map[string]any{
				"name": stringSchema("tmux session name (from list_sessions[].name)", true),
			}, []string{"name"}),
			Handler: wrap(s.handleKillSession),
		}
	}
	return t
}

// wrap converts a strongly-typed handler-like function into the
// generic ToolHandler shape. Kept thin so handlers.go can write
// idiomatic Go signatures without every one re-doing JSON plumbing.
func wrap(fn ToolHandler) ToolHandler { return fn }

// JSON Schema helpers — building blocks for tool argument schemas.
// These are deliberately small; MCP clients only need name/type/
// description/required for sensible UIs, not the full Draft 2020-12
// spec surface.

func emptySchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           map[string]any{},
		"additionalProperties": false,
	}
}

func object(properties map[string]any, required []string) map[string]any {
	s := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func stringSchema(description string, _ bool) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func numberSchema(description string, _ bool) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}

func boolSchema(description string, _ bool) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}
