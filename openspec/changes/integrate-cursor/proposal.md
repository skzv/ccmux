## Why

ccmux currently supports Claude Code, Codex, and Antigravity CLI as
first-class supervised agents. Cursor now ships a terminal agent
(`cursor-agent`) with interactive sessions and resume support, but ccmux
cannot launch it from project creation, agent switching, setup, doctor,
or configured command resolution.

Users who prefer Cursor CLI should be able to use the same ccmux
workflow as the existing agents: pick it per project, have ccmux launch
the right binary in tmux, and keep daemon/service command resolution
deterministic.

## What Changes

- Add Cursor CLI as a fourth supported agent with canonical ID
  `cursor`.
- Register Cursor in the agent registry after Antigravity so existing
  defaults and ordering remain stable.
- Launch new Cursor sessions with `cursor-agent`.
- Resume the latest Cursor conversation for an existing project with
  `cursor-agent resume`, and define explicit conversation resume args
  with `cursor-agent --resume <id>`.
- Include Cursor in configured agent command storage, setup-time
  multi-install selection, daemon service PATH generation, and doctor
  diagnostics.
- Update user-facing docs and tests that currently enumerate the
  supported agent set.
- Do not add Cursor transcript parsing or Cursor usage aggregation in
  this change; those need reliable local transcript format coverage.

## Capabilities

### New Capabilities

- `cursor-agent-support`: ccmux can supervise Cursor CLI as a
  first-class interactive agent.

### Modified Capabilities

- `agent-install-resolution`: Configured agent command resolution must
  include Cursor.

## Impact

- `internal/agent` — Cursor implementation, ID parsing, registry,
  launch/resume command shape, configured command selection.
- `internal/config` — persisted `agents.cursor.command` setting and
  runtime command conversion.
- `internal/setupwizard` — Cursor install hint and command selection.
- `cmd/ccmux/cmd` — `--agent` help/error text and doctor command
  reporting include Cursor.
- `internal/daemonservice` — service PATH includes configured Cursor
  command directories.
- `internal/conversations` — explicit Cursor resume args, while list
  parsing remains out of scope.
- `internal/tui`, docs, and tests — supported agent labels/order and
  user-facing copy updated from three agents to four.
