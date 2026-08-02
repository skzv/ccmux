## 1. Telegram Bot API client (`internal/telegram`)

- [x] 1.1 Create `internal/telegram/` package with a minimal Bot API client over `net/http`, behind an injectable `httpDoer`/transport seam for hermetic tests.
- [x] 1.2 Implement the methods the bridge needs: `getMe`, `getUpdates` (long-poll + offset ack), `sendMessage`, `sendDocument`, `editMessageText`, `answerCallbackQuery`, `answerInlineQuery`, `setMyCommands`.
- [x] 1.3 Model the update/payload types actually used (Update, Message, CallbackQuery, InlineQuery, InlineKeyboardMarkup, etc.) — no broader than needed.
- [x] 1.4 Handle API errors and the 409 Conflict (another poller owns the token) as a distinct, surfaceable error; never include the token in error strings.
- [x] 1.5 Provide a `fakeTransport` test helper and unit-test each method against recorded/synthetic responses (success, 401 bad token, 409 conflict, 5xx → retry).

## 2. Configuration

- [x] 2.1 Add `TelegramConfig{Enabled, BotToken, AllowedChatIDs []int64, AllowExec, WebViewer, MuteAlerts, PaneTailLines}` to `internal/config/config.go`, mirroring the `APNsConfig`/`FCMConfig` pattern, and add it to the root `Config` struct.
- [x] 2.2 Wire sensible defaults in `Defaults()` (enabled=false, allow_exec=false, web_viewer=false, a default pane-tail line cap) and add a helper to read the allowlist as a set.
- [x] 2.3 Test that adding a chat ID + token round-trips through `config.Load`/`config.Save` without clobbering other sections (theme, hosts, openrouter).

## 3. Tailnet control-plane routing

- [x] 3.1 Add a `host:session` addressing helper (parse/format; bare name → local) and a resolver that maps a host to the local `daemon.LocalClient()` or a cached `daemon.RemoteClient(peerAddr)` discovered via `Peers()`.
- [x] 3.2 Implement fan-out reads (sessions, optionally projects/usage) across local + reachable peers with per-call deadlines; unreachable peers are omitted/marked, never blocking.
- [x] 3.3 Define a `DaemonClient` interface (the subset of `daemon.Client` the bridge uses, including the agent-commands read from §7) so the bridge is testable with a fake, exactly like `cmd/ccmux-mcp`.
- [x] 3.4 Unit-test addressing parse/format, host resolution, and fan-out with a fake multi-host client (including a dead peer).

## 4. Daemon subsystem + notification sink

- [x] 4.1 Add a `Bridge` type in `internal/telegram` with a lifecycle (`Start(ctx)`/stop): runs the long-poll loop with bounded backoff and resumes from the last offset.
- [x] 4.2 Start the bridge from `cmd/ccmuxd` only when `[telegram].enabled` + token present; otherwise log one debug line and do nothing.
- [x] 4.3 Expose `bridge.Notify(host, session, paneTail, changeID)` and call it from poll-loop Phase 4 when a session enters `needs_input` unattended, plus `MarkSeen` on resolution — alongside the existing bell/APNs/FCM dispatch, off the critical path.
- [x] 4.4 Cap the pane tail to `PaneTailLines` before sending (`lastLines`); never send the token (scrubbed in the client; secrecy unit-tested).
- [x] 4.5 White-box test the poll-loop hook: extracted the alert/resolve decision into the pure `telegramSignals(prev, next, attached)` and table-tested every transition (entry/attended/resolve/no-op).

## 5. Approve/deny + watch-compatible actions

- [x] 5.1 Implement alert rendering: `host:session`, project, agent, capped pane tail, with an inline keyboard (Approve / Deny / Preview) whose callback data carries `host:session` + change id.
- [x] 5.2 Accept plain text quick-replies (`y`/`n`/`approve`/`deny`, case-insensitive) attributed to the replied-to alert; map approve→accept keys and deny→decline keys via `SendKeys` on the owning host.
- [x] 5.3 On action, answer the callback and `editMessageText` to show the outcome ("Approved"/"Denied"); make buttons idempotent (second tap reports already-handled).
- [x] 5.4 Maintain the in-memory outstanding-alert map (message id/callback → host, session, change id, status); mark alerts stale when the session is seen elsewhere; respect dedup so one block → one alert.
- [x] 5.5 Multi-session disambiguation: bare quick-reply not attributable to a single outstanding alert asks which `host:session` instead of guessing.
- [x] 5.6 Tests: approve via button, approve via text, deny, dedup (no duplicate per cycle), stale-after-seen, two-outstanding targeting, ambiguous-reply clarification — all against the fake transport + fake daemon client.

## 6. ccmux fleet command surface

- [x] 6.1 Read tier: `/sessions`, `/projects`, `/notes`, `/usage`, `/preview <host:session> [lines]` → daemon reads; code-format pane/preview output.
- [x] 6.2 Control tier: `/new <project> [agent]`, `/kill <host:session>` (with confirm), `/send <host:session> <text>` → daemon mutations.
- [x] 6.3 Exec tier gated by `allow_exec`: `/run …` refused-with-explanation when off, executed (still allowlist-gated) when on.
- [x] 6.4 Register commands via `setMyCommands`, reflecting active tiers (omit `/run` when `allow_exec=false`).
- [x] 6.5 Inline-query autocomplete of ccmux's own commands (Cobra tree) — distinct from the agent-command autocomplete in §7; degrade gracefully if inline mode is off.
- [x] 6.6 Inline-keyboard argument navigation: `/preview`, `/kill`, `/agent` with no arg present a button list of sessions to pick.
- [x] 6.7 Output-limit safety: attach as a document when over one message's limit (`sendCodeOrDocument`); clamp plain messages; never send an over-limit request.
- [x] 6.8 Tests for each tier (including exec-refused-by-default and confirm-before-kill), menu contents per tier, ccmux-command autocomplete matching, and over-limit attach.

## 7. Agent CLI control + per-agent command catalog

- [x] 7.1 Add an optional `CommandCatalog()` agent interface in `internal/agent` (same opt-in pattern as `TitleAwareAgent`) returning `[]AgentCommand{Name, Description, TakesArg}`; agents that don't implement it are prompt-only.
- [x] 7.2 Claude catalog: built-in slash-commands merged with the host user's commands and skills via `internal/claudeconfig` `ListCommands()`/`ListSkills()` (composition in `internal/agentcatalog` to keep `internal/agent` I/O-free).
- [x] 7.3 Curated built-in catalogs for the other agents (codex …) as data co-located with the agent strategy; the rest stay prompt-only until confirmed.
- [x] 7.4 Daemon endpoint `GET /v1/sessions/{name}/agent-commands` returning the session's agent catalog from the owning host; add `Client.AgentCommands(ctx, name)` and include it in the bridge `DaemonClient` interface (§3.3).
- [x] 7.5 Bridge: surface the catalog as inline-query autocomplete for the targeted session and as an inline-keyboard command picker that shows each command's description (the preview).
- [x] 7.6 Send a chosen catalog command to the session via `SendKeys` (command text + Enter); argument-taking commands prompt for/offer the value first.
- [x] 7.7 Free-form prompt send: route typed text to the session's agent (+Enter) as a prompt; classify as control tier (available without `allow_exec`).
- [x] 7.8 `ccmux agents commands [--agent <id>] [--session <name>]` CLI (`cmd/ccmux/cmd/agentcommands.go`) printing the resolved catalog (feature-surface + scripting).
- [x] 7.9 Tests: Claude catalog merges a custom command; Codex catalog ≠ Claude; autocomplete matching; send-command and send-prompt land the right keys; control-tier reachable with `allow_exec=false`. (Peer-host catalog reflection is covered by the §13 two-daemon e2e — pending.)

## 8. Notes viewing

- [x] 8.1 `/notes [project]` lists the vault via the daemon `Notes` read; selecting a file sends it as a `.md` document (native in-app render).
- [x] 8.2 `/notes <project> <query>` runs daemon `SearchNotes` and returns hits that open the containing file.
- [x] 8.3 Path-safety guard: reject traversal/out-of-vault paths; read only `.md` within discovered projects (`safeNotePath`, plus the daemon's own validation).
- [x] 8.4 Tests: list, send-document, search, and traversal-rejection against a temp vault.

## 9. Optional tailnet web viewer

- [x] 9.1 Behind `web_viewer=true`, serve a markdown browser for the project vaults (`cmd/ccmuxd/webviewer.go`); reuse the notes read path; scope strictly to discovered vaults.
- [x] 9.2 Bind it to the host's tailnet address (tailnet-only by construction; never `tailscale funnel`; no global tailscale-state mutation). `tailscale serve` HTTPS noted as an optional manual upgrade — spec/design updated to match.
- [x] 9.3 Offer a URL button from `/notes` when the viewer is enabled; default off (no listener, send-document only).
- [x] 9.4 Tests: viewer disabled → no URL/listener; enabled → vault-scoped serving + traversal/non-`.md` rejection; unknown project 404.

## 10. CLI: `ccmux telegram`

- [x] 10.1 Add `cmd/ccmux/cmd/telegram.go` Cobra group following the `mcp.go` pattern.
- [x] 10.2 `register` (set token + enable), `pair` (mint a single-use code via the daemon + print the /start instruction), `status` (enabled/enrolled count/tiers, token redacted), `test` (send a test message), `serve` (toggle web viewer).
- [x] 10.3 Implement the one-time pairing code store (`pairingStore`) + daemon `pair-code` endpoint + `/start <code>` consumption that appends to `allowed_chat_ids` and persists; codes expire and are single-use.
- [x] 10.4 Tests: `redactToken`, pairing store (valid/expired/reused), enrollment, and `agents commands` JSON. (register/test RunE are thin wrappers over config + the validated client; covered manually / by §13 e2e.)

## 11. TUI affordance

- [x] 11.1 Add a Telegram status line to the Network screen (on/off + paired count) and a `T` key that mints a pairing code shown as a toast — colors/spacing from tokens (design-system lint green).
- [x] 11.2 Wire it through the App: `SetTelegram` from config at startup; pairing routes to the daemon `pair-code` endpoint and persists via the bridge enrollment path.
- [x] 11.3 Tests for the status-line states + the help hint; network goldens regenerated (status line + `T` hint).

## 12. Doctor + setup wizard

- [x] 12.1 Add a Telegram check to `ccmux doctor`: enabled?, `getMe` accepts the token?, enrolled count, exec-tier warning — token never printed; clear remediation on failure (incl. 401 and 409 conflict).
- [x] 12.2 Add a Telegram step to the setup wizard (`stepTelegram`: token entry via masked input + validation + save) consistent with the existing MCP step; non-interactive runs print the CLI fallback.
- [x] 12.3 Tests for the doctor probe (not-configured, good token, 401, 409) via an injectable validator; asserts the token is never printed.

## 13. Integration / e2e

- [x] 13.1 Hermetic integration test driving `Notify` through the **real** `telegram.Client` (real HTTP marshaling) against a mock Bot API: needs-input → alert with approve/deny keyboard reaches the configured chat. (Runs in plain `go test`, not just CI.)
- [x] 13.2 `host:session` routing + per-host fan-out covered by the `Router` unit tests (incl. a dead peer); the agent-commands endpoint is exercised over a real daemon HTTP server below. (A full two-real-tmux-daemon harness test remains an optional follow-up under `//go:build integration`.)
- [x] 13.3 Agent-control e2e: fetch a session's catalog through a real daemon HTTP server + real `daemon.Client` (`AgentCommands`), asserting the catalog crosses the wire.
- [x] 13.4 Verification gate: `go test ./...`, `make lint`, and `GOOS=linux`/`GOOS=windows` cross-compile — run green (see final check).

## 14. Docs + website

- [x] 14.1 README: added the ✈️ Telegram control feature to the full feature list (approve/deny, agent CLI control, tailnet control plane, notes).
- [x] 14.2 `docs/04_Guides/Telegram_Setup.md`: BotFather token, pairing, tiers, agent-command autocomplete, optional web viewer, watch usage, privacy note, config reference; linked from the `CLAUDE.md` docs map.
- [x] 14.3 `docs/02_Architecture/06_Telegram_Bridge.md`: long-poll / no-inbound model, one-bot tailnet control plane, per-host agent-command catalog, watch parity; linked from the docs map.
- [x] 14.4 ccmux-website (PR-only): added `/docs/telegram` MDX page + docs-index entry + landing mobile-section link + strategy "approve from your watch" angle; opened as a PR (skzv/ccmux-website#13). Built clean; Sasha's voice, agent list open-ended, no competitor refs.
