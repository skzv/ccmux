## Why

The Home screen's two-column wide layout puts the "Hello" hero inside the right column and stacks the session detail underneath the list on the left. The hero reads better as a full-width welcome banner across the top, and the session detail ("Session Info") belongs beside the list it describes. On a phone the greeting banner is pure decoration that costs scarce vertical rows — it should disappear entirely.

## What Changes

- **Wide screen (≥ 120 cols):** the Hello hero becomes a full-width banner across the top. Below it, two columns: the **sessions list** on the left, and on the right the **Session Info** detail pane on top followed by the **Session summary / Devices / Claude usage** tiles stacked beneath it.
- **Small screen (< 120 cols):** the Hello hero is removed entirely (not just curated — gone). The screen is a single column: sessions (list + condensed detail) → Session summary → Devices → Claude usage.
- `homeView` composes the layout directly from the sessions list/detail renderers and the dashboard panels, rather than delegating the whole sessions block (list + detail bundled) to one call.

## Capabilities

### New Capabilities

<!-- none — this refines an existing capability -->

### Modified Capabilities

- `adaptive-screen-layout`: the "Home screen layout adapts to width" requirement changes — the hero moves to a full-width top banner, the session detail moves to the right column, and the hero is omitted (not curated) below the breakpoint.

## Impact

- `internal/tui/app.go` — `homeView` layout composition
- `internal/tui/sessions.go` — how the list and detail are exposed to `homeView` (currently bundled by `View`)
- `internal/tui/screens_test.go` — `TestHomeView_NarrowSingleColumn` / `TestHomeView_WideTwoColumn` updated to the new layout
- `README.md` — the Home-screen description, if it mentions tile order
- No API, daemon, or CLI changes.
