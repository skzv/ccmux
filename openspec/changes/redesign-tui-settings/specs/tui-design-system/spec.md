## ADDED Requirements

### Requirement: Settings field sub-section grouping

The Settings screen SHALL render its editable fields grouped into labeled sub-sections (`Subscription`, `Projects`, `Agents`, `Sleep prevention`, `Hosts`). Each sub-section heading SHALL use `Styles.Type.Subtitle`. Each sub-section body SHALL be indented one design-system step (2 cells) inside its heading.

#### Scenario: Three editable groups render as labeled sections

- **WHEN** the Settings screen renders at width ≥ 100 columns
- **THEN** the screen contains three sub-section headings (`Subscription`, `Projects`, `Agents`) above the Sleep prevention and Hosts blocks, each with its editable fields indented beneath

### Requirement: Settings active-field treatment

The active editable field's cursor SHALL render using the design-system accent-bar treatment (`▌ ` + elevated background) consistent with `components.RenderListRow`'s selected-row treatment on every other selectable surface. The legacy `▸ ` marker SHALL NOT be used.

#### Scenario: Active field has the accent-bar

- **WHEN** the cursor is on the `subscription.tier` field
- **THEN** that row renders with the design-system accent-bar prefix and the elevated background — the same treatment a selected row in `components.RenderListRow` uses

### Requirement: Fixed-enum field value chips

Editable fields whose value is a fixed enum or boolean (e.g., `subscription.tier`, `agents.default`, `sleep.mode`) SHALL render the current value as a bracketed chip (`[max5x]`, `[claude]`, `[off]`). The active-row chip SHALL render in `Semantic.Accent`; off-row chips SHALL render in muted.

#### Scenario: Tier field renders the current tier as a chip

- **WHEN** `subscription.tier` is `"max5x"` and the field is not under the cursor
- **THEN** the row's value renders as a `[max5x]` chip in the muted foreground

### Requirement: Settings info modal

The Settings screen SHALL bind the `i` key to open a focused overlay rendering reference metadata: ccmux version, absolute config-file path, log-file path, last config-save time, and last-save error if any. The legacy `ccmux version <v>` and `config file <path>` rows SHALL NOT render in the default Settings view.

#### Scenario: `i` opens the info modal

- **WHEN** the user is on the Settings screen and presses `i`
- **THEN** the info overlay opens; the previous Settings view is hidden until the overlay closes

#### Scenario: Default Settings view omits reference metadata

- **WHEN** the Settings screen renders at width ≥ 120 columns
- **THEN** neither `ccmux version` nor `config file` appears in the body; they live in the info overlay
