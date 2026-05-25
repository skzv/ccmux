## 1. TUI Grouping Model

- [x] 1.1 Add known Conversations agent section definitions ordered Claude, Codex, Cursor, Agy, mapping Agy to `agent.IDAntigravity`.
- [x] 1.2 Project the existing filtered conversation list into per-agent sections without changing `internal/conversations.All` or CLI list output.
- [x] 1.3 Preserve selected conversation identity across refreshes, filters, and headless toggles, then clamp within the focused section when the row disappears.
- [x] 1.4 Hide conversations with unknown agent IDs and do not render an `Other` section.

## 2. TUI Rendering And Navigation

- [x] 2.1 Render agent section headings and per-section empty states in both wide and narrow Conversations layouts.
- [x] 2.2 Keep Enter resume, `x` delete confirmation, detail preview, project filter, loading, and error behavior working from the focused section.
- [x] 2.3 Scope up/down row movement within the focused section.
- [x] 2.4 Switch focused sections with Tab/Shift+Tab and left/right keys, disarming pending delete on section changes.
- [x] 2.5 Update inline hints to describe section switching without overflowing narrow layouts.

## 3. Tests

- [x] 3.1 Add focused TUI unit tests for grouped rendering, ordering, hidden unknown agents, and empty section states.
- [x] 3.2 Add focused TUI unit tests for section-scoped up/down navigation and Tab/Shift+Tab plus left/right section switching.
- [x] 3.3 Update existing Conversations tests whose assumptions depend on one global cursor/list.
- [x] 3.4 Add hermetic e2e coverage for grouped Conversations rendering, empty agent sections, and section switching.

## 4. Verification

- [x] 4.1 Run `openspec validate group-conversations-by-agent --type change --strict --no-interactive`.
- [x] 4.2 Run focused Go tests for `internal/tui` and `internal/conversations`.
- [x] 4.3 Run `make test`.
- [x] 4.4 Run `make test-e2e` only after confirming the harness remains isolated from live `$HOME`, tmux, sockets, and agent CLIs.
