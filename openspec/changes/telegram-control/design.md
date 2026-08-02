## Context

ccmuxd already does the hard part of attention: the poll loop captures every pane, classifies state, and on a `needs_input` transition computes an `attentionDecision` (`cmd/ccmuxd/poll.go`) that today drives the terminal bell and the APNs/FCM push dispatchers in Phase 4. It also already exposes the whole session/project/notes/usage surface over an HTTP API on the Unix socket and (optionally) a tailnet-bound `:7474` listener, and it can talk to peer daemons via `daemon.RemoteClient(addr)` — that's how the dashboard shows local + remote sessions together.

What's missing is a way to *act* from the notification. Today the push is a dead end. Telegram is a uniquely good fit to close it: a bot reaches Telegram outbound only (`getUpdates` long polling), so it needs no open port and works behind NAT; it's already on the user's phone, and as of June 2026 on the Apple Watch / Wear OS; and its in-app browser now renders `.md` natively. This change adds an optional Telegram bridge that turns the existing attention signal into an actionable approve/deny and layers a full read/control surface on top, hosted on one always-on daemon that fans out across the tailnet.

This design builds on the four capability specs (`telegram-bridge`, `telegram-notifications`, `telegram-commands`, `telegram-notes-viewer`) for requirements; it describes how to satisfy them.

## Goals / Non-Goals

**Goals:**
- Turn a `needs_input` push into a one-tap (or one-word) approve/deny that works on phone *and* watch.
- A single bot, hosted on the user's always-on daemon, that controls the whole tailnet by reaching peer daemons over the existing `:7474` API.
- A ccmux fleet-management surface (sessions/projects/notes/usage/new/kill), discoverable via Telegram's command menu.
- **Drive the agent CLI inside a session** — send its own slash-commands and prompts from Telegram, with autocomplete and previews of that agent's available commands (Claude/Codex/droid/opencode/…), sourced per-host so the catalog reflects that machine's user-defined commands. This is what "full CLI support" means here.
- View project markdown in Telegram with zero extra infrastructure (native `.md` rendering), plus an optional richer tailnet web viewer.
- Setup that is a couple of commands: paste a BotFather token, scan/enter a pairing code, done.
- Zero impact when off; no new inbound exposure when on.
- Both a CLI and a TUI affordance, with tests, per the repo's feature-surface and testing policies.

**Non-Goals:**
- Replacing Mosh/attach. The bridge is for triage and quick actions, not a full terminal. Deep interactive work still attaches.
- A public/Funnel-exposed service. The optional web viewer is tailnet-only (`tailscale serve`), never `tailscale funnel`.
- Per-user, per-host RBAC. v1 has one allowlist of trusted chats; finer-grained scoping is future work.
- Speech-to-text of our own. "Dictate approve" relies on the watch/phone producing text we then parse; we add no STT.
- Hosting the bot on multiple daemons for one token (Telegram forbids it — see Decisions).
- A full in-TUI command palette backed by the per-agent catalog. The catalog ships here (consumed by Telegram plus a `ccmux agent commands` CLI for discovery/scripting); a TUI palette on top of the same data is a natural follow-on, out of scope.

## Decisions

### Embed the bridge in ccmuxd as an optional subsystem (not a separate binary)
The bridge lives in `internal/telegram` and is started by `cmd/ccmuxd` when configured, as a goroutine alongside the poll loop — the same place APNs/FCM dispatch already lives. The poll loop calls a thin `bridge.Notify(host, session, paneTail, changeID)` when `decideAttention(...).SendPush` is true and Telegram is enabled+unmuted.
- **Why:** the headline feature *is* the `needs_input` hook, and that signal is computed inside the poll loop. Embedding reuses it directly, keeps one always-on process, gives direct local session access, and puts the token next to the other credentials. The package boundary (`internal/telegram` with a `DaemonClient` interface, exactly like `cmd/ccmux-mcp`) keeps it testable and means it could be re-hosted later without a rewrite.
- **Alternatives:** a separate `ccmux-telegram` binary (like `ccmux-mcp`) connecting over the Client. Rejected: it would have to re-derive `needs_input` transitions by diffing `Sessions()` polls, runs a second process to supervise, and can't cleanly reuse the existing seen/dedup decision. The MCP server is request/response and stateless; the bridge is long-lived and event-driven, so it belongs with the event source.

### Long polling, not webhooks
The bridge drives the connection with `getUpdates` (long-poll timeout, offset acknowledgement).
- **Why:** zero inbound exposure. No port, no public HTTPS endpoint, no domain, works behind NAT — which is the whole point of a tool you run on a laptop or home Mac mini. It matches ccmux's posture of reaching *out* (Tailscale, Mosh) rather than exposing inbound.
- **Alternative:** webhooks. Rejected for the control channel: they require a public HTTPS endpoint Telegram can reach, which a NAT'd machine doesn't have.

### One bot token = one daemon = the tailnet control plane
The bot runs on exactly one daemon (the user's always-on node). It addresses sessions as `host:session`, resolves `host` to the local Unix-socket Client or a `RemoteClient(peerAddr)` discovered via `Peers()`, and fans out reads across local + reachable peers.
- **Why:** Telegram allows only one active `getUpdates` poller per token (a second returns 409 Conflict). One bot is also less setup and one credential. And the daemon already knows how to reach its peers, so a single bot naturally controls the whole tailnet — directly answering "can we leverage tailnet/ccmuxd?" Yes: the bridge *is* a tailnet client of the existing peer API.
- **Alternatives:** one bot per machine (N tokens). Rejected: N× setup, N× tokens, and the user would have to know which chat maps to which host. Or: bot only sees its own host's sessions. Rejected: throws away the multi-machine value the tailnet already provides.

### Markdown: `sendDocument` (native render) as base, tailnet-bound viewer as opt-in
The common path for `/notes` is to send the selected file as a document; Telegram's in-app browser renders `.md` natively now, so it opens formatted with no server. The optional `web_viewer` starts a markdown browser **bound to the host's tailnet address** (the same `100.x` the daemon API uses, on `tailnet_port+1`) for whole-vault navigation with working links; the bot offers a plain URL button to it.
- **Why:** the base path needs no HTTPS, no domain, no BotFather Mini-App registration, and no tailnet membership on the viewing device — it just works. The viewer is strictly better for browsing many linked notes; binding it to the tailnet interface makes it tailnet-only without mutating the user's global `tailscale serve` config, and the in-app browser renders the served `.md`/HTML over plain tailnet HTTP. It's opt-in because it needs the viewing device on the tailnet.
- **Alternative considered:** front it with `tailscale serve` for an HTTPS `*.ts.net` URL. Rejected as the default because `tailscale serve` mutates global tailscale state (a side effect a session daemon shouldn't impose); it remains an optional manual upgrade a user can layer on. A full Mini App was also rejected for the base case — heavier setup for what a sent document already does.
- **Security:** tailnet-only by binding (never `tailscale funnel`); scoped to discovered project vaults; path-traversal and non-`.md` rejected.

### Approve/deny works as inline buttons *and* text quick-replies (watch parity)
Every alert carries an inline keyboard (Approve/Deny/Preview) **and** accepts plain text replies (`y`/`n`/`approve`/`deny`, case-insensitive). Callback data encodes the target `host:session` and change id; a bare quick-reply is attributed to the alert it replies to.
- **Why:** June-2026 Telegram watch apps send text and voice and use smart/canned replies, but tapping inline-keyboard buttons on the watch can't be assumed. To honor "approve/deny via the watch," the action must be reachable by sending a word. Buttons stay for the phone/desktop one-tap path.
- **Alternative:** buttons only. Rejected — fails the explicit watch requirement.

### Hand-rolled minimal Bot API client (no third-party dependency)
`internal/telegram` implements just the methods we use over `net/http`: `getMe`, `getUpdates`, `sendMessage`, `sendDocument`, `editMessageText`, `answerCallbackQuery`, `answerInlineQuery`, `setMyCommands`.
- **Why:** the surface is small and the project already hand-rolls its HTTP clients (`internal/daemon`). Avoids pulling an opinionated bot framework and its transitive deps. A `transport` seam (an interface or an injectable `httpDoer`) makes the whole bridge testable against a fake Telegram with no network.
- **Alternative:** a Go Telegram library. Faster to start but adds dependency weight and a framework's structure; we can adopt one later if the surface grows. Documented as a deliberate, reversible choice.

### Tiered permissions, least-privilege by default
- **Read tier** (always on when enabled): `/sessions`, `/projects`, `/notes`, `/usage`, `/preview`.
- **Control tier** (allowlist-gated; the allowlist *is* the authentication): `/new`, `/kill`, `/send`, the approve/deny actions, **and agent-CLI control — sending an agent's own slash-commands and prompts, chosen from its catalog**. Destructive ops (`/kill`, exec) require a confirm step.
- **Exec tier** (`allow_exec`, default `false`): `/run` arbitrary keys/commands. Refused with a clear message when off.
- **Why:** the bot can drive real machines, so the dangerous surface is opt-in while the valuable surface (triage, approve/deny, and structured agent control) is available without opening an arbitrary shell. Agent-CLI control sits in the control tier rather than exec because it's a *curated* surface — the agent's own known commands plus prompts (the agent's normal input), not raw shell. The allowlist is mandatory and does the real authorization; the token is just transport.

### Per-agent command catalog, sourced from the owning host
"Autocomplete of available CLI commands" means the *agent's* commands, and those are per-agent and per-host. Each `Agent` may implement an optional `CommandCatalog()` interface (the same opt-in pattern as `TitleAwareAgent`, so the 15 existing agents don't all change at once). Claude's catalog merges its built-in slash-commands with the host user's own commands and skills via `internal/claudeconfig`'s existing `ListCommands()`/`ListSkills()`; the other agents return a curated built-in set. The catalog is exposed by a new daemon endpoint (`GET /v1/sessions/{name}/agent-commands`) with a matching `Client.AgentCommands(ctx, name)`, so a session's catalog is computed on the host that actually runs it. The bridge consumes it for inline-query autocomplete and an inline-keyboard picker (each entry showing its description as the preview), then sends the chosen command via `SendKeys`. The same data is reachable from `ccmux agent commands` for CLI discovery and scripting.
- **Why:** custom Claude commands/skills live in `~/.claude/` on whichever machine runs the session, and the agent differs per session — so the catalog can't be a single hardcoded list in the bridge. Sourcing it from the owning host makes a peer session surface *that* host's commands, and reusing `claudeconfig` means user-authored commands appear automatically.
- **Alternative:** hardcode one catalog (or only Claude's built-ins) in the bridge. Rejected: it would hide user/host-specific commands and show the wrong set for non-Claude or peer sessions. The optional interface keeps per-agent catalogs co-located with each agent strategy.

### Outstanding-alert state is in-memory and ephemeral
The bridge keeps a small map: Telegram message id / callback token → (host, session, change id, status). Used to attribute quick-replies, edit messages on resolution, and mark alerts stale when the session is seen elsewhere. Lost on restart — a restart just means old buttons answer "this was already handled."
- **Why:** no persistence complexity for transient UI state; correctness degrades gracefully.

## Risks / Trade-offs

- **Pane tails and note contents traverse Telegram's servers.** → Everything is opt-in and off by default; the alert pane tail is line-capped (and optionally redactable) before sending; the tailnet web viewer keeps full content on the tailnet. Documented as an explicit privacy trade-off so users decide per-machine. Don't enable on panes with secrets you wouldn't paste into a chat.
- **Bot token leak.** → The token alone cannot act: the chat-ID allowlist gates every update, so a leaked token lets someone *message* the bot but not *drive* it. Token is redacted in all output, stored like the APNs/OpenRouter keys, and rotatable via `ccmux telegram register`.
- **Two daemons, one token.** → Telegram returns 409; the bridge detects it, stops, and reports a clear "token already owned by another ccmuxd" error in logs/status/doctor.
- **Exec tier is genuinely dangerous.** → Off by default, allowlist-gated even when on, with confirmation for destructive actions; refusal message makes the gate discoverable.
- **Watch can't tap inline buttons.** → Mirrored quick-replies make every action a word away; bare `y`/`n` is attributed to the replied-to alert, and ambiguous replies are clarified rather than guessed.
- **Peer fan-out partial failure / latency.** → Per-call deadlines (reuse the Client's context budget); unreachable peers are omitted or marked, never block local results.
- **Web viewer turning into an open file server.** → Off by default; `tailscale serve` (tailnet-only) never `funnel`; scoped to the same project vaults the bridge exposes; path-traversal rejected.
- **Telegram message limits / long output.** → Chunk across messages or attach as a document; use the 32,768-char limit and collapsible sections; never send an over-limit request.
- **Inline-query autocomplete needs inline mode enabled in BotFather.** → Setup guides enabling it; if off, the command menu (`setMyCommands`) still provides discovery, so the feature degrades, not breaks.

## Migration Plan

Purely additive and gated. Rollout order (also the tasks order): land `internal/telegram` (client + bridge + fakes + tests) → add `TelegramConfig` (additive, round-trip-preserving) → wire the subsystem and the poll-loop `Notify` sink in `cmd/ccmuxd` → `ccmux telegram` CLI group → TUI Network-screen affordance → `ccmux doctor` probe → docs + website. Each lands with tests that fail before and pass after.

Rollback is trivial: the bridge only runs when `[telegram].enabled = true` with a token; setting `enabled = false` (or never configuring it) returns ccmuxd to current behavior. No data migration, no breaking config change — the new fields are additive and preserved by the existing load/save round-trip, verified by a config test.

## Open Questions

- **Pane-tail egress cap/redaction.** Default line cap before a pane tail leaves the machine, and whether to offer a regex redaction hook. Proposed: cap to N lines (config), redaction deferred to a follow-up.
- **Confirmation granularity.** Which control actions require an explicit confirm. Proposed: only `/kill` and the exec tier; approve/deny do not (they're the whole point of being fast).
- **Multi-user allowlist semantics.** Several enrolled chats all get the same tier in v1. Per-chat tier/host scoping is out of scope but the config shape (`allowed_chat_ids` as a list) leaves room.
- **Voice approve.** Relying on watch/phone dictation to produce text we parse — confirm that's sufficient and we add no STT.
- **BotFather inline mode.** Whether setup should hard-require enabling inline mode or treat autocomplete as a best-effort enhancement over the always-present command menu. Proposed: best-effort, documented.
- **Command-catalog freshness.** How aggressively to (re)read each host's user-defined Claude commands/skills — per request vs cached with an mtime check. Proposed: cache per host with a short TTL / mtime invalidation, since `ListCommands`/`ListSkills` already key off mtime.
- **Curated catalog coverage for non-Claude agents.** Which agents ship a meaningful built-in command set in v1 vs an empty/minimal catalog (still allowing free-form prompts). Proposed: populate the agents with documented slash-command sets first, leave the rest prompt-only until their command surfaces are confirmed.
