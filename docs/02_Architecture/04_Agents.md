# Agents (Claude Code, Codex, Antigravity)

ccmux supervises four interactive AI coding agents through a single
strategy interface. This doc is the implementer's view; for the user-
facing story see the README's *Multi-agent* card, and for the original
plan see [`docs/01_Specs/02_Multi_Agent.md`](../01_Specs/02_Multi_Agent.md).

## Where the abstraction lives

```
internal/agent/                       ← strategy interface + 3 impls
├── agent.go        ← Agent interface, ID enum, State enum, registry
├── claude.go       ← Claude{}  (delegates to internal/claude)
├── codex.go        ← Codex{}   (v1 idle-heuristic classifier stub)
├── antigravity.go  ← Antigravity{}  (same)
└── cursor.go       ← Cursor{}  (same)
```

The package exports six things callers reach for:

| Symbol | Purpose |
|---|---|
| `agent.ID` | Canonical id type. Values: `claude`, `codex`, `antigravity`, `cursor`, `gemini` (Gemini CLI is independent of Antigravity). Load-bearing — written verbatim into `.ccmux/agent`. |
| `agent.Agent` | The strategy interface (ID, Binary, LaunchCmd, ConfigRoot, TranscriptsRoot, InitialPrompt, Classify). |
| `agent.All()` | Canonical-order list of every shipped agent. Order matters: pickers default to first installed. |
| `agent.ByID(id)` | Unchecked lookup. Empty string → claude (back-compat). Panics on unknown — callers route user input through ParseID first. |
| `agent.ParseID(s)` | Whitespace-tolerant parser for sidecar / config / CLI flags. |
| `agent.AllInstalled(ctx)` | Subset of All() whose Binary() resolves on $PATH. |
| `agent.Default()` | Locked at Claude — the back-compat default for every legacy project. |

## Per-project agent: `<project>/.ccmux/agent`

Each project carries a one-line sidecar identifying its agent. Read at
inspect-time by `internal/project.ReadAgent(path)`, written by
`internal/project.SetAgent(path, id)` and (transitively) by
`internal/scaffold.PrepareDir` when a new project is created. Missing
file or unparseable contents resolve to `agent.IDClaude` so every
project that existed before this sidecar keeps working.

`SetAgent` writes through `agent.ParseID` so a typo'd caller surfaces
as an error rather than persisting garbage that `ReadAgent` would
silently coerce.

The new-project form's `Agent` field carries the user's pick through to
`scaffold.PrepareDir` (local) or `daemon.NewProjectRequest.Agent`
(remote). `PrepareDir` writes the sidecar only when the caller named a
valid agent: a valid id is persisted, while an empty or unrecognized id
writes nothing and leaves `ReadAgent` to fall back to Claude.

## How the daemon dispatches

`cmd/ccmuxd/poll.go` `pollOnce` walks every tmux session each tick and
resolves what runs in it (`fixedAgent`):

1. The session's `@ccmux_agent` tag naming an agent — every session
   ccmux starts is tagged with the agent it launched (a project session,
   a resumed conversation, a bare agent session). Authoritative, and read
   every tick, so a changed tag is followed.
2. No tag, and named with the `c-` prefix: a session an older ccmux made
   before it tagged them — the project's agent via
   `project.ReadAgent(ts.Path)`, Claude when nothing is recorded.
3. Anything else — tagged `shell` (`ccmux shell`, a bare shell session),
   or a tmux session the user made themselves, wherever it runs — follows
   its **foreground**: while an agent runs in the foreground of its pane
   it is classified as that agent, otherwise it is a plain shell and is
   never classified. So `claude` run by hand in a plain tmux session or a
   ccmux shell is supervised and notifies like any other session, and a
   log tail or a zsh prompt in a project directory is just a shell.

The foreground comes from tmux's `#{pane_current_command}` (read with the
pane list each tick): the OS's name for the pane's foreground process,
mapped by `agent.InForeground` — an agent's `Binary()` name, or a bare
version number such as `2.1.281`, which is how macOS reports Claude Code's
native installer (the `claude` symlink points at `versions/2.1.281`).
Known limits, all because the name is the process's, not what was typed:

- `node` runs Claude Code installed from npm, and Gemini CLI, Codex's npm
  wrapper and other tools; it counts as Claude only when the pane shows
  Claude (its `✳` title, its v2 input box, its banner). Other node-based
  agents run by hand aren't recognised.
- A script shows as its interpreter (`sh`, `bash`), so a wrapper script
  named `claude` that doesn't `exec` the real binary hides it.
- A session ccmux launches runs its agent under `$SHELL -c "agent ||
  fallback"`, whose foreground is that shell — which is why tagged and
  `c-` sessions keep their tag or sidecar instead.

When the agent in a following session's foreground changes (started, or
exited back to the shell), the session starts over as if new: its startup
is not a turn, and a `state_change` event reports the new agent.

An untagged `c-` session's agent is resolved when the daemon first sees it
and cached on the `tracked` struct for the session's lifetime: switching a
project's agent (Projects → `a`) applies to its next session, and doesn't
reclassify the one already running.

```go
newState := agent.ClassifyStateFrom(agent.ByID(t.agentID), prev, pane, title, lastChange, idleNeeds)
```

The pane classified is the agent's own — the session's oldest pane, kept
while it exists — not whichever pane is active, so a shell window opened
next to the agent doesn't hide it. A working-spinner OSC title counts only
while the daemon sees signs of life (tmux keeps the title of a program
that exited), and a pane resize is a redraw, not activity. Likewise
Claude's input box counts as a prompt only while it is the live bottom of
the pane — nothing under it but Claude's own footer (indented lines, or
the `❯` row selected in its background-task list): a Claude killed
mid-turn leaves its last frame on screen with the relaunch error and the
shell prompt printed under it, and that pane is `error`, not waiting for
input, however short the error. The bell, push
and prompt count fire only at the end of a real turn — see `turn`,
`decideAttention` and `agent.ReadTurn`, and "What counts as a turn" in
[05_HTTP_API.md](05_HTTP_API.md). An agent can help the daemon tell its
work from the user's typing by implementing `agent.TurnReader` (Claude
does: its input box keeps typing apart from its output) or with body
`working` rules in its rule file (a working footer).

The `State` enum is shared (`agent.State` mirrors `internal/claude`'s
values exactly) so the bell-trigger comparison, sleep-manager active
boolean, and dashboard row ordering don't have to change.

`listSessions` reports each row's agent on the wire via the
`daemon.SessionState.Agent` field (omitempty for back-compat).

## TUI surface

- **Projects → `n` (new):** form's 4th row is an agent picker
  populated from `agent.AllInstalled()` (or `All()` if nothing is
  installed). Submit carries the chosen `agent.ID` through.
- **Projects → `a` (switch):** on the selected local project, cycles
  through agents in canonical order (claude → codex → antigravity → cursor → claude),
  writes the sidecar, toasts the result. Remote-project switching is
  currently a "not yet supported" toast — adding a daemon endpoint for
  in-place sidecar mutation on remotes is a Phase-4-remaining item.
- **Dashboard rows:** non-default agents get a `[codex]` / `[antigravity]` / `[cursor]`
  tag in muted styling. Claude rows show nothing (the 95% case stays
  visually clean).

## Conversations: hiding automation noise

The Conversations screen and `ccmux list-conversations` enumerate every
on-disk transcript across all three agents. For users who wire Claude
into automation (shell wrappers, the SDK, `claude -p` one-shots) the
list is dominated by single-turn rows that swamp the actual interactive
work — sometimes 20%+ of every transcript on disk.

Each agent records a launch-mode tag on its transcript; `Conversation.Entrypoint`
holds the raw value and `IsHeadless()` is the per-agent predicate that
collapses it to a yes/no:

- **Claude** — `entrypoint` field on every user event in the JSONL
  transcript:
  - `"cli"` — interactive `claude` session in a terminal.
  - `"sdk-cli"` — headless run via `claude -p` or any automation
    wrapper; `"sdk-ts"` / `"sdk-py"` for the TypeScript / Python Agent
    SDKs. Every `sdk-*` value counts as headless.
- **Codex** — `payload.originator` on the first `session_meta` event
  in each rollout:
  - `"codex-tui"` — interactive `codex` session.
  - `"codex_exec"` — headless `codex exec` run.
  - Independently of the originator, a rollout whose
    `payload.source` is `{"subagent": …}` (guardian reviews,
    `thread_spawn` children) is something Codex spawned on its own;
    `Conversation.Subagent` records it and `IsHeadless()` treats it as
    headless.
- **Antigravity** — transcripts are opaque protobuf (encrypted on
  disk), so no signal is available. `Entrypoint` is always empty and
  `IsHeadless()` always returns false; rows are never filtered by this
  toggle.

`conversations.All(Options{ExcludeHeadless: true})` filters headless
rows out after the sort. The package itself stays policy-neutral
(zero-value Options preserves the old "show everything" behavior so
external callers don't silently lose rows); the policy lives in the
TUI and CLI layers, both of which exclude headless rows by default:

- **TUI** — `internal/tui/conversations.go` carries a `showHeadless`
  flag seeded from `config.Conversations.ShowHeadless`. The H keybind
  flips it live, the bottom-of-screen hint surfaces the current state,
  and the detail pane marks headless rows with a mode-specific badge
  (`headless / SDK` for Claude `sdk-cli`, `headless / exec` for Codex
  `codex_exec`) so a user who's opted them in knows what they're
  resuming. The per-project menu (Enter on a project row) honors the
  same default.
- **CLI** — `ccmux list-conversations` and `ccmux project <name>`
  read the same config, plus `list-conversations --include-headless`
  for a one-shot override. `ccmux resume` without an ID also skips
  headless rows when picking the most-recent fallback (a bare
  `ccmux resume` shouldn't drop the user into a `claude -p` or
  `codex exec` replay); resuming by explicit ID always works.

Adding a new agent (or a new headless mode for an existing one) is a
two-line change: parse the launch-mode tag into `Conversation.Entrypoint`
in the agent's `read…Transcript` walker, then add a `case` to
`IsHeadless()`. Every TUI/CLI surface routes through that predicate.

## What's deliberately not abstracted (v1)

- **Usage panel** — `internal/claudeusage` still walks `~/.claude/projects/*/*.jsonl`
  and shows Claude-only stats on the dashboard. Codex's
  `~/.codex/sessions/` and Antigravity's `~/.gemini/antigravity-cli/conversations/`
  formats are different shapes; the walkers need real fixture
  samples that we don't have until users adopt those agents.
  Tracked in spec.
- **Config tab** — the "Claude" TUI screen still manages
  `~/.claude/settings.json`. A future "Agents" screen with per-agent
  sub-panes will need its own design pass; Codex and Antigravity's config
  surfaces aren't stable enough for a useful TUI viewer today.
- **Mobile push categorization** — moshi-hook lives in Claude Code's
  hooks system. Codex/Antigravity get the audible BEL (which iOS clients
  turn into a generic push). A daemon-side notification dispatcher
  that works for all three is its own multi-week project.

## Adding a fourth agent

The shape is intentionally additive. To add, say, `qwen`:

1. Implement `internal/agent/qwen.go` with the seven methods.
2. Add `IDQwen` and the new instance to `agent.All()` (preserve
   canonical order — append to the end).
3. Add the install hint to `cmd/ccmux/cmd/subcommands.go`
   `agentInstallHint` and `internal/setupwizard/wizard.go`
   `installHintFor`.
4. (When the daemon's classifier gets tightened) drop pane-content
   fixtures into `internal/agent/testdata/qwen_*.txt`.

> **Google CLI identities** — `IDGemini` launches `gemini` and `IDAntigravity`
> launches `agy`. Google continues to support Gemini CLI for Code Assist
> Standard/Enterprise and paid API users. Existing `gemini` sidecars retain that
> identity; Antigravity sidecars stay unchanged. `internal/gemini` reads native
> JSON/JSONL chats, applies message updates/rewinds, and resolves project paths
> through `.project_root` markers or `~/.gemini/projects.json`. Antigravity’s
> protobuf conversations remain separate. Settings/credentials are not migrated.

The protocol, sidecar shape, picker UI, doctor flow, and dashboard
badge all pick it up automatically — there is no other place to
register the new agent.
