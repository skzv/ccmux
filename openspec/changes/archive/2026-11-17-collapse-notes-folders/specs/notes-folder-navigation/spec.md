## ADDED Requirements

### Requirement: Folders start collapsed when a project is opened

When the Notes screen loads a project's note tree, every folder SHALL be rendered collapsed by default. Only top-level folder headers and root-level files (notes whose folder path is empty) SHALL be visible initially. A folder's child notes and sub-folders SHALL NOT be rendered until the folder is expanded.

#### Scenario: Opening a project with foldered notes

- **WHEN** the user opens a project whose notes live in one or more folders
- **THEN** the list shows each top-level folder as a single collapsed header row
- **AND** no files inside any folder are shown
- **AND** the cursor starts on the first visible row

#### Scenario: Root-level files remain visible

- **WHEN** a project contains notes at its root (no enclosing folder) alongside foldered notes
- **THEN** the root-level files are shown
- **AND** all folders are still collapsed

### Requirement: Expand and collapse folders with arrow keys

The Notes list SHALL let the user expand the folder under the cursor with `right` (or `l`) and collapse it with `left` (or `h`), while the list pane has focus. Folder headers SHALL display an affordance distinguishing collapsed from expanded state.

#### Scenario: Expanding a collapsed folder

- **WHEN** the cursor is on a collapsed folder header and the user presses `right`
- **THEN** the folder's direct children (files and sub-folder headers) become visible immediately below it
- **AND** the folder's affordance changes to its expanded indicator

#### Scenario: Collapsing an expanded folder

- **WHEN** the cursor is on an expanded folder header and the user presses `left`
- **THEN** the folder's children are hidden
- **AND** the folder's affordance changes to its collapsed indicator

#### Scenario: Right on an expanded folder drills into its first child

- **WHEN** the cursor is on an already-expanded folder header and the user presses `right`
- **THEN** the cursor moves to the folder's first visible child row

#### Scenario: Left on a child jumps to its parent folder

- **WHEN** the cursor is on a file or a collapsed folder nested inside another folder and the user presses `left`
- **THEN** the cursor moves to the enclosing parent folder's header row

### Requirement: Navigation walks only visible rows

Up/down cursor movement in the Notes list SHALL traverse only rows that are currently visible. Rows hidden inside a collapsed folder SHALL be skipped entirely and SHALL NOT be reachable by the cursor.

#### Scenario: Down skips collapsed folder contents

- **WHEN** the cursor is on a collapsed folder header and the user presses `down`
- **THEN** the cursor moves to the next visible row (the following folder or root file), not to a hidden child

#### Scenario: Navigating an expanded folder includes its children

- **WHEN** a folder is expanded and the user presses `down` from its header
- **THEN** the cursor moves to the folder's first child row

### Requirement: The cursor never lands on a hidden row

Collapsing a folder SHALL NOT leave the cursor pointing at a row that is no longer visible. When a folder is collapsed while the cursor is on one of its descendants, the cursor SHALL move to that folder's header row. After any change to fold state or the loaded entry list, the cursor SHALL be clamped to a valid visible row.

#### Scenario: Collapsing a folder that contains the selection

- **WHEN** the cursor is on a file (or sub-folder) inside folder `D` and the user collapses `D`
- **THEN** the cursor moves to `D`'s header row
- **AND** the selected row is the now-collapsed `D`

#### Scenario: Cursor stays valid after the tree reloads

- **WHEN** the note tree is reloaded (e.g. after creating or refreshing notes) while folders are collapsed
- **THEN** the cursor remains on a visible row within the new list bounds

### Requirement: File actions operate on the selected file row only

Actions that require a file (opening in the editor, previewing) SHALL apply only when the cursor is on a file row. When the cursor is on a folder header, file-specific actions SHALL be inert and SHALL NOT attempt to open or preview a file.

#### Scenario: Enter on a folder header does not open an editor

- **WHEN** the cursor is on a folder header and the user presses the open-in-editor key
- **THEN** no editor is launched
- **AND** the folder selection is unchanged

#### Scenario: Selecting a file row previews it

- **WHEN** the cursor moves onto a file row
- **THEN** that file's contents are shown in the preview pane

### Requirement: CLI control of initial fold state

The `ccmux` CLI SHALL provide a way to open the Notes view with all folders expanded, so the collapsing behaviour is reachable and overridable from the command line as well as the TUI. The default, with no flag, SHALL be all-collapsed to match the TUI default.

#### Scenario: Expand-all flag opens the tree fully expanded

- **WHEN** the user launches the notes view via the CLI with the expand-all flag set
- **THEN** every folder is rendered expanded on open

#### Scenario: Default CLI invocation matches the TUI default

- **WHEN** the user launches the notes view via the CLI with no fold-state flag
- **THEN** all folders start collapsed

### Requirement: Search results ignore fold state

While a notes search is active, results SHALL be shown as a flat list independent of folder fold state. Exiting search SHALL return to the folder tree with the cursor on a valid visible row.

#### Scenario: Searching shows a flat result list

- **WHEN** the user runs a search with folders collapsed
- **THEN** all matching results are listed regardless of which folders are collapsed

#### Scenario: Exiting search restores a valid tree cursor

- **WHEN** the user clears an active search
- **THEN** the folder tree is shown again with its prior fold state
- **AND** the cursor is on a visible row within bounds
