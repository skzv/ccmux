## Context

The Notes screen (`internal/tui/notes.go`) renders a project's whole markdown tree as a single flat list. `noteRows(width)` walks the sorted `m.entries []notes.Entry`, inserts a folder header row each time `Entry.Dir` changes, and renders every file. The cursor (`m.cursor int`) is an index into `m.entries` — it only ever points at a *file*; the "active" folder is derived from the selected file's `Dir`. Folders are always fully expanded.

Constraints from the codebase:
- The left pane is roughly `width/3` cells, so indentation is 1 cell per depth level — vertical and horizontal space are both tight.
- Styling tokens only: no literal hex / bare padding ints in screen files (`styles_lint_test.go` enforces this).
- Every `exec.Command` takes a context; this feature shells out to nothing new.
- Feature-surface policy: must be reachable from TUI *and* CLI, and ship with tests.
- `right`/`l` and `left`/`h` currently toggle focus between the list pane and the preview pane. Re-using them for expand/collapse must not strand the user's ability to reach the preview.

## Goals / Non-Goals

**Goals:**
- Folders start collapsed when a project is opened; the user expands branches on demand.
- `right`/`l` expands and `left`/`h` collapses the folder under the cursor.
- Cursor navigation (`up`/`down`) walks only currently-visible rows.
- The cursor can never land on a hidden (collapsed-away) row.
- A CLI affordance controls the initial fold state so the feature isn't TUI-only.
- Pure view-state: no change to on-disk notes or the `internal/notes` data model.

**Non-Goals:**
- Persisting fold state across app restarts (initial state is always "all collapsed" unless overridden by the CLI flag for that invocation).
- Recursive expand-all / collapse-all keybindings (may be a follow-up).
- Changing search behaviour — search results remain a flat list and ignore fold state.
- Multi-level lazy loading from disk; the full entry list is already loaded, we only change what is *rendered*.

## Decisions

### Decision 1: Introduce a navigable tree row model derived from the flat entry list

Today `m.cursor` indexes `m.entries` (files only) and headers are non-navigable artifacts of rendering. Expand/collapse requires the cursor to land on *folders*. We introduce an explicit, derived list of **visible rows**, each tagged as either a folder header or a file:

```go
type noteRowKind int
const (
    rowFolder noteRowKind = iota
    rowFile
)

type noteRow struct {
    kind     noteRowKind
    dir      string        // folder path for rowFolder; "" otherwise
    depth    int
    entryIdx int           // index into m.entries for rowFile; -1 for rowFolder
}
```

`m.cursor` becomes an index into this derived `[]noteRow` (visible rows) rather than into `m.entries`. A helper `m.visibleRows() []noteRow` rebuilds the slice from `m.entries` + fold state on demand (the entry list is small enough — hundreds of items — that recomputing per keypress is cheap and avoids a stale-cache class of bugs).

**Why over alternatives:** keeping the cursor as an `m.entries` index and "skipping" folded files during navigation was considered, but it leaves no way for the cursor to *sit on* a folder header to toggle it, which is the core interaction. A derived visible-row list makes "what is selectable" and "what is rendered" the same thing.

### Decision 2: Fold state as a `map[string]bool` of *expanded* folders

```go
expanded map[string]bool // key: Entry.Dir; absent or false == collapsed
```

Default-collapsed means the map starts empty, so the natural zero value is the desired default — no per-folder initialization walk. `expanded[dir]` true means that folder's direct children are visible. Keys are the same slash-separated `Dir` strings already used for grouping.

Nested folders: a child folder is only *visible* (and thus only togglable) when all of its ancestors are expanded. `visibleRows()` enforces this by only descending into expanded folders, so an unreachable folder's own expanded-bit is simply never consulted.

**Why a set of expanded rather than a set of collapsed:** the default is "all collapsed," and an empty set is the cleanest representation of that default. It also means newly-discovered folders (e.g. after a refresh) are collapsed automatically.

### Decision 3: Keybinding semantics for `right`/`left` (and `l`/`h`)

Reconcile expand/collapse with the existing focus-toggle. New rules when `m.focus == focusList`:

- **`right`/`l`**: if cursor is on a *collapsed folder* → expand it. If on an *expanded folder* → move cursor to its first child. If on a *file* (or an already-expanded folder with no children) → move focus to the preview pane (preserves today's behaviour as the "fall-through").
- **`left`/`h`**: if cursor is on an *expanded folder* → collapse it. If on a *file* or *collapsed folder* → move cursor to the parent folder header (jump "out"). If already at a top-level row → keep today's behaviour is unnecessary since focus is on list; left at top level is a no-op.
- When `m.focus == focusPreview`: `left`/`h` returns focus to the list (unchanged).

This mirrors the near-universal tree-view idiom (file explorers, `tree` widgets): right drills in, left backs out, and only "bottoms out" into the adjacent pane. Documented in the spec scenarios so it's testable.

**Alternative considered:** dedicated keys (e.g. `space`/`enter`) for toggle, leaving arrows as focus-only. Rejected: `space` already opens the project picker, `enter` opens the editor, and arrow-driven expand/collapse is what users expect from a tree.

### Decision 4: Cursor safety on collapse

Collapsing a folder can hide the currently-selected row (a descendant file or sub-folder). Rule: **when collapsing folder `D`, if the current cursor row is a descendant of `D`, move the cursor to `D`'s header row.** Implemented by checking `strings.HasPrefix(row.dir-or-file-dir, D+"/")` before recomputing visible rows, then locating `D` in the new visible list. After any rebuild, `m.cursor` is clamped to `[0, len(visible)-1]`.

### Decision 5: CLI affordance

Add initial-fold control to the CLI so the feature is reachable there too. Minimal surface: a `--expand-all` (or `--folders=expanded|collapsed`) flag on the notes entry path. The TUI default remains collapsed; the flag, when set, seeds `expanded` with every `Dir` so the screen opens fully expanded (today's behaviour, opt-in). This satisfies the feature-surface policy without inventing a heavy new command. Exact flag name finalized in tasks; default = collapsed to match the proposal.

### Decision 6: Visual affordance via tokens

Folder headers gain a leading glyph: `▸ ` collapsed, `▾ ` expanded (ASCII fallback `>`/`v` if the theme is ASCII-only — reuse whatever glyph policy existing components use). The active-folder dot and selected-row bar continue to come from `components.RenderListRow` / existing styles. No new hex or bare padding ints — depth indent keeps using `m.st.Spacing.SM`.

## Risks / Trade-offs

- **[Cursor model rewrite touches all navigation paths]** → The change from "cursor indexes entries" to "cursor indexes visible rows" affects `selected()`, `selectedPath()`, `listLen()`, up/down handlers, and clamp-on-load. Mitigation: centralize through `visibleRows()` + a single `selectedEntry()` helper that returns nil for folder rows; add table-driven tests covering navigation across collapsed/expanded states before refactoring.

- **[Search interaction]** → Search results are a separate flat list (`searchResults`); fold state must not apply during an active search, and exiting search must restore a valid cursor into the visible tree. Mitigation: gate all tree logic on `!m.hasActiveSearch()`; on search-exit, clamp cursor as on load.

- **[Default-collapsed hides the user's note on open]** → Someone who always lands on one root file may find their list "empty-looking." Mitigation: root-level files (`Dir == ""`) are always visible (they have no folder to collapse into); only foldered notes hide. The CLI `--expand-all` flag is the escape hatch, and expand is a single keystroke.

- **[Recompute-per-keypress cost]** → `visibleRows()` runs on every navigation key. Mitigation: entry counts are in the hundreds and the walk is O(n); acceptable. Cache can be added later if profiling shows a problem.

- **[Remote/device notes parity]** → `notes_device.go` may render its own list path. Mitigation: audit it in tasks; reuse the same `visibleRows()`/fold-state where it shares the model, or scope the feature to local notes for v1 and note the gap.

## Migration Plan

Pure additive view feature; no data migration, no rollback concerns. If the interaction proves disruptive, the CLI default can be flipped to expanded, or the whole tree mode gated behind a config key. The on-disk notes tree is untouched, so reverting the code fully reverts behaviour.

## Open Questions

- Final CLI flag spelling: `--expand-all` boolean vs `--folders=expanded|collapsed`. Leaning boolean for simplicity; resolve in tasks.
- Should there be a config.toml key for the *default* fold state (so a user can make "expanded" their permanent default)? Out of scope for v1 unless trivial to thread through.
- Whether `notes_device.go` (cross-device remote notes) gets the tree in this change or a follow-up — pending the tasks-phase audit.
