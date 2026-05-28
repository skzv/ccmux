# tui-design-system Specification

## Purpose

TBD - created by archiving change redesign-tui-charm. Update Purpose after archive.

## Requirements

### Requirement: Single token source of truth

The TUI SHALL define every color, spacing value, border radius, typography role, and semantic style in `internal/tui/styles/` as named tokens. No TUI screen file outside `internal/tui/styles/` and `internal/tui/components/` MAY introduce a literal color value (e.g., `lipgloss.Color("#…")`, `lipgloss.Color("123")`), a literal spacing value passed to `.Padding(...)` / `.Margin(...)` / `.PaddingLeft(...)` / `.MarginRight(...)`, or a literal border definition. All such values MUST come from the tokens layer.

#### Scenario: No inline hex colors in screen files

- **WHEN** the repository is searched under `internal/tui/` excluding `internal/tui/styles/` and `internal/tui/components/`
- **THEN** no Go source file contains a `lipgloss.Color("#` literal

#### Scenario: No inline spacing literals in screen files

- **WHEN** a TUI screen file applies padding or margin to a Lipgloss style
- **THEN** the numeric arguments are tokens from `styles.Spacing` (e.g., `s.Spacing.MD`) and not bare integer literals

#### Scenario: Styles screens consume from a single Styles value

- **WHEN** a screen renders any styled output
- **THEN** every style it uses is sourced from the `styles.Styles` value passed into the screen's model or returned by `styles.Default()`

### Requirement: Tokens layer structure

The `internal/tui/styles/` package SHALL expose, at minimum, a `Palette` type, a `Spacing` token set, a `Radius` token set, a `Typography` role set, a `Semantic` color set, and a `Styles` struct that aggregates the tokens for screen consumption. `styles.Default()` SHALL return the project's default `Styles` value.

#### Scenario: Default styles exposed

- **WHEN** a TUI screen calls `styles.Default()`
- **THEN** the returned `Styles` value contains a non-zero palette, spacing scale, radius set, typography roles, and semantic colors

#### Scenario: Spacing scale is enumerated

- **WHEN** a screen needs gap, padding, or margin sizing
- **THEN** it selects from named spacing tokens (e.g., `XS`, `SM`, `MD`, `LG`, `XL`) rather than arbitrary integers

#### Scenario: Semantic colors are named

- **WHEN** a screen indicates success, warning, danger, info, primary, or muted intent
- **THEN** it consumes the corresponding semantic style from `Styles` (e.g., `Styles.StatusGood`, `Styles.StatusWarning`, `Styles.StatusError`) rather than picking a palette color directly

### Requirement: Default theme

The TUI SHALL ship exactly one default theme. The default theme SHALL be exposed via a stable, theme-agnostic identifier (e.g., `styles.DefaultPalette`) so that a future change can swap palettes without renaming exported symbols across the codebase.

#### Scenario: Default theme reachable via stable name

- **WHEN** code outside `internal/tui/styles/` needs the default palette
- **THEN** it references a stable, theme-agnostic identifier (not a flavor-specific name like `CatppuccinMocha`)

#### Scenario: No theme picker

- **WHEN** the Settings screen renders
- **THEN** it does NOT expose a theme-selection control

### Requirement: Shared Header component is available

The TUI SHALL provide a single shared `Header` rendering component under `internal/tui/components/`. The Header is OPTIONAL — primary navigation screens MAY use it when a screen-level title or breadcrumb adds information that is not already present on the app-level tab strip or status bar. When a screen does use the Header, it SHALL pass through `components.Header` (not a bespoke alternative) so the visual treatment stays consistent.

#### Scenario: Header is available under components/

- **WHEN** a screen file imports `github.com/skzv/ccmux/internal/tui/components`
- **THEN** the `components.Header(styles.Styles, components.HeaderProps) string` function is callable

#### Scenario: Header has consistent height when used

- **WHEN** two screens render their views through `components.Header` at the same terminal width
- **THEN** their headers occupy the same number of vertical lines

#### Scenario: Header left slot always shows primary context when used

- **WHEN** a screen renders through `components.Header` at any width ≥ 40 columns
- **THEN** the left slot contains the screen's primary context identifier (the title and any breadcrumb the screen supplies)

#### Scenario: Screens MUST NOT roll their own header

- **WHEN** a screen renders a screen-level title bar
- **THEN** it uses `components.Header`, not a hand-rolled lipgloss composition

#### Scenario: Agent-accent helper is the single source of truth

- **WHEN** a screen renders a per-agent visual element (a label, a section heading, a chip)
- **THEN** the colour is sourced from `styles.AgentAccent(id agent.ID) lipgloss.Style`, not from an inline `lipgloss.NewStyle().Foreground(...)` literal selecting a palette colour

### Requirement: Shared Footer / HelpBar component

The TUI SHALL provide a single shared `HelpBar` (footer) rendering component under `internal/tui/components/`. The app-level help line at the bottom of the TUI SHALL render through this component (replacing the legacy hardcoded `? help • q quit • r refresh …` string). Each primary navigation screen SHALL supply its own `HelpBarProps` (an ordered list of `{Key, Label, Priority}` entries) so the help line is context-aware. The HelpBar SHALL render with consistent separators, accent colors, and graceful narrowing.

#### Scenario: HelpBar drives the app help line

- **WHEN** the TUI renders the bottom help line (and no toast is active)
- **THEN** the line is produced by the shared `components.HelpBar` function, driven by the active screen's `HelpBarProps`

#### Scenario: HelpBar drops by priority at narrow widths

- **WHEN** the available width is insufficient to render all provided shortcuts
- **THEN** the HelpBar omits entries in ascending order of priority (lowest priority first) until the remaining set fits

#### Scenario: HelpBar uses consistent separator and accent

- **WHEN** two screens render their HelpBars at the same width with the same number of fitting entries
- **THEN** the separator characters and accent color between entries are identical

### Requirement: Shared selectable-list rendering

The TUI SHALL provide a single shared selectable-list rendering helper under `internal/tui/components/`. The Sessions, Conversations, Projects, and Notes screens SHALL render their primary item rows through this helper. The helper SHALL display each item with a primary line, an optional secondary metadata line, and an optional trailing metadata segment, and SHALL highlight the selected item using an accent treatment that is identical across the four screens.

#### Scenario: List helper used by list-bearing screens

- **WHEN** the Sessions, Conversations, Projects, or Notes screen renders its item list
- **THEN** each row is produced by the shared list-rendering helper

#### Scenario: Selection treatment is identical across screens

- **WHEN** any of the four list-bearing screens renders a selected item
- **THEN** the selection's visual treatment (accent indicator, background, foreground emphasis) matches the same treatment on every other list-bearing screen

#### Scenario: Secondary metadata uses muted styling

- **WHEN** a list row includes secondary metadata
- **THEN** the secondary metadata renders in the muted-foreground semantic style

### Requirement: Information density preserved

The redesign SHALL NOT remove or hide any panel, status indicator, or numeric readout that the TUI currently displays by default. Specifically, the Dashboard SHALL continue to show its usage panel, agent stats, 5-hour quota bar, and per-host session breakdown without requiring a keypress to reveal them.

#### Scenario: Dashboard panels unchanged

- **WHEN** the Dashboard renders at a width ≥ 120 columns
- **THEN** every panel that was visible in the pre-redesign Dashboard at the same width remains visible

#### Scenario: No new "press to reveal" affordances

- **WHEN** any redesigned screen renders
- **THEN** information that was previously visible by default is NOT moved behind a new toggle, modal, or keystroke

### Requirement: Adaptive collapse order

The shared Header, HelpBar, and selectable-list components SHALL honor the existing single-breakpoint rule (`adaptive-screen-layout`: narrow = width < 120). When width is insufficient, the components SHALL collapse content in a defined order: Header right-slot chips collapse first, then HelpBar entries by ascending priority, then list secondary metadata. The primary content identifier on each screen MUST remain visible at any width ≥ 40 columns.

#### Scenario: Header right slot collapses first

- **WHEN** the terminal width is narrow enough that the Header's right slot would overflow
- **THEN** the right slot is hidden before any left-slot content is truncated

#### Scenario: List secondary metadata hides at narrow widths

- **WHEN** the terminal width is narrow enough that a list row's secondary metadata would overflow
- **THEN** the secondary metadata is hidden while the primary line remains visible (possibly truncated with "…")

### Requirement: Visual regression coverage

Every primary navigation screen (Dashboard, Sessions, Conversations, Projects, Notes, Settings) SHALL have at least one `teatest`-based golden-file test that snapshots the rendered view at a canonical terminal size of 120 columns by 40 rows. Golden files SHALL live under `internal/tui/testdata/golden/` and SHALL regenerate when the environment variable `CCMUX_UPDATE_GOLDEN=1` is set.

#### Scenario: Golden test exists per primary screen

- **WHEN** the test suite runs
- **THEN** each of Dashboard, Sessions, Conversations, Projects, Notes, and Settings has at least one passing golden-file test

#### Scenario: Golden regeneration honors env flag

- **WHEN** a developer runs the golden tests with `CCMUX_UPDATE_GOLDEN=1`
- **THEN** the golden files under `internal/tui/testdata/golden/` are rewritten and the tests pass on the regenerated output

#### Scenario: Golden mismatch fails CI

- **WHEN** any redesigned screen's rendered output diverges from its golden file in CI
- **THEN** the corresponding golden test fails

### Requirement: Modal and overlay exception

Full-screen overlays and modal forms (e.g., Tour, confirmation modals, Huh-based forms) MAY render without the shared Header and HelpBar when doing so would distort the overlay's intent. Such overlays SHALL still consume tokens from `internal/tui/styles/` for every color, spacing value, and border, and MUST NOT introduce inline literals.

#### Scenario: Modal opts out of standard header/footer

- **WHEN** a confirmation modal or full-screen overlay renders
- **THEN** it MAY omit the shared Header and HelpBar components

#### Scenario: Modal still consumes tokens

- **WHEN** a modal or overlay renders
- **THEN** all colors, spacing, and borders it uses are sourced from `internal/tui/styles/` tokens

### Requirement: Per-agent palette as single source of truth

The TUI SHALL expose a single, theme-agnostic helper that maps an `agent.ID` to a `lipgloss.Style` representing that agent's accent colour. Every surface that surfaces multiple agents (the Dashboard's Usage panel, the Conversations section nav, the Conversations row's agent label column, the Agents sub-tab row) SHALL consume the colour from this helper. Inline per-screen colour selection for agents SHALL NOT be reintroduced.

The mapping SHALL be: Claude=`Palette.Mauve`, Codex=`Palette.Sky`, Antigravity=`Palette.Peach`, Cursor=`Palette.Teal`.

#### Scenario: Agent accent helper exists

- **WHEN** a screen file imports `internal/tui/styles`
- **THEN** a `styles.AgentAccent(id agent.ID) lipgloss.Style` (or equivalent) is callable and returns the agent's accent style

#### Scenario: Conversations section nav uses the helper

- **WHEN** the Conversations screen renders `renderAgentNav`
- **THEN** the active section's label colour is produced by the shared helper for the matching agent ID, not by an inline `lipgloss.NewStyle().Foreground(…)` literal

### Requirement: Conversation row agent labels carry the agent accent

Each conversation row's agent label column SHALL render in the agent's accent colour (via the shared helper). Agent labels SHALL use bare lowercase names (`claude`, `codex`, `cursor`, `agy`) without bracket adornment. The row SHALL keep the agent label, compact timestamp, and prompt preview separated by compact fixed gaps, avoiding wide padded columns. The rest of the row (timestamp, preview) SHALL stay in the default / muted foreground.

#### Scenario: Codex row's agent label renders in sky

- **WHEN** the Conversations list renders a row whose `Conversation.Agent` is `agent.IDCodex`
- **THEN** the agent label (`codex`) renders in the colour returned by the agent-accent helper for Codex
- **AND** the label is not bracketed as `[codex]`

#### Scenario: Row columns are compact

- **GIVEN** a Codex conversation whose compact timestamp is `now`
- **WHEN** the Conversations list renders the row
- **THEN** the visible row text separates `codex`, `now`, and the prompt preview with compact gaps instead of wide padded columns

### Requirement: Armed-for-delete chip

When a row's `c.ID == m.pendingDelete`, the row SHALL render an `[delete? x to confirm · esc]` chip at the row's trailing edge in the `Semantic.Danger` colour. The agent label, timestamp, and a truncated preview SHALL remain visible on the same row.

#### Scenario: Armed row keeps identity visible

- **WHEN** the user presses `x` to arm delete on a conversation
- **THEN** the row displays the agent label, timestamp, a truncated preview, and the `[delete? x to confirm · esc]` chip — all on one row

### Requirement: Conversations columns do not bleed

In wide mode, the Conversations list column and detail column SHALL render as separate framed panes, matching the Projects and Notes two-column treatment. The columns SHALL reserve a visible gutter between panes, allocate roughly half the terminal width to the detail pane, and remain bounded to their allocated widths. Long project paths, skill paths, URLs, and first-prompt text in the detail pane SHALL wrap or hard-wrap within the detail column instead of relying on terminal soft wrapping.

#### Scenario: Long detail text stays inside the right column

- **GIVEN** a selected conversation has a long project path or first prompt containing path-like unbroken tokens
- **WHEN** the Conversations screen renders in wide mode
- **THEN** no rendered line exceeds the terminal width
- **AND** wrapped right-column text does not reflow beneath the left list

#### Scenario: Wide columns are framed with a gutter

- **WHEN** the Conversations screen renders in wide mode
- **THEN** the list and detail columns each render with pane borders
- **AND** a visible gutter separates the two bordered panes

### Requirement: Transcript preview modal

The Conversations screen SHALL bind the `p` key to open a focused overlay rendering the selected conversation's recent message thread (Glamour-rendered, last ~30 messages). The overlay SHALL close on `p` or `esc`. The overlay SHALL consume the design-system tokens for every rendered element.

#### Scenario: `p` opens the preview modal

- **WHEN** the user is on the Conversations screen with a conversation selected and presses `p`
- **THEN** the transcript-preview overlay opens and renders the recent messages

#### Scenario: Modal Glamour theme matches the design system

- **WHEN** the transcript-preview overlay renders markdown
- **THEN** code blocks, headings, links, and blockquotes use the same Glamour theme configuration the Notes preview pane uses (per the parallel `redesign-tui-notes` change)

### Requirement: Conversation previews show prompt text only

Conversation row previews, the detail pane's first-prompt block, and transcript-preview user turns SHALL strip agent-injected wrapper text before display. The data layer SHALL drop pure synthetic XML blocks such as `environment_context` and `user_instructions`, drop leading Codex `# AGENTS.md instructions ... <INSTRUCTIONS>...</INSTRUCTIONS>` bundles, strip XML tag delimiters from wrapper tags that contain useful body text, and remove leading skill-invocation prefixes such as `worktree-openspec-workflow /worktree-openspec-workflow` while preserving the prompt text after the command token.

#### Scenario: Skill invocation prefix is hidden

- **GIVEN** a Claude user transcript message begins with `worktree-openspec-workflow` followed by `/worktree-openspec-workflow help me create a spec`
- **WHEN** Conversations renders the row preview or first-prompt detail
- **THEN** the visible text starts with `help me create a spec`
- **AND** the skill name and slash-command token are not shown

#### Scenario: Codex AGENTS instructions are hidden

- **GIVEN** a Codex user transcript message begins with `# AGENTS.md instructions for /repo` and an `<INSTRUCTIONS>...</INSTRUCTIONS>` block before the real prompt
- **WHEN** Conversations renders the row preview or first-prompt detail
- **THEN** the visible text starts with the real prompt after the instructions bundle
- **AND** the AGENTS.md instructions are not shown

### Requirement: Scattered transcript fragments merge into one logical conversation

The Conversations data layer SHALL merge known transcript fragments in memory before the TUI, CLI list, project picker, preview modal, message count, recency sort, and delete flow consume them. It SHALL NOT rewrite, concatenate, move, or otherwise physically merge agent-owned transcript files on disk.

Claude fragments SHALL merge by parent session ID: `~/.claude/projects/<encoded-cwd>/<uuid>.jsonl` and nested `~/.claude/projects/<encoded-cwd>/<uuid>/subagents/*.jsonl` SHALL produce one `Conversation` whose `ID` is `<uuid>`. Codex rollout fragments SHALL merge by rollout UUID when multiple `~/.codex/sessions/**/rollout-<timestamp>-<uuid>.jsonl` files share the same `<uuid>`.

Merged conversations SHALL keep `Conversation.Path` as the primary/resumable transcript path and expose every contributing fragment through `Conversation.Paths`. Recent-message preview and message counts SHALL read all fragments, sort timestamped messages chronologically, and return the requested recent tail. Delete SHALL validate every fragment path before removing any fragment, then remove all validated fragments for the selected logical conversation.

#### Scenario: Claude parent and subagent fragments render as one row

- **GIVEN** a Claude parent transcript `<uuid>.jsonl`
- **AND** one or more nested `<uuid>/subagents/*.jsonl` fragments
- **WHEN** Conversations loads the Claude transcript tree
- **THEN** the list contains one row for `<uuid>`
- **AND** the row's last activity, message count, preview modal, and delete action account for every fragment

#### Scenario: Duplicate Codex rollout UUIDs render as one row

- **GIVEN** two Codex rollout JSONL files with the same rollout UUID
- **WHEN** Conversations loads the Codex session tree
- **THEN** the list contains one row for that UUID
- **AND** the row remains resumable with `codex resume <uuid>`

### Requirement: Non-Claude transcript rows expose common conversation metadata

The Conversations data layer SHALL populate the same user-facing row/detail metadata for supported non-Claude JSON transcript formats that it populates for Claude: agent ID, resumable conversation ID, project label when recoverable, last activity, first-prompt preview, recent messages, and message count.

Codex SHALL read project cwd from either top-level `cwd` or `session_meta.payload.cwd`, last activity from timestamped transcript events when present, and first-prompt preview from either `user_message` events or user `response_item` messages. Cursor SHALL read JSONL transcripts from `~/.cursor/projects/<encoded-cwd>/agent-transcripts/<uuid>/<uuid>.jsonl`. Antigravity SHALL continue to list opaque `.pb` transcripts by ID/mtime and SHALL additionally read Gemini-style JSON chat transcripts from `~/.gemini/tmp/<project-hash>/chats/session-*.json` when present.

#### Scenario: Codex payload metadata renders like Claude metadata

- **GIVEN** a Codex rollout whose `session_meta.payload.cwd` is set and whose first user text is stored as a `response_item`
- **WHEN** Conversations loads Codex transcripts
- **THEN** the Codex row has a project label, first-prompt preview, last activity, recent messages, and message count

#### Scenario: Cursor JSONL transcripts render in the Cursor section

- **GIVEN** a Cursor Agent JSONL transcript under `~/.cursor/projects/<encoded-cwd>/agent-transcripts/<uuid>/<uuid>.jsonl`
- **WHEN** Conversations loads all agents
- **THEN** the Cursor section contains a resumable row with first-prompt preview, recent messages, and message count

#### Scenario: Antigravity JSON chats render with previews

- **GIVEN** a Gemini-style Antigravity JSON chat under `~/.gemini/tmp/<project-hash>/chats/session-*.json`
- **WHEN** Conversations loads Antigravity transcripts
- **THEN** the Agy section contains a row with first-prompt preview, recent messages, and message count

### Requirement: Agent-coloured Projects rows

The Projects list SHALL render each project row's leading status glyph in the agent's colour from `Styles.AgentAccent(id)`. The colour mapping MUST be explicit (not hashed) so the legend rendered at the top of the pane and the per-row dots always agree on the same colour for the same agent. Host identity is preserved by the `on <host>` subheader and the detail-pane title.

#### Scenario: Each known agent ID maps to a stable colour

- **WHEN** the Projects list renders a project whose `Agent` is `"claude"`, `"codex"`, `"antigravity"`, or `"cursor"`
- **THEN** the leading status glyph is rendered in the colour returned by `Styles.AgentAccent(agent)` and the colour is identical across renders of the same agent ID

#### Scenario: Empty or unknown agent falls back to claude

- **WHEN** the Projects list renders a project whose `Agent` is empty
- **THEN** the leading status glyph is rendered in the same colour as `Styles.AgentAccent("claude")`, mirroring `project.ReadAgent`'s back-compat default

### Requirement: Agent legend at the top of the list

The Projects list SHALL render a one-line legend directly under the pane title enumerating every agent in `agent.All()` next to its `Styles.AgentAccent` glyph. The legend SHALL be omitted only when the list has no rows to render against.

#### Scenario: Legend lists every shipped agent

- **WHEN** the Projects list has one or more visible rows
- **THEN** the rendered output contains the literal `"agents:"` token and each agent's ID (`claude`, `codex`, `antigravity`, `cursor`)

### Requirement: Scaffolding flag chips

The Projects list SHALL render each project row's scaffolding flags (`HasGit`, `HasCM`, `HasDocs`) as bracketed chips (e.g., `[git]`, `[CLAUDE]`, `[docs/]`) consistent with the design-system chip vocabulary established in PR #114. The selected row's chips SHALL render in the accent colour; off-row chips SHALL render in the muted colour.

#### Scenario: Selected-row chips render in accent

- **WHEN** the cursor is on a project whose `HasGit`, `HasCM`, and `HasDocs` are all true
- **THEN** the row displays `[git] [CLAUDE] [docs/]` chips in the design-system accent foreground

#### Scenario: Off-row chips render in muted

- **WHEN** a project not under the cursor has scaffolding flags set
- **THEN** the row displays the same chips in the design-system muted foreground

### Requirement: Project-info modal

The Projects screen SHALL bind the `i` key to open a focused overlay rendering the selected project's full metadata: absolute path, agent sidecar contents, recent session count, CLAUDE.md head (first 10 lines), and last-modified timestamp. The overlay SHALL close on `i` or `esc`. The overlay SHALL NOT render when no project is selected.

#### Scenario: `i` opens the modal on a selected project

- **WHEN** the user is on the Projects screen with a project selected and presses `i`
- **THEN** the project-info overlay opens and renders the project's metadata; the keystroke is not passed through to the underlying list

#### Scenario: `i` is a no-op when no project is selected

- **WHEN** the Projects list is empty and the user presses `i`
- **THEN** no overlay opens; the keystroke is consumed without changing state

#### Scenario: Modal closes on `esc` or `i`

- **WHEN** the project-info overlay is open and the user presses `esc` or `i`
- **THEN** the overlay closes and the previous Projects view is restored

### Requirement: Refresh re-discovers projects

The `r` keybinding on the Projects screen SHALL re-run project discovery in addition to refreshing the session list. Without this, a project added or removed from disk while ccmux is running never appears (or remains stale) until restart.

#### Scenario: r on Projects refreshes projects + sessions

- **WHEN** the user presses `r` while the Projects screen is focused
- **THEN** both `refreshSessionsCmd` and `refreshProjectsCmd` fire as a single `tea.Batch`

### Requirement: Projects filter-state golden

The Projects screen's golden coverage SHALL include the filter-active state in addition to the default state. The filter-active golden SHALL render at the same canonical width (120 columns) as the default golden.

#### Scenario: Filter-active golden exists

- **WHEN** the test suite runs
- **THEN** `internal/tui/testdata/golden/projects_filter.txt` exists and passes against a fresh render of the Projects screen with `/` filter mode active

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

### Requirement: Agents sub-tab row uses the per-agent palette

The Agents tab's sub-tab row SHALL render each sub-tab label in the corresponding agent's accent colour from the shared `styles.AgentAccent(id)` helper. Inactive sub-tabs SHALL render in the muted colour. The active sub-tab SHALL be additionally marked with the `◆ ` glyph.

#### Scenario: Active Claude sub-tab renders in mauve

- **WHEN** the Agents tab is open with the Claude sub-tab active
- **THEN** the `Claude` label renders in the colour returned by `styles.AgentAccent(agent.IDClaude)` (mauve) with the `◆ ` glyph prefix

#### Scenario: Inactive sub-tabs render muted

- **WHEN** the Agents tab is open with the Claude sub-tab active
- **THEN** the `Codex`, `Antigravity`, and `Cursor` labels render in `Styles.Muted` (no agent-accent colour)

### Requirement: Per-sub-tab HelpBar

The Agents tab's `HelpBarProps` SHALL surface keys relevant to the active sub-tab in addition to the common Agents-tab keys. The Claude sub-tab SHALL include `m model`, `e effort`, `a always`, `y yolo`, `c CLAUDE.md`, `j settings.json`. The Codex and Antigravity sub-tabs SHALL include `y yolo` and `e edit`. The Cursor sub-tab SHALL render `(read-only)` in place of action keys.

#### Scenario: Claude sub-tab HelpBar advertises its keys

- **WHEN** the Agents tab is open with the Claude sub-tab active
- **THEN** the HelpBar contains `m`, `e`, `a`, `y`, `c`, `j` hints in addition to `? help`, `q quit`, `tab next`, `1-7 screens`

#### Scenario: Cursor sub-tab HelpBar omits Claude-specific keys

- **WHEN** the Agents tab is open with the Cursor sub-tab active
- **THEN** the HelpBar does NOT contain `m`, `c`, `j` hints

### Requirement: Cursor sub-tab populated from local SQLite

The Cursor sub-tab SHALL render usage data from `~/.cursor/ai-tracking/ai-code-tracking.db` via the new `internal/cursorusage` package when the database exists and is readable. The rendered fields SHALL include: conversation count, top models used (up to 5), AI-authored lines in the last 7 days, and most-recent activity timestamp. When the database does not exist, the sub-tab SHALL render a muted `Cursor not detected — install from cursor.com` placeholder.

#### Scenario: Cursor sub-tab renders the SQLite summary

- **WHEN** `~/.cursor/ai-tracking/ai-code-tracking.db` exists and contains rows in `ai_code_hashes`
- **THEN** the Cursor sub-tab renders the conversation count, top models, AI-lines-last-7d, and last-activity timestamp from the database

#### Scenario: Cursor sub-tab renders empty-state when DB missing

- **WHEN** `~/.cursor/ai-tracking/ai-code-tracking.db` does not exist
- **THEN** the Cursor sub-tab renders a muted `Cursor not detected — install from cursor.com` placeholder and the screen does not error
