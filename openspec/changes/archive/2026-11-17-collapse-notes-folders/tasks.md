## 1. Audit & test scaffolding

- [x] 1.1 Re-read `internal/tui/notes.go` cursor/selection helpers (`selected`, `selectedPath`, `listLen`, `noteRows`, `folderDepth`, `folderHeader`) and the up/down/left/right handlers in `Update`.
- [x] 1.2 Audit `internal/tui/notes_device.go` to determine whether cross-device notes share the list/cursor model; decide if v1 covers it or scopes to local notes (record the decision in the change).
- [x] 1.3 Add failing golden/table tests capturing the target behaviour: a freshly-opened project shows only collapsed top-level folders + root files (golden), and a keymap table test for expand/collapse/navigate.

## 2. Tree row model & fold state

- [x] 2.1 Add `noteRowKind`, `noteRow` types and an `expanded map[string]bool` field to `notesModel` (initialize empty == all collapsed).
- [x] 2.2 Implement `visibleRows() []noteRow` that walks `m.entries` + `expanded`, emitting folder headers and only descending into expanded folders; root files always emitted.
- [x] 2.3 Repoint `m.cursor` to index `visibleRows()` instead of `m.entries`; add a `selectedEntry() *notes.Entry` helper returning nil on folder rows, and update `selectedPath`, `listLen`, `selected`.
- [x] 2.4 Add cursor clamp after every rebuild (load, fold change, search exit) so cursor stays within visible-row bounds.

## 3. Rendering

- [x] 3.1 Rewrite `noteRows(width)` to render from `visibleRows()`, using existing depth indent (`m.st.Spacing.SM`) and `components.RenderListRow` for the selected-row bar.
- [x] 3.2 Add the collapse/expand affordance glyph to folder headers (`▸` collapsed, `▾` expanded; ASCII fallback consistent with existing glyph policy). No literal hex / bare padding ints (respect `styles_lint_test.go`).

## 4. Keybindings

- [x] 4.1 Implement `right`/`l` in list focus: collapsed folder → expand; expanded folder → move to first child; file (or childless folder) → move focus to preview (fall-through to today's behaviour).
- [x] 4.2 Implement `left`/`h` in list focus: expanded folder → collapse; file or collapsed nested folder → move cursor to parent header; top-level → no-op. Preserve preview→list focus return when `focus == focusPreview`.
- [x] 4.3 Implement cursor-safety on collapse: when collapsing folder `D` while cursor is on a descendant, move cursor to `D`'s header before rebuilding.
- [x] 4.4 Gate all tree/fold logic on `!m.hasActiveSearch()`; keep search results a flat list; clamp cursor on search exit.
- [x] 4.5 Make file-only actions (open-in-editor `enter`/`e`, preview refresh) inert when the cursor is on a folder row.

## 5. CLI affordance

- [x] 5.1 Add the initial-fold-state flag to the notes CLI entry path in `cmd/ccmux/cmd/` (finalize `--expand-all` vs `--folders=...`; default collapsed). Thread it into the notes model so it seeds `expanded` with all dirs when set.
- [x] 5.2 Add a CLI-surface test exercising the flag (default collapsed vs expand-all expanded).

## 6. Tests & verification

- [x] 6.1 Make the Task 1.3 tests pass; add table-driven keymap tests for: expand, collapse, right-drill-into-child, left-jump-to-parent, down-skips-collapsed, collapse-with-selection-inside (cursor safety).
- [x] 6.2 Update the notes golden file(s) for the new collapsed-by-default layout and the affordance glyphs.
- [x] 6.3 Run `go test ./...` (and `gofmt`/`go vet`/staticcheck via `make lint`); fix any failures before commit.

## 7. Docs

- [x] 7.1 Update `docs/02_Architecture/01_Notes_System.md` to describe the collapsible tree, default fold state, and keybindings.
- [x] 7.2 Update README + website notes docs/MDX for the new keybindings and CLI flag (per feature-surface policy).
