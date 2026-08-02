# Telegram bridge

The Telegram bridge turns ccmux's existing attention signal into an *actionable* one: the daemon already knows when an agent needs you (it rings the bell and fires APNs/FCM pushes); the bridge lets you answer — approve/deny, drive the agent, read notes — without leaving the chat. It's an optional `ccmuxd` subsystem (`internal/telegram`), off unless `[telegram].enabled` and a `bot_token` are set.

## Why it sits in the daemon

The headline feature is the `needs_input` hook, and that signal is computed inside the poll loop (`decideAttention` in `cmd/ccmuxd/poll.go`). Embedding the bridge there — alongside the bell/APNs/FCM dispatch — reuses that decision directly, keeps one always-on process, and puts the bot token next to the other credentials. The package exposes a `DaemonClient` interface (the subset of `daemon.Client` it needs) and a `BotAPI` interface, so the whole bridge runs against fakes in tests with no network. (Contrast `ccmux-mcp`, which is request/response and stateless, so it's a separate binary.)

## No inbound exposure

The bridge drives the connection with `getUpdates` long polling — it reaches *out* to `api.telegram.org`. No webhook, no listening port, no public URL. That matches ccmux's whole posture (Tailscale, Mosh): reach out, never expose inbound. A network drop is retried with bounded backoff from the last acknowledged update offset. Telegram allows exactly one active poller per token; a second daemon polling the same token gets HTTP 409, which the bridge surfaces as a clear "token already in use" error rather than spinning.

## One bot = one daemon = the tailnet control plane

The bot runs on one daemon (your always-on machine). It addresses sessions as `host:session` and resolves each to the local Unix-socket `daemon.Client` or a `daemon.RemoteClient(peerAddr)` discovered from configured hosts — the same local+remote model the dashboard uses. So a single bot lists, previews, and controls sessions across the whole tailnet; reads fan out across local + reachable peers with per-call deadlines, and an unreachable peer is omitted rather than blocking the reply. This is the answer to "leverage tailnet/ccmuxd?": the bridge is a tailnet client of the existing `:7474` peer API.

## Authorization

The chat-ID allowlist is the real authentication — the token is just transport. Every update (message, button tap, inline query) is checked against `allowed_chat_ids` before any action; unknown chats get nothing. Enrollment is a one-time, single-use, expiring pairing code (`ccmux telegram pair` → `POST /v1/telegram/pair-code`, Unix-socket only → the user sends `/start <code>`), mirroring the existing `pair-token` flow. On top of the allowlist sit three tiers: **read** (always), **control** (spawn/kill/send + approve/deny + agent commands), and **exec** (`/run` arbitrary input, off by default behind `allow_exec`).

## Agent-command catalog (per host)

"Full CLI support" means driving the agent CLI itself. The bot surfaces each session's agent commands as autocomplete + an inline picker. That catalog is per-agent and **resolved on the host that runs the session** — for Claude it merges the built-in slash-commands with that machine's own `~/.claude/commands` and skills (`internal/claudeconfig`). It's exposed by a daemon endpoint, `GET /v1/sessions/{name}/agent-commands`, so a peer session surfaces *its* host's commands. The built-in catalogs live in `internal/agent` (an optional `CommandCatalog()` interface, the same opt-in pattern as `TitleAwareAgent`); the user-command merge lives in `internal/agentcatalog` to keep `internal/agent` free of filesystem I/O. The same data is reachable from `ccmux agents commands`.

## Watch parity

June-2026 Telegram watch apps send text and voice but can't be assumed to tap inline-keyboard buttons. So every action is reachable two ways: an inline button (phone/desktop one-tap) **and** a plain quick-reply (`y`/`n`/`approve`/`deny`) attributed to the alert it answers. A bare quick-reply with several alerts outstanding asks which session instead of guessing.

## Markdown viewing

`/notes` sends the selected file as a document; the in-app browser renders `.md` natively, so it needs no server. The optional `web_viewer` (`cmd/ccmuxd/webviewer.go`) binds a markdown browser to the host's tailnet address — tailnet-only by construction, never `tailscale funnel`, scoped to discovered project vaults and traversal-checked. `tailscale serve` for an HTTPS `*.ts.net` URL is an optional manual upgrade; the built-in listener is tailnet-scoped HTTP.

## Files

- `internal/telegram/` — Bot API client, bridge lifecycle, router, command/agent/notes handlers, notifications.
- `internal/agent/commands.go`, `internal/agentcatalog/` — the command catalog.
- `cmd/ccmuxd/telegram.go`, `webviewer.go`, the `poll.go` hook — daemon wiring.
- `cmd/ccmux/cmd/telegram.go`, `agentcommands.go` — the CLI.
- `internal/tui/network.go` — the Network-screen status line + `T` pairing.

See the [setup guide](../04_Guides/Telegram_Setup.md) for usage.
