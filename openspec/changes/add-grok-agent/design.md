## Context

ccmux's multi-agent architecture is built around the
`internal/agent.Agent` strategy interface. Adding an agent is additive:
implement the interface in a new file, register the ID in `All()` /
`ByID` / `ParseID`, add a `Commands` field + configured-command
substitution, and let the existing project picker, dashboard badge,
setup wizard, doctor flow, and daemon PATH generation consume
`agent.All()`. pi (the fifth agent) followed exactly this shape.

Grok Build is xAI's terminal coding agent, invoked as `grok`, announced
2026-05-25 in early beta (SuperGrok / X Premium+). Facts below are from
the primary docs — `x.ai/cli`, `docs.x.ai/build/overview`, and the
`docs.x.ai/build/cli/headless-scripting` + `/build/enterprise` reference
pages (read 2026-05-28; the marketing page via a real browser, the docs
via curl):

- **Binary / command:** `grok`.
- **Install:** `curl -fsSL https://x.ai/cli/install.sh | bash`, or the
  official npm package `npm install -g @xai-official/grok` (the
  documented alternative that avoids the `x.ai` install host).
- **Resume flags** (from the headless-scripting CLI table):
  - `-c, --continue` — continue the most recent session in the cwd.
  - `-r, --resume <ID>` — resume an existing session.
  - `-s, --session-id <ID>` — create or resume a named headless session.
- **Headless prompt:** `grok -p "<prompt>"`; model select `-m`;
  `--no-auto-update` to skip background update checks in scripts/CI.
- **Config root:** `~/.grok/` with `config.toml` (TOML; `[cli]`,
  `[ui]`, `[skills]`, `[plugins]` sections). Layered config also reads
  `~/.grok/managed_config.toml`, `/etc/grok/managed_config.toml`, and
  `/etc/grok/requirements.toml`.
- **Session history:** `~/.grok/sessions` (verified on grok 0.2.3). Each
  session is a *directory*: `~/.grok/sessions/<url-encoded-cwd>/<uuidv7>/`
  with `chat_history.jsonl` (transcript) + `events.jsonl`,
  `updates.jsonl`, `summary.json`, `system_prompt.txt`, … plus a
  `session_search.sqlite` FTS index at the sessions root. There are
  `grok sessions` (list/search/restore) and `grok export` subcommands.
- **Context files:** `AGENTS.md` is a **first-class, native** Grok
  feature — confirmed in grok 0.2.3's own system prompt (which references
  both `AGENTS.md` and `Claude.md`; Grok is "fully compatible with Claude
  Code"). `AGENTS.md` keeps ccmux's non-Claude agents on one shared file.
- **Auth for headless/remote:** `grok login --device-auth` is the
  documented path for SSH sessions, containers, and headless hosts —
  relevant to ccmux's mosh/Tailscale remote attach.

All of the above is now verified against a real grok 0.2.3 install
(`grok --help`, a headless `grok -p` run, the resulting `~/.grok` tree,
and a tmux launch of `grok --continue` that correctly resumed the cwd
session). The on-disk session format turned out to be a multi-file
directory + SQLite index (not a single JSONL), which is exactly why the
Conversations parser stays deferred — see Decision 8.

## Goals / Non-Goals

**Goals:**

- Make Grok selectable anywhere ccmux accepts an agent ID (CLI `--agent`,
  TUI picker, sidecar `.ccmux/agent`).
- Keep Claude the default and preserve existing agent ordering by
  appending Grok last.
- Honor a configured Grok command path identically to the other agents
  (launch, resume, setup selection, doctor, daemon PATH).
- Pin launch, resume, parse, setup, doctor, and config behavior with
  tests.

**Non-Goals:**

- Parse Grok local session history for the unified Conversations list.
- Aggregate Grok token / usage data for the dashboard.
- Add a Grok-specific config-editing screen.
- Change existing Claude / Codex / Antigravity / Cursor / pi behavior.

## Decisions

1. **ID + identity.** Grok registers as `agent.IDGrok` with string value
   `grok`, display name `Grok`, default binary `grok`. The string is
   load-bearing (written into `.ccmux/agent`), so it matches the product
   command exactly.

2. **Registry ordering.** Append `Grok{}` after `Pi{}` in `All()`.
   Pickers default to the first installed entry, so appending keeps
   Claude-biased defaults and existing ordering stable.

3. **Launch dialect.** New session: `grok`. Existing-project resume:
   `grok --continue || grok || zsh || bash || sh`. Grok takes the same
   `--continue` shape as Claude / Codex / Antigravity / pi, so it flows
   through the existing `launchCmdWithBinary` default branch (no special
   case like Cursor's `resume` subcommand).

4. **Explicit resume args.** `ResumeArgs(IDGrok, id, …)` →
   `["grok", "--resume", <id>]`, honoring configured-command
   substitution. Empty ID returns nil, matching every other agent.

5. **Configured command.** Add `Commands.Grok`, an `agents.grok.command`
   config field, and `grok` cases in `commandOverride` /
   `configuredBinary`. This automatically pulls Grok into
   `AllAvailable`, setup multi-install selection, and daemon service
   PATH generation — those iterate `All()` and read the override.

6. **ConfigRoot / TranscriptsRoot.** `ConfigRoot(home)` = `~/.grok`
   (holds `config.toml`). `TranscriptsRoot(home)` = `~/.grok/sessions`
   (verified) — the root above the per-cwd/`<uuid>` session directories.
   No Conversations parser is wired to it in this change (Decision 8),
   but it's a correct anchor for the config tab and a future parser.

7. **Initial prompt + context file.** Reuse the `AGENTS.md`-centered
   bootstrap shared by Codex / Antigravity / Cursor / pi. This is the
   right file (not a guess): the x.ai/cli landing page documents
   `AGENTS.md` as a native, out-of-the-box Grok convention, so the same
   cross-agent file works across whichever agent a project uses. (Grok's
   Claude-Code compatibility means `CLAUDE.md` is also read, but
   `AGENTS.md` keeps ccmux's non-Claude agents on one shared file.)

8. **Conversations parsing deferred.** Like Cursor, Grok ships
   launch + resume first. Now that the layout is verified, the reason to
   defer is concrete: grok stores each session as a *multi-file directory*
   (`<cwd>/<uuid>/chat_history.jsonl` + siblings) plus a SQLite FTS index,
   which is a different (heavier) shape than the single-JSONL-per-session
   the existing walkers assume. A future `ListGrok` should prefer shelling
   `grok sessions` / `grok export` over re-implementing that on-disk
   format. Explicit resume args are already defined so the parser only
   needs to emit `Conversation{Agent: IDGrok, ID: …}` rows.

9. **Classify heuristic.** Use the same conservative quiet-pane
   heuristic as the other non-Claude agents (`StateUnknown` on empty
   pane, `StateNeedsInput` past the idle threshold, else `StateActive`)
   until real Grok pane fixtures exist.

## Risks / Trade-offs

- **Beta flag churn.** Grok Build is early beta; flags may change.
  *Mitigation:* keep the integration isolated to `internal/agent/grok.go`
  + tests so a flag change is a one-file edit.
- **Heavier session format than peers.** Verified: grok sessions are
  multi-file dirs + a SQLite index, not single JSONL files. *Mitigation:*
  Conversations parsing is a non-goal here; when it lands, prefer the
  `grok sessions` / `grok export` CLI over re-implementing the format.
- **Grok absent from Conversations list.** Without a parser, Grok
  sessions won't appear in the unified list. *Mitigation:* documented as
  a non-goal; resume args are defined so the follow-up is purely additive.
