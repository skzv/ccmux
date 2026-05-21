## 1. homeView layout

- [x] 1.1 Add a modal short-circuit at the top of `homeView`: when `sessionsM.form` or `sessionsM.renameForm` is set, delegate the whole body to `sessionsModel.View` and return
- [x] 1.2 Narrow path (`isNarrow(width)`): drop the hero entirely; render a single column — `sessionsModel.View(width, listH, true)` then `StatsView(width)`
- [x] 1.3 Wide path: render `heroPanel(width)` as a full-width banner; below it a row of two columns
- [x] 1.4 Wide left column: `sessionsModel.renderList(leftW, rowH)` (list only)
- [x] 1.5 Wide right column: `JoinVertical` of `renderDetail(rightW, false)` then `StatsView(rightW)`
- [x] 1.6 Set `a.dashboard.narrow` from the terminal width before calling any panel (unchanged pattern)

## 2. Sessions component

- [x] 2.1 Confirm `sessionsModel.View` still works for the narrow single-column path (list + condensed detail) and for modal rendering — keep it; no callers other than `homeView`
- [x] 2.2 Confirm `renderList` and `renderDetail` are callable from `homeView` (same package) with the signatures they have

## 3. Tests

- [x] 3.1 Update `TestHomeView_NarrowSingleColumn`: assert the "Hello." hero is ABSENT at width < 120; sessions + summary + devices + usage still present in column order
- [x] 3.2 Update `TestHomeView_WideTwoColumn`: assert the hero spans the top, the sessions list is the left column, and the right column holds the session detail + the three tiles; no overflow
- [x] 3.3 Add `TestHomeView_WideHeroIsBanner` — at width ≥ 120 the "Hello." hero appears before (above) both the sessions list and the Session Info detail
- [x] 3.4 Confirm `TestWidthSweep_AllScreens` still passes (Home T0 anchors: "Hello." moved to the T2/narrow-absent list, "Sessions" is the new T0 anchor)

## 4. Verification

- [x] 4.1 `go test ./...` — all green
- [x] 4.2 `make build` — binary compiles; `gofmt` / `go vet` clean
- [x] 4.3 Manual smoke: launch ccmux on a wide monitor (hero banner on top, list left, Session Info + tiles right) and at ~50 cols (no hero, single column) — *automated coverage via `TestHomeView_*` + `TestWidthSweep_AllScreens`; interactive smoke still pending a human*
- [x] 4.4 Update `README.md` if the Home-screen description mentions tile order — checked; README does not describe Home tile order, no change needed
