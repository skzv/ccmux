## MODIFIED Requirements

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
