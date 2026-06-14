## Why

ccmux already wakes your phone when an agent needs you — the daemon rings the terminal bell and fires APNs/FCM pushes on the `needs_input` transition. But the notification is a dead end: you still have to open a terminal, Mosh in, and attach before you can answer. The gap between "an agent is blocked on me" and "I unblocked it" is the whole friction of running agents while away from the desk.

Telegram closes that gap. A bot can reach out through any NAT with zero inbound exposure (long-poll `getUpdates`), it's already on every phone and now on the Apple Watch and Wear OS (June 2026 watch apps), and as of the same release its in-app browser renders `.md` natively. That means: get the alert, read the pane, tap **Approve** (or dictate "approve" from your wrist), all without leaving the chat. The same channel doubles as a remote control surface for the whole tailnet — list sessions, preview panes, read project notes, spawn or kill work — hosted on one always-on daemon that already knows how to talk to its peers over the `:7474` API.

## What Changes

- **New optional ccmuxd subsystem** (`internal/telegram`, wired in `cmd/ccmuxd`) that connects a Telegram bot via long polling. No webhook, no open port, works behind NAT. Off unless `[telegram].enabled` and a bot token are set.
- **Chat-ID allowlist** — only enrolled Telegram users can drive the bot. Enrollment is a one-time pairing code, mirroring the existing Unix-socket `pair-token` pattern.
- **Tailnet-wide control plane** — one bot, hosted on the user's always-on daemon, addresses sessions as `host:session` and fans out to peer daemons over the existing `RemoteClient(peerAddr)` HTTP API. Same local+remote model the dashboard already uses.
- **Proactive approve/deny on `needs_input`** — the poll loop's existing `decideAttention` `SendPush` decision gains a Telegram sink. The bot sends the pane tail plus action controls. Because Telegram watch apps send text/voice but cannot be assumed to tap inline buttons, every action works **both** as an inline-keyboard button **and** as a plain quick-reply (`y`/`n`/`approve`/`deny`), so it lands on the watch.
- **ccmux fleet commands** — the bot exposes ccmux's own management commands (`/sessions`, `/projects`, `/notes`, `/usage`, `/new`, `/kill`, `/send`, `/preview`) with a Telegram command menu (`setMyCommands`). Read tier is always on; control tier (spawn/kill/send-keys) is allowlist-gated; an arbitrary-exec tier (`/run`) is opt-in and **off by default**.
- **Agent CLI control with per-agent autocomplete** — this is what "full CLI support" means here: drive the agent running inside a session (Claude, Codex, droid, opencode, …) by sending its *own* slash-commands and prompts from Telegram, with **autocomplete and previews of that agent's available commands**. The catalog is per-agent and sourced from the host that runs the session — Claude merges its built-in slash-commands with the user's `~/.claude/commands` and skills (via `internal/claudeconfig`'s existing `ListCommands`/`ListSkills`), and each other agent contributes a curated catalog. Surfaced as inline-query autocomplete and inline-keyboard pickers (each entry showing the command's description); selecting one sends it to the agent's pane.
- **Markdown viewing** — `/notes` lists a project's vault; selecting a file sends it as a document that Telegram's in-app browser renders natively. An **optional** richer path serves a tailnet `tailscale serve` HTTPS viewer for browsing the whole vault with working links.
- **Smooth setup** — `ccmux telegram` CLI group (`register`, `pair`, `status`, `test`, `serve`) plus a TUI affordance on the Network screen, BotFather-token entry, and a `ccmux doctor` Telegram probe.
- **Docs + website** — setup guide, feature copy, and an architecture note covering the long-poll / no-inbound-exposure model and the tailnet control-plane design.

## Capabilities

### New Capabilities
- `telegram-bridge`: the optional ccmuxd subsystem — long-poll connection lifecycle, the chat-ID allowlist and pairing/enrollment, the tailnet-wide peer fan-out and session addressing, the `[telegram]` config section, the `ccmux telegram` CLI group, the TUI affordance, and the `ccmux doctor` probe.
- `telegram-notifications`: proactive `needs_input` alerts and the approve/deny/quick-reply action handling — built on the existing poll-loop attention decision and seen/dedup machinery, designed to be actionable from the watch (plain message + quick replies, not only tappable buttons).
- `telegram-commands`: the ccmux fleet-management surface over Telegram — read / control / exec tiers for sessions, projects, notes, and usage; the Telegram command menu; and inline-keyboard argument navigation.
- `telegram-agent-control`: driving the agent CLI inside a session — sending its slash-commands and prompts, and the per-agent command catalog (built-ins + the host's user-defined commands/skills for Claude; curated sets for the others) that powers autocomplete and previews. This is "full CLI support for the agents."
- `telegram-notes-viewer`: viewing project markdown over Telegram — `sendDocument` native rendering as the base path, and the optional `tailscale serve` HTTPS web viewer for whole-vault browsing.

### Modified Capabilities
<!-- None. This change is additive: it adds a new consumer of the existing daemon HTTP API, poll-loop attention decision, and tailnet peer fan-out without altering any of their spec-level requirements. -->

## Impact

- **New code:** `internal/telegram/` (minimal Bot API client over `net/http` — `getUpdates`, `sendMessage`, `sendDocument`, `answerCallbackQuery`, `answerInlineQuery`, `setMyCommands`, `editMessageText`; no third-party dependency, consistent with the hand-rolled `internal/daemon` client); subsystem wiring + poll-loop sink in `cmd/ccmuxd/`; `cmd/ccmux/cmd/telegram.go` (Cobra group); a per-agent command catalog (an optional `CommandCatalog()` agent interface mirroring `TitleAwareAgent`, plus curated catalogs per agent) and a `cmd/ccmux/cmd/agent.go` `ccmux agent commands` subcommand; TUI screen/row under `internal/tui/`; optional `tailscale serve` helper in `internal/tailnet`.
- **Touched code:** `internal/config/config.go` (`TelegramConfig` section, mirroring `APNsConfig`/`FCMConfig`); `cmd/ccmuxd/poll.go` (Telegram sink alongside the existing bell/APNs/FCM dispatch in Phase 4); `internal/agent` (optional command-catalog interface) + Claude's catalog reusing `internal/claudeconfig` `ListCommands`/`ListSkills`; the daemon protocol/server + `internal/daemon` Client (a new `agent-commands` endpoint so a session's catalog comes from its owning host); `cmd/ccmux/cmd/` doctor command (per-instance Telegram probe); setup wizard.
- **External dependency:** the Telegram Bot API (`api.telegram.org`), reached outbound only. A bot token is a new credential stored in `config.toml` (treated like the APNs key / OpenRouter key).
- **Security surface:** the bot can drive sessions, so the allowlist is mandatory and the exec tier is opt-in; documented as a deliberate tiered-permission model. The optional web viewer is tailnet-only (`tailscale serve`, never `funnel`) and requires the viewing device to be on the tailnet.
- **No breaking changes.** Everything is gated behind `[telegram].enabled = false` by default; existing notification channels (bell/APNs/FCM) are untouched.
- **Docs:** README feature section, `docs/04_Guides/Telegram_Setup.md`, an architecture note in `docs/02_Architecture/`, and ccmux-website MDX + feature copy.
