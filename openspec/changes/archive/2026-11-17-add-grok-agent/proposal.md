## Why

ccmux supervises Claude Code, Codex, Antigravity, Cursor, and pi as
first-class interactive agents. xAI shipped **Grok Build** — a terminal
coding agent invoked as `grok` — in early beta on 2026-05-25, but ccmux
can't launch, resume, or configure it. Users on SuperGrok / X Premium
Plus who want Grok in the same workflow have no path today.

## What Changes

- Add Grok Build CLI as a sixth supported agent with canonical ID
  `grok`, display name `Grok`, default binary `grok`.
- Register Grok in `agent.All()` after pi so existing defaults and
  ordering stay stable (Claude remains the default).
- Launch new Grok sessions with `grok`; resume the latest session for an
  existing project with `grok --continue` (falling back to `grok`, then
  an interactive shell).
- Define explicit conversation resume args as `grok --resume <id>`
  (Grok documents `-r, --resume <ID>`).
- Include Grok in configured agent command storage
  (`agents.grok.command`), setup-time multi-install selection, daemon
  service PATH generation, and `ccmux doctor` diagnostics.
- Use the cross-agent `AGENTS.md` bootstrap for Grok's initial prompt,
  matching the other non-Claude agents.
- Update user-facing surfaces (CLI `--agent` help/errors, TUI agent
  picker/labels), README, and website docs that enumerate the agent set.
- **Out of scope:** Grok transcript parsing for the Conversations list
  and Grok token/usage aggregation. Verified on grok 0.2.3: sessions are
  stored as multi-file directories (`~/.grok/sessions/<cwd>/<uuid>/…`)
  plus a SQLite FTS index — a heavier shape than the single-JSONL the
  existing walkers assume, so a parser is deferred (and should prefer the
  `grok sessions`/`grok export` CLI). This mirrors how Cursor shipped
  (launch/resume first, parsing later).

## Capabilities

### New Capabilities

- `grok-agent-support`: ccmux can supervise Grok Build CLI as a
  first-class interactive agent — registration, launch/resume command
  dialect, configured-command resolution, setup selection, doctor
  reporting, and user-facing surfaces.

### Modified Capabilities

- None. The `agent-install-resolution` requirements are written
  agent-agnostically (`agents.<agent>.command`, "any supported agent"),
  so they already cover Grok without a normative change; Grok's
  install-resolution behavior is pinned with concrete scenarios in
  `grok-agent-support`.

## Impact

- `internal/agent` — `grok.go` implementation, `ID`/`ParseID`/`ByID`
  registration, `All()` ordering, launch/resume command shape,
  `Commands.Grok` + configured-command substitution, `ResumeArgs`.
- `internal/config` — persisted `agents.grok.command` setting and its
  conversion into `agent.Commands`.
- `internal/setupwizard` — Grok install hint and multi-binary command
  selection.
- `internal/daemonservice` — service PATH includes configured Grok
  command directories.
- `cmd/ccmux/cmd` — `--agent` help/error text and `ccmux doctor` agent
  diagnostics include Grok.
- `internal/conversations` — explicit Grok resume args wired; on-disk
  list parsing remains out of scope.
- `internal/tui`, README, website docs, and tests — supported-agent
  labels/order and copy updated from five agents to six.
