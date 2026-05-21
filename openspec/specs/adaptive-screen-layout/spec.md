# adaptive-screen-layout Specification

## Purpose
TBD - created by archiving change responsive-screen-widths. Update Purpose after archive.
## Requirements
### Requirement: Screen width contract
Every TUI screen SHALL render without any visible line exceeding the terminal width. A "visible line" is a newline-delimited segment measured in display columns (ANSI escape codes excluded). This contract holds for any terminal width in the range [40, ∞).

#### Scenario: No overflow at 50 columns
- **WHEN** the terminal width is 50 columns
- **THEN** every line of every screen's rendered output is ≤ 50 display columns wide

#### Scenario: No overflow at 80 columns
- **WHEN** the terminal width is 80 columns
- **THEN** every line of every screen's rendered output is ≤ 80 display columns wide

#### Scenario: No overflow at 120 columns
- **WHEN** the terminal width is 120 columns
- **THEN** every line of every screen's rendered output is ≤ 120 display columns wide

#### Scenario: No overflow at 200 columns
- **WHEN** the terminal width is 200 columns
- **THEN** every line of every screen's rendered output is ≤ 200 display columns wide

### Requirement: Primary content always visible
Every screen SHALL always display its primary content identifier (session name for Sessions, project name for Projects, note title for Notes, etc.) regardless of terminal width, as long as width ≥ 40.

#### Scenario: Session name visible at 80 columns
- **WHEN** the Sessions screen renders at 80 columns with at least one session in the list
- **THEN** the session name is present in the rendered output (possibly truncated with "…" but not absent)

#### Scenario: Project name visible at 80 columns
- **WHEN** the Projects screen renders at 80 columns with at least one project in the list
- **THEN** the project name is present in the rendered output

### Requirement: Narrow layout breakpoint
There SHALL be exactly one layout breakpoint. A screen is "narrow" when `width < 120` and "wide" otherwise. Every screen that adapts to width MUST use this single breakpoint (`isNarrow`); no screen may define its own threshold.

#### Scenario: Single shared breakpoint
- **WHEN** any screen decides whether to render its narrow or wide layout
- **THEN** the decision is made by `isNarrow(width)` (`width < 120`)
- **THEN** no screen uses a different width threshold (e.g. a derived detail-pane width) to make the same decision

#### Scenario: Side-by-side panes collapse below the breakpoint
- **WHEN** a screen with a list + detail side-by-side layout renders at width < 120
- **THEN** the panes are stacked or the detail pane is omitted, never rendered side-by-side
- **THEN** no rendered line exceeds the terminal width

### Requirement: Secondary content omitted below the narrow breakpoint
Below the narrow breakpoint, each screen SHALL render a curated subset of its content. Elements classified T2 (reference) in the change's design document MUST be omitted entirely; elements classified T1 (useful) MAY be condensed; elements classified T0 (glanceable) MUST remain. Omission means the content is absent — there is no reveal affordance in the TUI.

#### Scenario: Usage panel collapses to one line
- **WHEN** the Home screen renders at width < 120
- **THEN** the Claude usage panel renders as a single summary line carrying the prompt count
- **THEN** the cache breakdown, top-projects list, and Codex/Antigravity blocks are absent

#### Scenario: Inline hint lines are hidden
- **WHEN** any screen renders at width < 120
- **THEN** inline hint / cheatsheet lines (e.g. "enter: attach   x: kill") are absent

#### Scenario: Detach instructions survive narrow mode
- **WHEN** the Sessions detail for a selected session renders at width < 120
- **THEN** the detach instruction (how to return after attaching) is present, condensed to a short form
- **THEN** the session path, window count, and full key cheatsheet are absent

#### Scenario: Secondary content returns when wide
- **WHEN** a screen renders at width ≥ 120
- **THEN** all T2 elements for that screen are present

### Requirement: Home screen layout adapts to width
Below the narrow breakpoint the Home screen SHALL render as a single full-width column with the hero omitted entirely, stacked top to bottom: sessions (list + condensed detail) → session summary → devices → usage. At or above the breakpoint the Home screen SHALL render the hero as a full-width banner across the top; beneath the banner it SHALL render two columns — the sessions list on the left, and on the right the session detail pane followed by the session summary, devices, and usage tiles stacked beneath it.

#### Scenario: Hero omitted below the breakpoint
- **WHEN** the Home screen renders at width < 120
- **THEN** the "Hello." hero is absent from the output
- **THEN** the sessions block, session summary, devices, and usage render in one full-width column

#### Scenario: Hero is a full-width banner at or above the breakpoint
- **WHEN** the Home screen renders at width ≥ 120
- **THEN** the "Hello." hero spans the top of the screen above both columns
- **THEN** no rendered line exceeds the terminal width

#### Scenario: Sessions list on the left, detail and tiles on the right
- **WHEN** the Home screen renders at width ≥ 120
- **THEN** the sessions list occupies the left column
- **THEN** the right column stacks the session detail pane on top, then the session summary, devices, and usage tiles
- **THEN** the session detail pane shows the full key cheatsheet (it is not collapsed to the phone form)

### Requirement: Tab bar always readable
The tab bar header SHALL always show either the full screen label or the screen number for every screen, regardless of terminal width.

#### Scenario: Wide tab bar shows labels
- **WHEN** the terminal width is ≥ 120
- **THEN** each screen's full label is present in the rendered header

#### Scenario: Narrow tab bar shows numbers
- **WHEN** the terminal width is < 120
- **THEN** each screen's number is present in the rendered header
- **THEN** the active screen's initial letter is present in the rendered header

### Requirement: Chrome rows curate then clamp
The persistent chrome rows — header, status bar, footer — SHALL each render a width-appropriate curated subset of their content using the same T0/T1/T2 model as screen bodies. Single-line truncation (`forceSingleLine`) is a final overflow safety net, not the collapse mechanism: each chrome row MUST compose its curated string for the current width tier before any truncation, and MUST order its content so that if truncation still occurs it removes T2 content before T0 or T1 content.

#### Scenario: Status bar keeps safety and daemon status on narrow
- **WHEN** the status bar renders at width < 120
- **THEN** the battery-danger banner (when active) and the daemon online/offline status are present

#### Scenario: Status bar drops reference detail on narrow
- **WHEN** the status bar renders at width < 120
- **THEN** the refreshed-at clock and the version chip are absent

#### Scenario: Footer keeps the help gateway on narrow
- **WHEN** the footer renders at width < 120 with no toast active
- **THEN** the `? help` hint is present
- **THEN** the per-screen action hints (`n new`, `x kill`, `r refresh`) are absent

#### Scenario: Truncation removes the least important content first
- **WHEN** a chrome row's curated string still exceeds the terminal width
- **THEN** truncation removes T2 content before T0/T1 content, because the row orders its content T0-first

#### Scenario: Error toast takes the footer at every width
- **WHEN** an error toast is active
- **THEN** the footer renders the toast in place of the hint line, at every width

### Requirement: Width-sweep test coverage
The test suite SHALL include a width-sweep test for every screen that renders each screen at widths 50, 80, 100, 120, and 200 and asserts the screen width contract and primary content visibility requirements.

#### Scenario: Test runner covers all screens
- **WHEN** the width-sweep tests run
- **THEN** every screen defined in the `Screen` enum is exercised at each canonical width (50, 80, 100, 120, 200)
- **THEN** any screen that overflows its allocated width causes the test to fail

#### Scenario: Narrow widths assert omission
- **WHEN** the width-sweep tests run at a narrow width (50, 80, or 100)
- **THEN** each screen's T2 elements are asserted absent
- **THEN** each screen's T0 elements are asserted present

