## Why

PR #114 (`redesign-tui-charm`) landed the design-system foundation and brought the Sessions/Dashboard surface to a polished state. The Notes tab got the structural plumbing — three selection sites migrated to `components.RenderListRow`, the inline hint row dropped from `renderList`, `HelpBarProps` exposed — but the visible surface (file-tree column on the left, Glamour-rendered preview on the right) still reads flat.

Two pain points are specific to Notes:

- The HelpBar advert dropped `n` because the new-note flow was never wired. The Feature Catalog flags it as a Phase 2 follow-up; this change is the right place to actually implement it.
- The Glamour preview pane uses default Glamour styles, which clash with the design-system palette. The pane reads as a third-party widget bolted onto ccmux rather than part of the same TUI.

This change implements the new-note flow, themes Glamour to match the design system, and applies the sub-section / chip / modal vocabulary to the file list.

## What Changes

- **HelpBar audit**: pin every advertised key against its handler. Reintroduce `n` in the HelpBar after the new-note flow is wired (this change implements it). Drop the lingering "new note picker (Agent Log / Spec / ADR)" mention in `helpForScreen[ScreenNotes]` since this change ships a simpler single-flow version.
- **New-note flow (`n` keybind)**:
  - On `n`, open a Huh-based modal asking for filename (suggested default: `notes/note-YYYY-MM-DD-HHMM.md` under the project root) and an optional one-line title for the front matter.
  - On submit, create the file with a minimal `# {title}\n\n` body and open it in `$EDITOR` via the existing `openInEditor` helper.
  - On editor close, refresh the entries list so the new file appears at the cursor.
  - Errors (filename conflict, write failure, no project selected) surface as toasts.
- **Glamour theme tune**: pass a custom `glamour.WithStyles(...)` configuration that maps Glamour's theme tokens (heading colours, code bg, link, blockquote) to the design-system palette. Codeblocks use `s.P.BGAlt`; headings use `s.Type.Title` mauve; muted text uses `s.Muted`.
- **Folder-group sub-section indent**: `noteRows` already emits a muted folder header before each group. Align indent with the design-system 2-cell step. Folder headers render in `s.Type.Subtitle`; files indent one step in; the active file gets the accent bar from `RenderListRow`.
- **Front-matter aware row labels**: when a note has YAML/TOML frontmatter or a leading H1, `notes.Entry.Display` falls back to the H1 text over the filename. Makes the row read as the note's title rather than `00_Vision.md`.
- **`i` keybind + note info modal**: open an overlay with full path, frontmatter dump, line/word counts, modified time, and the H1 if any. Parallel to the Dashboard's `u` overlay.
- **Search-hit chip rendering**: promote `Rel:Line` to a muted bracketed chip `[Rel:N]` so the snippet has the visual weight on the row.
- **Per-screen golden refresh**: regenerate `internal/tui/testdata/golden/notes.txt`. Add a second golden capturing search-results state.
- **`bubbles/spinner` for note scan**: replace the muted `Loading notes…` placeholder.

**Non-goals:**

- No multi-kind picker (Agent Log / Spec / ADR). Simpler "name + open editor" flow only. Multi-kind picker is a follow-up.
- No Obsidian integration changes.
- No ripgrep-search rewrite. Existing semantics stay.

## Capabilities

### Modified Capabilities

- `tui-design-system`: adds Notes-specific scenarios for sub-section indent (folder groupings), Glamour theming, and the `i` info-modal pattern. Adds a requirement for the Glamour preview pane to consume design-system tokens for every render.

## Impact

- **Affected code:** `internal/tui/notes.go` (new-note Huh modal, Glamour theming, folder indent, chip rendering), `internal/tui/app.go` (overlay routing for `i`), `internal/notes/notes.go` (small extension to surface a note's H1 to `Entry.Display`).
- **Tests:** existing `notes_test.go` stays; one new test for the new-note flow; two new tests for H1-as-Display fallback and Glamour theming; `notes.txt` golden regenerates; a new `notes_search.txt` golden captures search-results state.
- **Dependencies:** no new third-party. Uses `huh`, `bubbles/spinner`, `glamour` (all vendored).
- **User-visible:** pressing `n` actually creates and opens a note; preview reads as part of ccmux; folder groups have proper indent; row labels prefer H1 over filename when available.
- **CLI:** no changes.
