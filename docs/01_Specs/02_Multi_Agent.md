# Multi-agent support (Codex, Gemini, …)

Status: **plan locked, in progress**.
Owner: skz.

ccmux today is hardcoded to Claude Code. The goal of this work is to let
users run **Codex** (OpenAI) and **Gemini CLI** (Google) sessions
through the same TUI, dashboard, and cross-device wiring with no
second-class behavior — while keeping Claude as the default and not
breaking existing users.

## Decisions

These are locked. Open new specs if you want to revisit.

1. **Agent identity is per-project, switchable.**
   Each project has a default agent stored in `<project>/.ccmux/agent`
   (one of `claude` / `codex` / `gemini`). Missing file → claude
   (back-compat). The Projects tab gains a key to switch the agent for
   the selected project (writes the sidecar). The new-project form
   gains an agent picker row that defaults to the user's preferred
   agent (config) or to `claude`.

2. **Session name prefix stays `c-`.**
   `c-` already stands for "ccmux", not "claude", in the code. Renaming
   would churn every existing user's tmux session list and add nothing.
   Per-session agent identity comes from the sidecar + a small status
   bar tag.

3. **Notifications are BEL-only for Codex/Gemini in v1.**
   Claude's path through moshi-hook + Claude Code hooks stays
   unchanged. Codex and Gemini get the audible terminal bell on
   needs-input transitions (which iOS clients turn into a generic push)
   until those CLIs grow their own hook systems. Building a generic
   ccmuxd-side push dispatcher is its own multi-week project tracked
   separately.

## Architecture

The current Claude-specific code becomes an `Agent` strategy with
three implementations. The single change point is `internal/agent`:

```go
type ID string  // "claude", "codex", "gemini"

type Agent interface {
    ID() ID
    DisplayName() string
    Binary() string                                          // "claude" / "codex" / "gemini"
    LaunchCmd(continueFlag bool) string                      // tmux New() command string
    ConfigRoot(home string) string                           // ~/.claude, ~/.codex, ~/.gemini
    TranscriptsRoot(home string) string                      // for usage panel
    InitialPrompt(name, description string) string           // first message ccmux sends
    Classify(pane string, lastChange time.Time, idle time.Duration) State
}

func ByID(id ID) Agent          // panics on unknown; ParseID is the safe parser
func ParseID(s string) (ID, bool)
func All() []Agent              // claude, codex, gemini (canonical order)
func AllInstalled(ctx context.Context) []Agent  // intersect with $PATH
func Default() Agent            // claude
```

The existing `internal/claude` keeps its public surface intact (so we
don't touch every caller in one commit). `agent.Claude{}` is a thin
delegator. Subsequent phases migrate callers over and eventually
collapse `internal/claude` into `agent.claude`.

## Surface to refactor

| Layer | Today | After |
|---|---|---|
| Session command | `tmux.New(name, dir, "claude")` | `tmux.New(name, dir, agent.ByID(p.Agent).LaunchCmd(false))` |
| State classifier | `internal/claude.Classify` | `agent.ByID(p.Agent).Classify(...)` |
| Usage panel | `internal/claudeusage` walks `~/.claude/projects/` | `internal/usage` dispatches per-agent walker |
| Config tab | "Claude" TUI screen | "Agents" screen with one sub-pane per installed agent |
| Initial prompt | Hardcoded /init phrasing | `agent.InitialPrompt(name, desc)` |
| Doctor | Checks only `claude` | Checks every agent; at least one required |
| Dashboard rows | Single state column | Adds a small agent badge per row |
| Sidecar | n/a | `<project>/.ccmux/agent` |

## Phases (commit boundaries)

Each phase ships independently with its own test coverage. Stable
master between phases.

1. **Abstraction.** Add `internal/agent` package. Claude impl wraps
   `internal/claude`; Codex + Gemini get classifier stubs that fall
   back to "pane quiet for N seconds = needs_input". Registry +
   AllInstalled. No callers migrate yet. Tests: per-agent identity
   round-trip, ParseID, AllInstalled against a fake PATH.

2. **Sidecar persistence.** `<project>/.ccmux/agent` read by
   `internal/project.Discover`, surfaced as `Project.Agent`. Written
   by `internal/scaffold` on new-project. Switchable via a public
   `project.SetAgent(path, id)` helper. Tests: read/write round-trip,
   missing-file → claude, invalid-content → claude + log, switching.

3. **Picker + switcher in TUI.**
   - `newProjectFormModel` 4th row: agent picker (←/→ cycles).
     Populated from `agent.AllInstalled()`.
   - `newProjectSubmitMsg` carries `Agent agent.ID`.
   - Projects screen detail pane shows current agent and an `a` key
     to switch (cycles + writes sidecar + toasts).
   - Daemon `NewProjectRequest` gains `Agent` field; defaults to claude
     when missing for back-compat.
   - Tests: form cycle, submit payload, switcher writes sidecar,
     daemon honors field, daemon default-on-missing.

4. **Dispatch by agent.**
   - Daemon poll loop reads each session's project agent from the
     sidecar (cached per session) and dispatches `Classify()`.
   - Dashboard usage panel becomes `internal/usage` and dispatches.
   - TUI "Claude" tab becomes "Agents" with sub-panes; selected
     sub-pane reads the relevant config root.
   - Tests: per-agent classifier table-driven; capture sample pane
     content from each agent into `testdata/`.

5. **Doctor + setup wizard.**
   - `ccmux doctor` enumerates installed agents; fails if zero.
   - `ccmux setup` adds an "install agents" step (each is `npm i -g
     ...` for the three CLIs).
   - README + Windows guide updated for multi-agent.

6. **Docs + live verify.**
   - `docs/02_Architecture/04_Agents.md` explains the abstraction.
   - README hero / features updated.
   - End-to-end test on this Mac: create one project per agent,
     verify dashboard rows + transcript usage + cross-device new on
     remote.

## Out of scope (v1)

- Categorized push notifications for Codex/Gemini (per decision 3).
- Cross-agent transcript browsing in the Notes tab.
- Per-agent skills / slash-command picker UI. Claude's stays; Codex/
  Gemini get config-file viewers only until their command surfaces
  stabilize.

## Open questions for v2

- Should the Notes tab show transcript excerpts from all agents in one
  unified timeline?
- Should we offer a "compare agents" mode (one window per agent, same
  prompt, split-pane)?
- Mobile push categorization for non-Claude agents — likely waits on
  Codex/Gemini growing hooks.
