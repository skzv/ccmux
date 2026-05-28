## ADDED Requirements

### Requirement: Glamour preview consumes design-system tokens

The Notes preview pane SHALL pass a `glamour.Style` value derived from the design-system palette to `glamour.WithStyles(...)`. The Glamour configuration MUST map: H1 → `Styles.Type.Title` (mauve, bold); H2/H3 → `Styles.Type.Subtitle`; code blocks background → `Palette.BGAlt`; code text → `Palette.Lavender`; links → `Semantic.Accent`; blockquote → `Muted` with an `Semantic.Accent` leading bar. Default Glamour styles SHALL NOT be used.

#### Scenario: Preview pane uses the configured Glamour style

- **WHEN** the Notes preview pane renders any markdown file
- **THEN** the rendered output uses code-block background `Palette.BGAlt` and H1 colour matching `Styles.Type.Title`, not Glamour's default greys

### Requirement: H1 as row label fallback

The Notes file list SHALL render each entry's label as the file's leading H1 text when the file has one within the first 4 KiB, falling back to the filename otherwise. The fallback evaluation SHALL be cached per `(path, mtime)` so the list does not re-read every file on every render.

#### Scenario: File with H1 renders the H1 as the row label

- **WHEN** the Notes list renders a file whose first 4 KiB contains a `# My Project Vision` line
- **THEN** the row label reads `My Project Vision`, not the filename

#### Scenario: File without H1 renders the filename

- **WHEN** the Notes list renders a file whose first 4 KiB contains no `# ` line
- **THEN** the row label reads the filename

### Requirement: New-note flow

The Notes screen SHALL bind the `n` key to open a Huh-based modal asking for filename (suggested default: `notes/note-YYYY-MM-DD-HHMM.md`) and an optional one-line title. On submit, the modal SHALL create the file with a minimal `# {title}\n\n` body and open it in `$EDITOR`. On editor close, the entries list SHALL refresh.

#### Scenario: `n` creates and opens a note

- **WHEN** the user presses `n` with a project selected, submits the modal with filename `docs/test-note.md` and title `My Test Note`
- **THEN** the file is created with the body `# My Test Note\n\n`, `$EDITOR` opens on it, and on editor close the entries list refreshes with the new file at the cursor

#### Scenario: `n` is a no-op with no project selected

- **WHEN** the user presses `n` with no project selected
- **THEN** the modal does not open; a toast indicates a project must be selected first

### Requirement: Note-info modal

The Notes screen SHALL bind the `i` key to open a focused overlay rendering the selected note's full metadata: absolute path, frontmatter dump, line count, word count, modified time, and H1 text if any. The overlay SHALL close on `i` or `esc`.

#### Scenario: `i` opens the note-info modal

- **WHEN** the user is on the Notes screen with a note selected and presses `i`
- **THEN** the note-info overlay opens and renders the note's metadata
