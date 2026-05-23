## Why

Codex and Antigravity users need the Agents TUI to reliably change their persisted thinking/reasoning mode without editing config files by hand. The current behavior is easy to regress because the key handling and config writes live in per-agent sub-models with little TUI-level coverage.

## What Changes

- Fix the Agents TUI so the Codex and Antigravity sub-tabs can toggle/cycle their thinking mode from the keyboard.
- Ensure the visible state updates immediately after a successful toggle and reports write errors clearly.
- Preserve each agent's existing config file semantics: Codex writes `model_reasoning_effort` in `~/.codex/config.toml`; Antigravity writes `reasoningEffort` in `~/.gemini/antigravity-cli/settings.json`.
- Add focused tests that cover keyboard interaction, config persistence, and error handling for both agents.

## Capabilities

### New Capabilities
- `agent-thinking-mode-config`: TUI controls for viewing and changing per-agent thinking/reasoning mode defaults.

### Modified Capabilities
- None.

## Impact

- Affected code: `internal/tui/agents.go`, `internal/tui/codexconfig.go`, `internal/tui/antigravityconfig.go`, and related TUI tests.
- Affected config writers: `internal/codexconfig` and `internal/antigravityconfig` only if the UI fix reveals a missing helper or unsafe edge case.
- No CLI or daemon API changes.
- No new dependencies expected.
