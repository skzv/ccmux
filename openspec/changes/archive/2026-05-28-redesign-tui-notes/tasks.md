## 1. H1 fallback for row labels

- [x] 1.1 Extend `notes.Vault.List` (or do it in the TUI layer) to read each entry's leading 4 KiB and extract a `# Heading` line if present.
- [x] 1.2 Cache the extraction per `(path, mtime)` to avoid re-reading every render.
- [x] 1.3 Set `Entry.Display` to the H1 text when found; otherwise the filename.
- [x] 1.4 Unit test against fixture markdown files (with H1, without H1, with H1 buried after long frontmatter).

## 2. Glamour theming

- [x] 2.1 Build a `glamour.Style` value from the design-system palette (H1 → Title; H2/H3 → Subtitle; code bg → BGAlt; code text → Lavender; links → Accent; blockquote → Muted + Accent bar).
- [x] 2.2 Pass via `glamour.WithStyles(...)` in the preview pane render.
- [x] 2.3 Round-trip render test: known markdown input produces output containing the expected token colours.

## 3. New-note flow (`n`)

- [x] 3.1 Add `newNoteFormModel` (Huh-based, file: `internal/tui/newnote.go`) asking for filename + optional title.
- [x] 3.2 Wire `n` key in `notes.go`'s Update; gate on `m.project != nil`; surface toast otherwise.
- [x] 3.3 On submit, write the file with `# {title}\n\n` body and dispatch `openInEditor`.
- [x] 3.4 On editor close, refresh the entries list.
- [x] 3.5 Reintroduce `n` to `HelpBarProps`.
- [x] 3.6 Update `helpForScreen[ScreenNotes]` to describe the simpler one-flow form.
- [x] 3.7 Update the empty-state hint to `(no markdown files yet — press n to create one)`.

## 4. Folder-group sub-section indent

- [x] 4.1 In `noteRows`, render folder headers as `s.Type.Subtitle` and indent the rows beneath by one design-system step (2 cells).
- [x] 4.2 Update the existing tests covering folder-group rendering to assert the new indent.

## 5. Search-hit chip

- [x] 5.1 Promote `Rel:N` in the search-hit rows to a muted bracketed chip; the snippet stays default-foreground.

## 6. `i` note-info modal

- [x] 6.1 Add `noteInfoOverlay` model rendering full path, frontmatter dump, line/word counts, modified time, H1.
- [x] 6.2 Wire `i` key in `app.go` with `!modalCapturingText()` guard.

## 7. Goldens

- [x] 7.1 Regenerate `internal/tui/testdata/golden/notes.txt` capturing the new visuals.
- [x] 7.2 Add `internal/tui/testdata/golden/notes_search.txt` capturing search-results state.

## 8. `bubbles/spinner` for note scan

- [x] 8.1 Replace `Loading notes…` muted placeholder with `bubbles/spinner`.

## 9. Validate

- [x] 9.1 Run `go test ./...` and `make lint`; confirm green.
- [x] 9.2 Run `openspec validate redesign-tui-notes --type change --strict --no-interactive`.
- [x] 9.3 Run `openspec instructions apply --change redesign-tui-notes --json` and confirm `state != "blocked"`.
- [x] 9.4 After merge: `openspec archive redesign-tui-notes --yes`.
