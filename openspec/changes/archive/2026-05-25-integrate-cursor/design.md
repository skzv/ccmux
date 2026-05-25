## Context

ccmux's multi-agent architecture is already built around
`internal/agent.Agent`. The docs describe adding a fourth agent as an
additive registration: implement the agent, add its ID to the registry,
add setup/install hints, and let the existing project picker, dashboard
badge, doctor flow, and launch paths consume `agent.All()`.

Cursor CLI's documented interactive binary is `cursor-agent`. It starts
interactive sessions as `cursor-agent`, resumes the latest conversation
with `cursor-agent resume`, and resumes a specific chat with
`cursor-agent --resume <chatId>`.

## Goals / Non-Goals

**Goals:**

- Make Cursor selectable anywhere ccmux currently accepts an agent ID.
- Keep Claude as the default and preserve existing agent ordering before
  appending Cursor.
- Honor configured Cursor command paths in the same way as Claude,
  Codex, and Antigravity.
- Pin launch, resume, setup, doctor, and config behavior with tests.

**Non-Goals:**

- Parse Cursor local transcript history for the Conversations list.
- Aggregate Cursor token or usage data.
- Add Cursor-specific config editing screens.
- Change existing Claude, Codex, or Antigravity behavior.

## Decisions

1. Cursor is registered as `agent.IDCursor` with string value `cursor`.
   This matches the product name users see and keeps sidecar files
   readable.

2. Cursor is appended after Antigravity in `agent.All()`. Existing
   defaults and picker ordering stay stable while new installs gain
   Cursor as an additional option.

3. Cursor's default binary is `cursor-agent`, not `cursor`. Official CLI
   docs use `cursor-agent` for install verification, interactive mode,
   and resume commands.

4. Cursor project attach uses latest-conversation resume
   `cursor-agent resume || cursor-agent || zsh || bash || sh`. Unlike
   the existing agents, the latest resume command is a subcommand rather
   than a `--continue` flag. Tests should assert resume semantics without
   requiring every agent to contain `--continue`.

5. Explicit conversation resume args use
   `cursor-agent --resume <conversation-id>`. This pins the documented
   specific-chat dialect even though Cursor transcript discovery remains
   out of scope.

6. Cursor gets the same configured-command path as existing agents:
   `agents.cursor.command` in config, `agent.Commands.Cursor` at runtime,
   setup wizard selection when multiple binaries are on PATH, doctor
   diagnostics, and daemon service PATH inclusion.

## Risks / Trade-offs

- Cursor CLI is beta and may change command flags. Mitigation: keep the
  integration isolated to `internal/agent/cursor.go` and tests so future
  flag changes are narrow.
- Without transcript parsing, Cursor conversations will not appear in
  the unified Conversations list unless another part of ccmux creates a
  row. Mitigation: document this as a non-goal and still define explicit
  resume args for future parser work.
- Existing tests assume all resume launches use `--continue`.
  Mitigation: adjust tests to validate each agent's own latest-resume
  contract instead of a cross-agent flag string.
