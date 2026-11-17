## Why

The Notes screen renders every `.md` file in a project as one flat, always-expanded list grouped by folder. Projects with many folders full of notes produce a list far taller than the pane, burying the folders a user actually wants and making the up/down cursor walk through hundreds of files. Collapsing folders by default turns the list into a navigable outline: you see the folder structure first, then drill into only the branches you care about.

## What Changes

- The notes list gains a **collapsible folder tree**. Folder headers become navigable, selectable rows (today the cursor only lands on files).
- **On opening a project, all folders start collapsed.** Only the top-level folder headers (and any root-level files) are visible.
- **`right`/`l` expands** the folder under the cursor; **`left`/`h` collapses** it. (Today these keys only swap focus between the list and preview panes — that behaviour moves to apply only when the cursor is on a file / the tree is fully navigated, see design.)
- Folder headers show an **expand/collapse affordance** (e.g. `▸` collapsed, `▾` expanded).
- `up`/`down` navigation walks the *visible* rows only — collapsed folders' children are skipped.
- Collapsing a folder whose descendant is selected moves the cursor to that folder header so selection never lands on a hidden row.
- A new `ccmux notes` CLI flag/affordance to control initial fold state, keeping the feature reachable from both surfaces per the feature-surface policy.

## Capabilities

### New Capabilities
- `notes-folder-navigation`: Collapsible folder tree in the Notes screen — initial collapsed state, expand/collapse keybindings, visible-row navigation, cursor-safety on collapse, and the CLI affordance.

### Modified Capabilities
<!-- None: there is no existing spec covering the notes list behaviour. -->

## Impact

- **Code:** `internal/tui/notes.go` (model state, `Update` key handling, `noteRows` rendering, cursor/selection helpers). Possibly `internal/tui/notes_device.go` for remote-notes parity.
- **CLI:** `cmd/ccmux/cmd/` — a `notes` subcommand or flag for initial fold state.
- **Tests:** golden-file tests for the collapsed/expanded list, table-driven keymap tests for expand/collapse, cursor-safety on collapse.
- **Docs:** README + website notes docs, `docs/02_Architecture/01_Notes_System.md`.
- **No data/format changes:** purely a view-state feature; the on-disk notes tree and `internal/notes` data model are unchanged (though a fold-state helper may be added).
