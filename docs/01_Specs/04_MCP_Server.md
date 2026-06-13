# ccmux — MCP Server

> `ccmux-mcp` is an [MCP](https://modelcontextprotocol.io) server that exposes ccmux to coding agents. Speaks JSON-RPC 2.0 over stdio per the spec; proxies tool calls to the local `ccmuxd` (or a tailnet peer's daemon via `CCMUX_HOST`).

## Why

An agent running inside one ccmux session can see and act on every other session — its own siblings on the same machine, sessions on the Mac mini, sessions on any tailnet peer. The handful of tools below cover the operations that the dashboard, CLI, and mobile clients already do; the MCP layer is just the agent-facing protocol.

This is the differentiator: nobody else can do it, because nobody else has both a project model and a multi-host daemon. An agent in session A on machine 1 can orchestrate sessions B and C on machine 2 through one protocol, with the same security model the daemon already enforces (Unix socket = same-user; tailnet HTTP = tailnet identity).

## Wire-up

Claude Code (`~/.claude/settings.json`):

```jsonc
{
  "mcpServers": {
    "ccmux": { "command": "ccmux-mcp" }
  }
}
```

Read-only by default. To expose the mutating tools, pass `--allow-mutate`:

```jsonc
{
  "mcpServers": {
    "ccmux": { "command": "ccmux-mcp", "args": ["--allow-mutate"] }
  }
}
```

Codex, Cursor, and any other MCP-aware client follow the same shape — point at the `ccmux-mcp` binary, optionally pass `--allow-mutate`.

### Setup helpers

Don't want to hand-edit `settings.json`? Two paths do it for you:

- **Setup wizard.** `ccmux setup` now includes a "ccmux-mcp registration (Claude Code)" step that detects Claude Code on PATH and offers to register the entry — with a follow-up prompt for `--allow-mutate`. Idempotent; re-running detects the existing registration and reports the mode.
- **CLI.** `ccmux mcp register [--allow-mutate]` does the same thing without the wizard chrome. `ccmux mcp status` reports whether ccmux is registered and in which mode.

Both write a timestamped backup to `~/.claude/backups/` before mutating, preserve any other `mcpServers` entries you already have, and round-trip unknown JSON keys verbatim via the `internal/claudeconfig` round-trip discipline.

## Target a remote daemon

```bash
CCMUX_HOST=mini.tail-xxxxx.ts.net:7474 ccmux-mcp
# or
ccmux-mcp --host mini.tail-xxxxx.ts.net:7474
```

When `--host` is set, the server talks to that ccmuxd over HTTP on the tailnet instead of the local Unix socket. Useful when the agent runs on the laptop but should orchestrate sessions on the Mac mini.

## Tools

### Read-only (always exposed)

| Tool                | Returns                                                                                                              |
| ------------------- | -------------------------------------------------------------------------------------------------------------------- |
| `list_sessions`     | every session known to the daemon, with state / agent / project / host / last-change                                 |
| `read_pane`         | last N lines of a session's active tmux pane (default 40, max 500)                                                   |
| `list_projects`     | every project under the configured root, with agent assignment and a few metadata bits                               |
| `list_conversations`| past Claude / Codex / Antigravity / Cursor / Pi / Grok transcripts, sorted by recency, with the resumable agent ID   |
| `get_usage`         | aggregated per-agent token + cost over a rolling window                                                              |
| `list_machines`     | tailnet peers, whether each runs ccmuxd                                                                              |
| `list_notes`        | every markdown note in a project, grouped by directory                                                               |
| `read_note`         | one note's full contents                                                                                             |
| `search_notes`      | ripgrep search across a project's notes tree                                                                         |
| `get_daemon_health` | first-probe surface: ccmuxd hostname, version, session count, sleep mode                                             |

### Mutating (only with `--allow-mutate`)

| Tool                | Effect                                                                                                       |
| ------------------- | ------------------------------------------------------------------------------------------------------------ |
| `spawn_session`     | start a new agent session in an existing project (same shape as the TUI's Projects → `n` flow)               |
| `spawn_bare_session`| start a project-less session (just `$SHELL` or an agent at a path)                                           |
| `send_keys`         | type a literal keystroke string into a session's pane (tmux interprets `Enter`, `C-c`, etc.)                 |
| `kill_session`      | terminate a tmux session                                                                                     |

Tools are listed in alphabetical order via `tools/list`. Mutating tools are not just guarded — they're absent from the tools list entirely when `--allow-mutate` is off, so an agent can't surface them in its own UI even if a user toggled the flag in a config file.

## Security model

- **Transport.** stdio is the only transport. The Unix socket the daemon listens on is filesystem-permission scoped to the user; tailnet HTTP requires being on the tailnet. ccmux-mcp inherits whichever the daemon is.
- **Mutation.** Off by default. The `--allow-mutate` flag is the only way to expose `spawn_session` / `send_keys` / `kill_session`. There is no per-tool override.
- **Bound input.** `read_pane` caps the requested line count at 500 so a buggy or malicious agent can't drag the daemon down by requesting full scrollback every call.
- **Per-call deadline.** Every handler runs under a 30-second context — well above legitimate work, well below "hangs the stdio loop."

## Wire shapes

### `initialize` (handshake)

Request:

```json
{ "jsonrpc": "2.0", "id": 1, "method": "initialize", "params": { "protocolVersion": "2025-06-18" } }
```

Response:

```jsonc
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "protocolVersion": "2025-06-18",
    "capabilities": { "tools": {} },
    "serverInfo": { "name": "ccmux-mcp", "version": "v0.1.27" },
    "instructions": "ccmux exposes its session/project/agent state through these tools…"
  }
}
```

### `tools/list`

Returns the tools the server advertises, alphabetized. Each entry carries an `inputSchema` (a small JSON Schema) so the client can render help and validate arguments.

### `tools/call`

Request:

```json
{
  "jsonrpc": "2.0",
  "id": 7,
  "method": "tools/call",
  "params": { "name": "list_sessions", "arguments": {} }
}
```

Result: one `content` block of type `text` whose body is the JSON-encoded tool output (pretty-printed). Tool-execution failures are returned as `isError: true` on the result, NOT as a JSON-RPC error — agents distinguish "I called the wrong tool" (`error.code = -32601 / -32602`) from "the tool ran but failed" (`result.isError = true`).

## Testing

- `cmd/ccmux-mcp/server_test.go` — protocol-level tests: handshake, ping, parse errors, notifications, unknown method, mutate gating, argument validation, tools list ordering.
- `cmd/ccmux-mcp/handlers_test.go` — per-tool tests against a `fakeClient` that records every daemon call. Confirms argument forwarding, nil-safety, and `lines` capping for `read_pane`.
- `internal/e2e/mcp_test.go` (`//go:build integration`) — spawns the real `ccmux-mcp` binary against a real ccmuxd in the isolated `TMUX_TMPDIR` sandbox. Runs `initialize` → `tools/list` → `tools/call list_sessions` end-to-end and confirms a live tmux session appears in the result. Mutate-gate-off path is pinned end-to-end too.

Run with: `make test` (units) and `make test-e2e` (the live end-to-end).

## Why JSON-RPC over stdio (and not HTTP)?

stdio is the MCP transport contract and the simplest deployment shape: every MCP-aware client knows how to spawn a child and read/write its stdio. No port, no auth, no `localhost` reachability question. The daemon already handles HTTP for tailnet peers; ccmux-mcp doesn't need a second copy of that surface — it just proxies to the daemon's existing HTTP API through the same `internal/daemon.Client` the TUI uses.

## Roadmap

- **Live event stream.** Surface `/v1/events` as MCP `resources/subscribe` so an agent can react to "session X went needs_input" without polling.
- **Prompt library.** Expose the project's notes folder as MCP `prompts/list` — pre-built prompts an agent can pick from.
- **Per-tool permissions.** A middle ground between read-only and `--allow-mutate`: allow `spawn_session` but not `send_keys`, etc. The implementation hook is the existing `s.allowMutate` flag; extending it to a set is a small refactor when the use case shows up.
