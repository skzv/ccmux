## Context

The Conversations screen already loads a unified
`[]conversations.Conversation`, filters it by project, and preserves the
selected row by conversation ID across refreshes. Rendering and actions are
currently index-based over one filtered slice: up/down moves through the
combined list, Enter resumes the selected row, and `x` arms/deletes the
selected row.

The data model already carries `Conversation.Agent`, so this change can
stay in the TUI layer. Cursor is a supported `agent.ID`, but Cursor
transcript discovery is not wired yet; the Cursor section can still render
as empty until data exists.

## Goals / Non-Goals

**Goals:**
- Render known agent sections in the requested order: Claude, Codex,
  Cursor, Agy.
- Keep newest-first ordering inside each section.
- Keep resume, delete, project filter, headless toggle, loading, and error
  behavior intact.
- Scope up/down navigation to the focused section and switch sections with
  Tab/Shift+Tab and left/right.
- Add focused unit tests and hermetic e2e coverage.

**Non-Goals:**
- Add or change persisted conversation metadata.
- Add Cursor transcript parsing.
- Add an `Other` section for unknown/future agents.
- Change the CLI `list-conversations` output or ordering.

## Decisions

1. Build view groups from the existing filtered list.

   Grouping should be a presentation concern in `internal/tui`. The
   conversations package should continue returning a flat newest-first list
   for CLI callers, dashboard summaries, and existing resume/delete flows.
   The TUI can project that list into known sections at render/update time.

2. Track focused section separately from row cursor.

   A single global cursor conflicts with section-scoped navigation. The TUI
   should keep an active section and a per-section row cursor or equivalent
   selection state. SetList should preserve the selected conversation by ID
   when possible, then clamp within the selected section. If the selected
   section becomes empty, actions should no-op and the detail pane should
   show the section empty state.

3. Use left/right and Tab/Shift+Tab for section switching.

   Up/down already mean row movement. Keeping them scoped within a section
   avoids ambiguous behavior at section boundaries, while left/right and tab
   provide predictable agent-level switching.

4. Hide unknown agent IDs.

   The user explicitly chose not to show an `Other` bucket. Unknown agent
   values should not render in the Conversations TUI. Existing resume/delete
   safety remains in place because actions operate only on rendered rows.

## Risks / Trade-offs

- [Risk] Cursor may render as an empty section until transcript discovery
  exists. -> Mitigation: make the empty state explicit so users understand
  the section is intentionally present.
- [Risk] Per-section cursors can make refresh preservation more complex. ->
  Mitigation: preserve by selected conversation ID first, then clamp within
  the section with unit tests around refresh, filtering, and headless
  toggles.
- [Risk] E2E TUI driving can be brittle. -> Mitigation: keep model tests
  focused for most behavior and add one hermetic e2e flow that asserts the
  real TUI surface and key switching.
