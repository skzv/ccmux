## ADDED Requirements

### Requirement: Read tier always available

When the bridge is enabled, allowlisted chats SHALL be able to inspect state without any extra permission: `/sessions` (local + peers), `/projects`, `/notes [project]`, `/usage`, and `/preview <host:session> [lines]`. These map to existing daemon Client reads (`Sessions`, `Projects`, `Notes`, `Usage`, `Preview`) and MUST NOT mutate anything.

#### Scenario: Preview a pane
- **WHEN** an allowlisted user sends `/preview mini:build 40`
- **THEN** the bridge returns the last 40 pane lines of `build` on `mini`, code-formatted

#### Scenario: Usage summary
- **WHEN** an allowlisted user sends `/usage`
- **THEN** the bridge returns the per-agent token/cost summary from the daemon

### Requirement: Control tier is allowlist-gated

`/new <project> [agent]`, `/kill <host:session>`, and `/send <host:session> <text>` SHALL perform daemon mutations (`NewSession`, `Kill`, `SendKeys`). Because the allowlist already authenticates the chat, these are available to allowlisted users by default, but destructive actions (`/kill`) MUST require an explicit confirmation step (inline button or `/kill … confirm`).

#### Scenario: Spawn a session
- **WHEN** an allowlisted user sends `/new ccmux claude`
- **THEN** the bridge asks the daemon to spawn a Claude session in the `ccmux` project and reports the new session name

#### Scenario: Kill requires confirmation
- **WHEN** an allowlisted user sends `/kill mini:build`
- **THEN** the bridge asks for confirmation and only kills the session after the user confirms

### Requirement: Exec tier is opt-in and off by default

An arbitrary-execution tier — `/run <host:session> <raw-keys-or-command>` and any send of raw control sequences not covered by the curated control tier — SHALL be gated behind `[telegram].allow_exec` (default `false`). When `allow_exec` is false, these commands MUST be refused with a clear message; when true, they are still allowlist-gated.

#### Scenario: Exec refused by default
- **WHEN** `allow_exec = false` and an allowlisted user sends `/run local:build rm -rf build/`
- **THEN** the bridge refuses and explains that the exec tier is disabled, taking no action on the pane

#### Scenario: Exec allowed when opted in
- **WHEN** `allow_exec = true` and an allowlisted user sends `/run local:build make test`
- **THEN** the bridge sends those keys to the pane and confirms

### Requirement: Command menu registration

The bridge SHALL register its commands with Telegram via `setMyCommands` so they autocomplete in the composer's command list. The registered set MUST reflect the active tiers — exec commands are only registered when `allow_exec` is true.

#### Scenario: Menu reflects tiers
- **WHEN** the bridge starts with `allow_exec = false`
- **THEN** `setMyCommands` registers the read and control commands but not `/run`

### Requirement: Inline-query autocomplete of ccmux commands

The bridge SHALL answer Telegram inline queries (typing the bot's @handle plus a partial command) with matching ccmux management commands and their one-line help, so the user can discover ccmux's own commands from the keyboard. This catalog mirrors the ccmux Cobra command tree. Autocomplete of the *agent's own* slash-commands — the primary "available CLI commands" surface the user cares about — is specified separately in `telegram-agent-control`.

#### Scenario: Autocomplete matches a partial command
- **WHEN** an allowlisted user types an inline query `@bot ses`
- **THEN** the bridge returns inline results including `/sessions` with its help text

### Requirement: Inline-keyboard argument navigation

Commands that need a target SHALL offer inline-keyboard choices instead of forcing the user to type exact identifiers. `/preview`, `/kill`, `/send`, and `/notes` without arguments present a button list (sessions or projects) to pick from.

#### Scenario: Pick a session via buttons
- **WHEN** an allowlisted user sends `/preview` with no argument
- **THEN** the bridge replies with an inline keyboard of `host:session` choices, and tapping one previews that session

### Requirement: Output formatting and limit safety

Replies SHALL be formatted for chat and MUST never exceed Telegram's limits. Pane and note output is code-formatted; output longer than a single message is chunked or attached as a document rather than truncated silently; the bridge uses the expanded 32,768-character message limit and collapsible sections where helpful.

#### Scenario: Long output does not break
- **WHEN** a `/preview` or `/notes` result exceeds one message's character limit
- **THEN** the bridge splits it across messages or attaches it as a file, and never sends an over-limit request that Telegram rejects
