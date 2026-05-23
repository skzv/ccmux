## Context

The Agents screen routes between Claude, Codex, and Antigravity sub-tabs. Claude has a richer config model with picker messages and toast feedback; Codex and Antigravity use smaller sub-models that read and write their own config packages directly.

Codex persists reasoning mode as `model_reasoning_effort` in `~/.codex/config.toml`. Antigravity persists reasoning mode as `reasoningEffort` in `~/.gemini/antigravity-cli/settings.json`. The TUI should treat these as per-agent thinking-mode defaults and should not require users to open the config file for normal changes.

## Goals / Non-Goals

**Goals:**
- Make Codex and Antigravity thinking-mode keyboard controls work from their Agents sub-tabs.
- Keep config writes isolated to each agent's existing config package.
- Refresh each sub-tab after a successful write so the rendered state matches the file.
- Add tests that exercise the TUI interaction and persisted config result.

**Non-Goals:**
- Add new Codex or Antigravity config schema fields beyond the existing reasoning-effort fields.
- Change Claude thinking-mode behavior.
- Add CLI commands for thinking-mode changes.
- Validate whether a specific installed Codex or Antigravity CLI version honors the persisted field at runtime.

## Decisions

### Decision 1: Reuse Existing Per-Agent Config Writers

Codex and Antigravity already expose `SetEffortLevel`, `EffectiveEffortLevel`, and `KnownEffortLevels`. The fix should route TUI actions through those helpers instead of adding a new cross-agent abstraction.

Alternative considered: introduce a shared thinking-mode interface for all agents. That would add indirection without solving the immediate bug, and Claude's settings shape differs enough that the abstraction would be mostly adapter code.

### Decision 2: Keep Key Handling Local to the Active Sub-Model

The Agents parent model should continue handling only sub-tab navigation before delegating other keys to the active child model. The Codex and Antigravity sub-models should own their thinking-mode key behavior so the active tab determines which config file is written.

Alternative considered: handle thinking-mode keys in `agentsModel`. That would centralize shortcuts but would make the parent know too much about per-agent settings and error messages.

### Decision 3: Test with Isolated Agent Homes

Tests should set `CODEX_HOME` and `ANTIGRAVITY_HOME` to temporary directories, drive the relevant model key path, and assert both the rendered model state and the persisted config value. This keeps tests deterministic and avoids touching developer config.

Alternative considered: assert only helper functions in `internal/codexconfig` and `internal/antigravityconfig`. Those tests already cover config persistence; the missing risk is the TUI route from key press to helper call.

## Risks / Trade-offs

- TUI behavior may drift from the visible key hints -> Add tests that press the documented key for each sub-tab.
- Config write failures could leave stale on-screen state -> Reload only after successful writes and surface errors inline or via the existing feedback pattern.
- Antigravity CLI support for persisted `reasoningEffort` may vary by version -> Preserve current ccmux behavior of writing the documented/known field and letting the CLI decide how to interpret it.
