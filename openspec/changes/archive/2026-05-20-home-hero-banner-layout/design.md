## Context

The Home screen is assembled by `homeView` in `internal/tui/app.go`. Today: below the breakpoint it is a single column (hero → sessions → stat tiles); at or above it, two columns (left: sessions list + detail stacked; right: hero + session summary + devices + usage).

`sessionsModel.View(width, height, narrow)` bundles the sessions list and the selected-session detail (stacked) and also renders the rename / new-session modal overlays. The dashboard panels (`heroPanel`, `StatsView` → `statsPanel`/`devicesPanel`/`usagePanel`) curate by `dashboardModel.narrow`, which `homeView` sets from the terminal width.

This change rearranges the wide layout — the hero becomes a full-width top banner and the session detail moves to the right column — and removes the hero entirely below the breakpoint.

## Goals / Non-Goals

**Goals:**
- Wide (≥ 120): hero is a full-width banner across the top; beneath it, sessions list on the left and the session detail + the three stat tiles on the right.
- Narrow (< 120): the hero is absent; a single column of sessions → summary → devices → usage.
- Preserve the single breakpoint (`isNarrow`, width < 120) and the terminal-derived `narrow` propagation.

**Non-Goals:**
- Changing the per-screen / per-panel T0/T1/T2 content curation.
- Scrolling within a column — the right column may exceed terminal height and clamp at the bottom, as the single-column Home does today.
- Changing the narrow sessions block (still list + condensed detail stacked).

## Decisions

### Decision 1: `homeView` composes the layout from piece-renderers

The wide layout puts the sessions list and the session detail in *different* places (list left, detail top-right), so they can no longer be produced by one bundled `sessionsModel.View` call. `homeView` instead calls `sessionsModel.renderList` and `sessionsModel.renderDetail` directly (same package — they are accessible) plus the dashboard panels, and assembles the rows itself. `sessionsModel.View` is kept for the narrow single-column path (list + condensed detail stacked) and for modal rendering.

### Decision 2: Modal overlays stay owned by `sessionsModel.View`

The rename / new-session form overlays are rendered by `sessionsModel.View` as a centered modal over its whole area. `homeView` checks `sessionsM.form` / `sessionsM.renameForm` first; when a form is open it delegates to `sessionsModel.View` for the whole Home body and skips the layout. This keeps modal logic in one place rather than duplicating the `lipgloss.Place` call.

### Decision 3: The hero is omitted, not curated, below the breakpoint

On narrow, `homeView` simply does not call `heroPanel`. This is distinct from the T2-curation pattern (where a panel renders a shrunken form) — the whole panel is absent. `heroPanel` itself is unchanged; it is just not invoked.

### Decision 4: The right-column detail uses the full (wide) form

The right column is roughly half the terminal width (~100 cols on a 200-col monitor) — ample for the full detail with its key cheatsheet. `homeView` calls `renderDetail(rightW, false)` so the Session Info pane shows the complete attach / detach instructions, consistent with the rule that the narrow decision comes from the terminal width, never a sub-column width.

## Risks / Trade-offs

- [Risk] The right column stacks the full session detail (~25 lines) plus three stat tiles, so it can exceed the terminal height → Mitigation: `App.View`'s `clampLines` trims the bottom, exactly as it does for today's tall single-column Home. No scrolling in scope.
- [Risk] `homeView` reaching into `sessionsModel`'s unexported `renderList` / `renderDetail` widens the seam between the two → Mitigation: same package, and modal handling stays inside `sessionsModel.View` so there is still a single owner of that logic.
- [Trade-off] `heroPanel` also carries the launch-time "↑ update available" banner; removing the hero on narrow means a phone-width session never shows that nudge. Accepted — the nudge is rare and also surfaced by `ccmux update`; revisit if a phone user misses it.
