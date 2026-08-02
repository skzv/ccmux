## ADDED Requirements

### Requirement: Optional, off by default

The Telegram bridge SHALL be an optional ccmuxd subsystem that is inert unless `[telegram].enabled = true` and a non-empty `bot_token` are present in config. When disabled or unconfigured, ccmuxd MUST run exactly as it does today — no connection attempt, no goroutine, no log noise beyond a single debug line.

#### Scenario: Disabled by default
- **WHEN** ccmuxd starts with no `[telegram]` section (or `enabled = false`)
- **THEN** the bridge subsystem is not started and the daemon's existing behavior is unchanged

#### Scenario: Enabled but missing token
- **WHEN** `[telegram].enabled = true` but `bot_token` is empty
- **THEN** the bridge does not start and ccmuxd logs one warning explaining the token is required

### Requirement: Long-poll connection with no inbound exposure

When enabled, the bridge SHALL connect to the Telegram Bot API exclusively by outbound long polling (`getUpdates`). It MUST NOT open any listening port, register a webhook, or require inbound reachability, so it works behind NAT and on a machine with no public address.

#### Scenario: Connects via long polling
- **WHEN** the bridge starts with a valid token
- **THEN** it issues `getUpdates` requests with a long-poll timeout and processes returned updates, without binding any new socket

#### Scenario: Survives transient network failure
- **WHEN** a `getUpdates` call fails (network drop, timeout, 5xx)
- **THEN** the bridge retries with bounded backoff and resumes from the last acknowledged update offset without dropping or double-processing updates

### Requirement: Single-instance ownership of a bot token

The bridge SHALL detect when another process is already long-polling the same bot token (Telegram returns HTTP 409 Conflict) and surface a clear, actionable error rather than silently spinning. One bot token is owned by exactly one daemon — the tailnet control plane.

#### Scenario: Conflicting poller detected
- **WHEN** `getUpdates` returns 409 Conflict
- **THEN** the bridge stops polling, logs a clear message that the token is already in use by another ccmuxd, and the `status`/doctor surfaces report the conflict

### Requirement: Chat-ID allowlist enforcement

Every incoming update (message, callback query, inline query) SHALL be authorized against `allowed_chat_ids` before any action is taken. Updates from chats not on the allowlist MUST be ignored — no data returned, no command executed — and SHOULD be rate-limited/logged to avoid noise.

#### Scenario: Allowlisted chat is honored
- **WHEN** an enrolled chat sends `/sessions`
- **THEN** the bridge processes the command and replies

#### Scenario: Unknown chat is rejected
- **WHEN** a chat not in `allowed_chat_ids` sends any command or taps any button
- **THEN** the bridge performs no daemon action and does not leak session, project, or note data

### Requirement: One-time pairing enrollment

The bridge SHALL support enrolling a chat into the allowlist via a single-use, expiring pairing code, mirroring the existing Unix-socket `pair-token` model. The code is generated locally (`ccmux telegram pair` or the TUI) and consumed by the first chat that sends `/start <code>`.

#### Scenario: Pairing a new chat
- **WHEN** a chat sends `/start <valid-code>` before the code expires
- **THEN** that chat's ID is added to `allowed_chat_ids`, persisted to config, and the code is invalidated

#### Scenario: Expired or reused code
- **WHEN** a chat sends `/start <code>` for a code that has expired or already been consumed
- **THEN** enrollment is refused and the chat is not added to the allowlist

### Requirement: Tailnet-wide session addressing and fan-out

The bridge SHALL act as a control plane for the whole tailnet from a single host. Sessions are addressed as `host:session` (bare `session` resolves to local). Read and control actions MUST route to the local daemon via the Unix socket or to a peer daemon via the existing `:7474` HTTP API (`RemoteClient(peerAddr)`), reusing the same local+remote model as the dashboard.

#### Scenario: Local and peer sessions enumerated together
- **WHEN** an allowlisted chat sends `/sessions`
- **THEN** the reply lists local sessions and sessions from each reachable configured peer, each labeled with its host

#### Scenario: Action routed to the owning host
- **WHEN** a chat targets `mini:build` (a session on peer `mini`)
- **THEN** the bridge routes the preview/send-keys/kill to `mini`'s daemon over the tailnet API, not the local one

#### Scenario: Unreachable peer degrades gracefully
- **WHEN** a configured peer's daemon does not respond within the per-call deadline
- **THEN** the bridge omits that peer's sessions (or marks it unreachable) and still returns local + other-peer results

### Requirement: Configuration section

A `[telegram]` section SHALL be added to `config.toml`, mirroring the `[apns]`/`[fcm]`/`[openrouter]` credential pattern: `enabled`, `bot_token`, `allowed_chat_ids`, `allow_exec`, and `web_viewer`. Reads and writes MUST round-trip through the existing config load/save without clobbering unrelated fields.

#### Scenario: Config round-trips
- **WHEN** `ccmux telegram pair` adds a chat ID and saves config
- **THEN** reloading config preserves all other sections (theme, hosts, openrouter, etc.) and the new chat ID persists

### Requirement: Token secrecy

The bot token SHALL be treated as a secret: never written to logs, and redacted in `status`/doctor/TUI output (shown as a masked value).

#### Scenario: Token never logged
- **WHEN** the bridge connects, errors, or reports status
- **THEN** the full bot token does not appear in `ccmuxd.log` or any user-facing output

### Requirement: CLI affordance

A `ccmux telegram` Cobra command group SHALL exist with at least `register` (set token + enable), `pair` (mint enrollment code), `status` (connection + allowlist + tiers, token redacted), `test` (send a test message), and `serve` (toggle the optional web viewer). Per the feature-surface policy, this is the CLI half of the feature.

#### Scenario: Status reports health
- **WHEN** the user runs `ccmux telegram status`
- **THEN** it prints whether the bridge is enabled, connected, how many chats are enrolled, which tiers are active, and the token in redacted form

### Requirement: TUI affordance

The TUI SHALL expose Telegram setup and status from the Network screen (a row/section), per the feature-surface policy. The user can enroll a chat (show/copy a pairing code or QR), see connection status, and toggle the bridge without leaving the TUI.

#### Scenario: Enroll from the TUI
- **WHEN** the user opens the Telegram section on the Network screen and starts pairing
- **THEN** the TUI displays a pairing code and reflects "paired" once a chat consumes it

### Requirement: Doctor probe

`ccmux doctor` SHALL include a Telegram check that reports whether the bridge is enabled, the token is accepted by Telegram (`getMe` succeeds), the long poll is healthy, and how many chats are enrolled — without printing the token.

#### Scenario: Doctor flags a bad token
- **WHEN** the configured token is rejected by Telegram (`getMe` 401)
- **THEN** `ccmux doctor` reports the Telegram check as failing with a clear remediation hint
