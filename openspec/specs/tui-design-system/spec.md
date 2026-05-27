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

**Why the requirement is opt-in rather than mandatory.** The first pass of the redesign added a per-screen Header row on every screen, which on the home/Dashboard duplicated the tab-strip label (`Sessions`) and the status-bar session count (`N sess`). That stacked chrome works against the redesign's "calmer, less busy" goal. Keeping the Header available but opt-in means screens that genuinely have a screen-level breadcrumb (e.g., a Notes detail view showing the active file path) can still get a consistent treatment, while screens whose identity is fully carried by the tab strip don't pay for redundant chrome.

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

