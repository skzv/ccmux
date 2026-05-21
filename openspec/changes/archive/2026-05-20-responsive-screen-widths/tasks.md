## 1. Test harness

- [x] 1.1 Add `renderScreenAt(t, screen, width, height)` helper in `internal/tui/width_test.go` that builds a minimal App and calls the screen's View
- [x] 1.2 Add `assertNoOverflow(t, output, width)` — every line measured with `lipgloss.Width()` (ANSI-stripped) must be ≤ width
- [x] 1.3 Add `assertAbsent(t, output, anchors...)` and `assertPresent(t, output, anchors...)` helpers for T2/T0 checks
- [x] 1.4 Add `TestWidthSweep_AllScreens` — every Screen at widths 50, 80, 100, 120, 200; asserts no-overflow + T0 anchors present at all widths + T2 anchors absent at narrow widths (50/80/100)

## 2. Breakpoint unification

- [x] 2.1 Change `isNarrow(width)` (`projects.go:541`) from `width < 80` to `width < 120` — every existing caller (header, dashboard, projects, notes, sessions) picks up the new breakpoint
- [x] 2.2 Replace the `detailW < 20` branch in `conversations.go:249` with `isNarrow(width)` so Conversations goes narrow at the same width as every other screen
- [x] 2.3 Grep `internal/tui` for any other width threshold that isn't `isNarrow`; fold each into `isNarrow`
- [x] 2.4 Audit existing tests that render a screen at 80–119 expecting the wide layout; update their expectations to the narrow layout
- [x] 2.5 Add `TestConversations_UsesSharedBreakpoint` — narrow layout triggers at 119, wide at 120

## 3. Home — dashboard panels

- [x] 3.1 `heroPanel`: hide the welcome subtitle on narrow (T2); keep the update banner condensed to one line (T1)
- [x] 3.2 `statsPanel`: hide the live clock on narrow (T2); keep active/idle/waiting counts (T0)
- [x] 3.3 `devicesPanel`: hide the "this build:" line and the "unreachable peer…" help block on narrow (T2); keep device rows (T0)
- [x] 3.4 `usagePanel`: on narrow, collapse the whole panel to one line — `Claude · N prompts · $X · resets HH:MM` (T0+T1); drop cache, top-projects, Codex/Antigravity blocks (T2)
- [x] 3.5 Add `MaxWidth(width)` guard at each panel's outermost render so no panel can overflow
- [x] 3.6 Add `TestDashboardPanels_NarrowOmitsT2` and `TestDashboardPanels_WideKeepsT2`

## 4. Home — sessions list + detail

- [x] 4.1 Confirm `renderSessionLine` line-level gates (`inner > 50/60`) still hold; no change expected (already correct T1 behavior)
- [x] 4.2 Sessions detail (`renderDetail`): on narrow, render a condensed detail for the selected row instead of dropping the pane — T0 (name/host/state/project) + T1 (attached, detach instruction one-liner)
- [x] 4.3 Hide path, windows, created/changed, and the full key cheatsheet on narrow (T2)
- [x] 4.4 Verify `homeView` height math no longer starves the sessions list once `usagePanel`/`heroPanel` shrink on narrow
- [x] 4.5 Add `TestSessionsDetail_NarrowKeepsDetach` — detach instruction present at width 50; path/cheatsheet absent

## 5. Conversations screen

- [x] 5.1 On narrow: render list only, hide the detail pane (T2) and the "enter resume · x delete…" hint line (T2)
- [x] 5.2 Add `TestConversations_NarrowLayout` — conversation rows present, no overflow at width 50

## 6. Projects screen

- [x] 6.1 On narrow: hide the header hint "(/: filter   n: new …)" (T2); keep project names (T0) and host group subtitles (T1)
- [x] 6.2 Confirm the detail pane is already dropped on narrow (`isNarrow` path) — no change expected
- [x] 6.3 Add `TestProjects_NarrowLayout` — project name present, hint absent, no overflow at width 50

## 7. Notes screen

- [x] 7.1 On narrow: hide the hint line "p: switch · /: search …" (T2); keep header, entry list, search box (T0)
- [x] 7.2 Confirm the preview pane is already dropped on narrow (`isNarrow` path) — no change expected
- [x] 7.3 Add `TestNotes_NarrowLayout` — entry list present, hint absent, no overflow at width 50

## 8. Settings screen

- [x] 8.1 On narrow: hide the version + config-path lines and the "(↑/↓ to move…)" instructional subtitle (T2); keep editable field rows (T0) and the cursor-row hint (T1)
- [x] 8.2 Ensure each field row truncates within `width`; no row exceeds the terminal width
- [x] 8.3 Add `TestSettings_NarrowLayout`

## 9. Network screen

- [x] 9.1 On narrow: hide the legend line, the empty-state help paragraph, and the Selected block's os/address/dial/version lines (T2); keep device rows and the ssh-action line (T0/T1)
- [x] 9.2 Add `TestNetwork_NarrowLayout`

## 10. Agents screen

- [x] 10.1 On narrow: hide the subtab hint, the "settings: <path>" line, the per-block "press X to change" hints, the "Keys" cheatsheet, and the "last write backed up to…" line (T2) — across `claudeconfig.go` / `codexconfig.go` / `antigravityconfig.go`; keep subtab labels + each config block's heading and current value (T0/T1)
- [x] 10.2 Add `TestAgents_NarrowLayout`

## 11. Tab bar

- [x] 11.1 Confirm `renderHeader` collapses to numbers below 120 and shows labels at ≥ 120 (keyed to `isNarrow` — the constant move in 2.1 carries it; verify, no other change expected)
- [x] 11.2 Update `TestRenderHeader_NarrowCollapsesToNumbers` so widths 80–119 assert number-only tabs (narrow); add a ≥ 120 case asserting full labels
- [x] 11.3 On narrow, drop the ` ccmux ` brand title (T2) so the tab numbers get the reclaimed width

## 12. Chrome — status bar & footer

- [x] 12.1 `renderStatusBar`: on narrow, drop the refreshed-at clock and the version chip (T2); keep the battery-danger banner + daemon status (T0) and host + `N sess` (T1)
- [x] 12.2 Order the status bar's left block T0-first (battery, daemon, then host) so the `forceSingleLine` net truncates lower-priority content before the daemon status
- [x] 12.3 `renderFooter`: on narrow, collapse the hint line to `? help • q quit`; drop the `n new · x kill · r refresh` action hints (T2)
- [x] 12.4 Reorder the wide footer hint T0-first (`? help • q quit • r refresh • x kill • n new`) so truncation eats the T2 tail
- [x] 12.5 Fix the stale footer string — `1-6 screens` → `1-7 screens` (there are 7 screens)
- [x] 12.6 Make each chrome row compose its curated string for the width tier first; keep `forceSingleLine` only as the final overflow net, not the collapse mechanism
- [x] 12.7 Add `TestStatusBar_NarrowKeepsDaemonAndBattery`, `TestStatusBar_NarrowDropsClockAndVersion`, `TestFooter_NarrowKeepsHelp`, and `TestFooter_TruncationKeepsHelpOverActionHints`

## 13. Final verification

- [x] 13.1 `go test ./...` — all green
- [x] 13.2 `make build` — binary compiles
- [x] 13.3 Manual smoke: launch ccmux in a ~50-col terminal and tab through every screen; confirm no overflow and curated content (body and chrome) — *automated equivalent covered by `TestWidthSweep_AllScreens`; interactive smoke still pending a human*
- [x] 13.4 Update README / website docs if any user-visible screen behavior changed

