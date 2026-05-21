## Why

ccmux is designed to be used from any device on your tailnet — including iPhone terminals like Blink and Moshi that render at 80–110 columns — but several screens clip content, overflow panes, or render blank space at narrow widths because they were built and tested only at monitor widths (160–240 cols). This change audits every screen and enforces width-adaptive rendering so the app is equally usable on a phone and a desktop monitor.

## What Changes

- **Home screen**: verify the single-column layout (hero → sessions → stats → devices → usage) degrades gracefully at 80 cols; clamp minimum heights to avoid empty panes.
- **Sessions screen**: ensure the session list and status columns reflow at narrow widths (drop or truncate low-priority columns before truncating the session name).
- **Projects screen**: ensure the project list and any side-panel detail do not overflow at narrow widths.
- **Notes screen**: ensure the note list, note body viewport, and search bar all respect terminal width; prevent the list from overflowing its column at narrow widths.
- **Settings screen**: two-column key/value rows must not overflow; long values (the config path) are dropped at narrow widths.
- **Agents & Network screens**: collapse the per-agent config detail and the device-detail blocks gracefully at narrow widths.
- **Conversations screen**: ensure the conversation list + detail panel collapse gracefully.
- **Header / tab bar**: narrow-mode path collapses tabs to number-only below the breakpoint; verify the active screen's identity stays visible.
- **Status bar & footer**: curate on narrow — keep daemon status, the battery warning, and `? help`; drop the clock, version chip, and per-screen action hints. Truncation must remove low-priority content first, not blindly cut the line.
- Add a `minWidth` / `maxWidth` test harness that renders each screen at canonical widths (80, 120, 200) and asserts no line exceeds the given width and no mandatory element is missing.

## Capabilities

### New Capabilities

- `adaptive-screen-layout`: Every TUI screen and the persistent chrome (header, status bar, footer) must render correctly at any width in the range [40, ∞). Screens and chrome may adapt their layout (drop columns, collapse side panels, hide reference content) but must never overflow their allotted width or lose the primary content.

### Modified Capabilities

<!-- No existing specs to modify — this is a greenfield spec -->

## Impact

- `internal/tui/app.go` — `homeView`, modal rendering
- `internal/tui/sessions.go` — session list column widths
- `internal/tui/projects.go` — project list + detail pane
- `internal/tui/notes.go` — note list + viewport
- `internal/tui/setup.go`, `internal/tui/settings.go` — form layout
- `internal/tui/conversations.go` — list + detail
- `internal/tui/dashboard.go` — panel helpers (`statsPanel`, `devicesPanel`, `usagePanel`)
- `internal/tui/screens_test.go` — extend with width-sweep tests
- No API changes; no daemon changes; no CLI changes.
