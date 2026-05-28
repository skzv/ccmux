## ADDED Requirements

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

## MODIFIED Requirements

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
