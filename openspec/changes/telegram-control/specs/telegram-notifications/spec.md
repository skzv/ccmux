## ADDED Requirements

### Requirement: Proactive needs-input alert

The bridge SHALL send a Telegram alert when a session transitions to `needs_input` and the existing poll-loop attention logic decides a push is warranted (`decideAttention(...).SendPush`). The alert MUST identify the session as `host:session`, name the project and agent, and include the tail of the pane so the user has context without attaching.

#### Scenario: Alert on needs-input transition
- **WHEN** the poll loop computes `SendPush == true` for a session entering `needs_input`
- **THEN** the bridge sends a message to every allowlisted chat containing `host:session`, project, agent, and the last N pane lines

#### Scenario: No alert when push is not warranted
- **WHEN** a session changes state but `decideAttention` returns `SendPush == false` (e.g. a client is attached)
- **THEN** the bridge sends no Telegram alert for that transition

### Requirement: Watch-compatible actionable controls

Each alert SHALL be actionable from a smartwatch. Because June-2026 Telegram watch apps send text and voice but cannot be assumed to tap inline-keyboard buttons, every action MUST be available **both** as an inline-keyboard button (phone/desktop) **and** as a plain text quick-reply (`y`/`n`/`approve`/`deny`, case-insensitive) that the watch can send or dictate.

#### Scenario: Approve via inline button
- **WHEN** an allowlisted user taps the "Approve" button on an alert
- **THEN** the bridge resolves the button's session from the callback data and approves it

#### Scenario: Approve via quick-reply text
- **WHEN** an allowlisted user replies `approve` (or `y`) to an outstanding alert
- **THEN** the bridge approves the referenced session — no button tap required

### Requirement: Approve/deny maps to agent keystrokes

Approve and deny SHALL be delivered as send-keys to the target pane via the owning daemon (local or peer). Approve sends the agent's accept keystroke; deny sends the decline keystroke. After acting, the bridge MUST answer the callback query and edit the original message to reflect the outcome (so the buttons can't be tapped twice into a stale action).

#### Scenario: Approve sends accept keys and confirms
- **WHEN** the user approves an alert for `mini:build`
- **THEN** the bridge sends the accept keystroke to `mini`'s pane, answers the callback, and edits the message to show "Approved"

#### Scenario: Deny sends decline keys
- **WHEN** the user denies an alert
- **THEN** the bridge sends the decline keystroke to the target pane and edits the message to show "Denied"

### Requirement: Dedup and seen integration

Telegram alerts SHALL respect the daemon's existing `seen` machinery and not spam. While an alert for a given session-change is outstanding and unanswered, the bridge MUST NOT send a second alert for the same unchanged blocked state. Marking the session seen elsewhere (e.g. attaching) suppresses pending alerts.

#### Scenario: No duplicate alert for the same block
- **WHEN** a session remains blocked across multiple poll cycles without changing
- **THEN** the bridge sends exactly one alert, not one per cycle

#### Scenario: Attaching suppresses the alert action
- **WHEN** the user attaches to the session (seen becomes true) before responding in Telegram
- **THEN** the alert is marked resolved/stale and tapping its buttons reports that the session was already handled

### Requirement: Multi-session disambiguation

When more than one session is blocked at once, the bridge MUST keep actions unambiguous: inline-keyboard callbacks carry the target `host:session` in their data, and a bare quick-reply (`y`/`n`) applies to the alert it is sent in reply to. If a quick-reply cannot be attributed to a single outstanding alert, the bridge MUST ask the user to specify the target rather than guess.

#### Scenario: Button targets its own session
- **WHEN** two alerts are outstanding and the user taps "Approve" on the second
- **THEN** only the second alert's session is approved

#### Scenario: Ambiguous quick-reply is clarified
- **WHEN** the user sends a bare `y` not attributable to a single outstanding alert
- **THEN** the bridge replies asking which `host:session` to approve instead of acting on an arbitrary one

### Requirement: Mutable independently of the command surface

Telegram alerting SHALL be controllable without disabling the bridge. A user can mute proactive alerts (so the bot is read/command-only) and unmute, via config and a `ccmux telegram`/TUI toggle, without tearing down the connection.

#### Scenario: Mute keeps commands working
- **WHEN** the user mutes Telegram notifications
- **THEN** no `needs_input` alerts are sent, but `/sessions`, `/preview`, and other commands still respond
