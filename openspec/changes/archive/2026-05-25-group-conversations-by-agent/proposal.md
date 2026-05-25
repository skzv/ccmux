## Why

The Conversations tab currently presents every discovered transcript in a
single global timeline. Once ccmux supports Claude, Codex, Cursor, and
Antigravity/Agy, that flat list is difficult to scan: rows from different
agents interleave, empty or unsupported agents are unclear, and keyboard
focus has no agent-level structure.

Users need the same resume/delete workflow, but organized by agent type so
they can quickly inspect one agent's history without losing access to the
others.

## What Changes

- Group the Conversations tab into known agent sections ordered
  Claude -> Codex -> Cursor -> Agy.
- Preserve the existing row actions and detail preview behavior for
  conversations in each section.
- Show an explicit empty state for a known agent section that has no
  visible conversations.
- Hide unknown or future agent IDs rather than adding an `Other` bucket.
- Keep row navigation scoped to the focused agent section. Switch focused
  sections with Tab/Shift+Tab and left/right arrow keys.
- Preserve existing newest-first ordering within each agent section.
- Add focused automated tests and e2e coverage for grouped rendering, empty
  sections, and section switching.
- Do not add new persisted metadata, schema changes, or transcript walkers.

## Capabilities

### New Capabilities
- `conversation-agent-grouping`: The Conversations TUI groups known agent
  conversations into ordered sections with section-scoped keyboard focus.

### Modified Capabilities
- `cuj-e2e-coverage`: The Conversations CUJ e2e coverage must assert the
  grouped agent-section behavior, including empty states and section
  switching.

## Impact

- `internal/tui/conversations.go` — grouping, section focus, rendering,
  navigation, and empty-state behavior.
- `internal/tui/conversations_test.go` and related TUI tests — focused
  coverage for grouped rendering and navigation.
- `internal/e2e/conversations_test.go` or adjacent e2e harness tests —
  grouped Conversations CUJ coverage under isolated `$HOME`/tmux state.
- No new dependencies, persisted config, transcript formats, or public CLI
  contracts are expected.
