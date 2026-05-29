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

### Requirement: Settings two-column master-detail layout

In the wide layout (≥ 120 columns) the Settings screen SHALL render as two framed panes side by side — matching the Projects / Conversations master-detail treatment: a left list pane (the Moshi block, the grouped editable fields, and the read-only Sleep/Daemon/Hosts blocks) and a right detail pane describing the active field. The pane carrying keyboard focus (the list, or the editor while editing) SHALL use `Styles.PaneFocused`; the other SHALL use `Styles.Pane`. The right detail pane SHALL show the active field's label, its current value, its full description, and — for fixed-enum fields — the enum options. The per-field description SHALL NOT render below the row in the wide layout. In the narrow layout (< 120 columns) the screen SHALL collapse to the single-pane stacked layout, with the active field's description and editor rendered on the lines below its row.

#### Scenario: Wide terminals render two framed panes

- **WHEN** the Settings screen renders at width 120 with the cursor on `claude.tier`
- **THEN** a left list pane and a right detail pane render side by side, and the detail pane shows `claude.tier`, its current value, and its full description

#### Scenario: Narrow terminals collapse to a single column

- **WHEN** the Settings screen renders at width 50 and the cursor is on an editable field
- **THEN** a single pane renders and the active field's description appears on the line below its row, with no second pane and no line exceeding the terminal width

### Requirement: Active-field options list

When the cursor is on an editable field with a fixed enum (`options` slice non-empty) in the wide layout (≥ 120 columns), the right detail pane SHALL render an `Options` block listing every value one per line, with the current value rendered as the active bracketed chip (`[max5x]`) and the rest muted. Free-text fields (e.g. `projects.root`) SHALL render no `Options` block. While the inline editor is open, the detail pane SHALL render the editor and a save/cancel hint.

#### Scenario: Options list shows the enum set on a chip field

- **WHEN** the cursor is on `subscription.tier` (current value `max5x`) at width 120
- **THEN** the detail pane shows an `Options` block listing `api`, `pro`, `max5x`, `max20x` with `[max5x]` bracketed as the current value

#### Scenario: Free-text fields show no options list

- **WHEN** the cursor is on `projects.root` (no fixed enum) at width 120
- **THEN** no `Options` block appears in the detail pane

### Requirement: Fixed-enum field value chips

Editable fields whose value is a fixed enum or boolean (e.g., `subscription.tier`, `agents.default`, `sleep.mode`) SHALL render the current value as a bracketed chip (`[max5x]`, `[claude]`, `[off]`). Each chip SHALL render in a semantic color drawn from the live `Styles.Semantic` palette so the row reads as a status at a glance: status-positive values (`on`, `safe`, `mirror`) in `Semantic.Success`; status-negative or warning values (`off`, `dangerous`, `exclusive`) in `Semantic.Warning`; classification values (`api`, `pro`, `max5x`, `max20x`) in `Semantic.Info`; everything else in `Semantic.Accent`. Active-row chips SHALL render bold so they pop against the elevated background without changing hue.

#### Scenario: Tier field renders the current tier as a colored chip

- **WHEN** `subscription.tier` is `"max5x"` and the field is not under the cursor
- **THEN** the row's value renders as a `[max5x]` chip in `Semantic.Info`

#### Scenario: Boolean on/off chip uses status colors

- **WHEN** `update.auto_check` is `on`
- **THEN** the row's value renders as `[on]` in `Semantic.Success`; when `off`, it renders in `Semantic.Warning`

### Requirement: Per-agent subscription tiers

The Settings screen SHALL render one tier row per supported agent under the `Subscription` sub-section: `claude.tier`, `codex.tier`, `antigravity.tier`, `cursor.tier`. Each row SHALL chip-render the current value and Enter-cycle through that agent's own published enum (Anthropic: `api / pro / max5x / max20x`; OpenAI: `api / free / plus / pro / team`; Google AI: `api / free / ai-pro / ai-ultra`; Cursor: `free / pro / pro+ / ultra / teams`). The single legacy `subscription.tier` row SHALL NOT render. The persisted config SHALL keep `subscription.tier` as the Claude tier (top-level TOML key, for back-compat with binaries and tools that predate this change) and SHALL store every other agent's tier under `subscription.tiers.<agent_id>` so an older binary opening the file still finds `subscription.tier` and a newer reader uses `Subscription.TierFor(agentID)` to route to the right field transparently.

#### Scenario: Subscription section lists one row per available agent

- **WHEN** the Settings screen renders at width ≥ 100 columns and every agent is available
- **THEN** the Subscription sub-section contains the four rows `claude.tier`, `codex.tier`, `antigravity.tier`, `cursor.tier` in that order, and the legacy `subscription.tier` row is absent

### Requirement: Hide tier rows for unavailable agents

The Settings screen SHALL omit a per-agent tier row when that agent is not installed or otherwise launchable on the current machine (no PATH binary and no setup-pinned command override), so the user only sees tiers they can act on. The omitted rows SHALL also be skipped by cursor navigation. Claude's tier row (`claude.tier`) SHALL always render regardless of availability, since it is the app's primary agent and drives the dashboard quota bar. Until availability has been detected, the screen MAY show all rows.

#### Scenario: Unavailable agent's tier row is hidden

- **WHEN** the Cursor agent is unavailable on this machine
- **THEN** the `cursor.tier` row does not render in the Subscription sub-section and the cursor cannot land on it

#### Scenario: Claude tier always shows

- **WHEN** no coding agents are detected as available
- **THEN** the `claude.tier` row still renders

#### Scenario: Setting a non-Claude tier does not touch the legacy Tier field

- **WHEN** the user sets `codex.tier` to `pro`
- **THEN** `config.toml`'s top-level `subscription.tier` is unchanged and `subscription.tiers.codex` is written as `"pro"`

#### Scenario: Claude tier still reads/writes the top-level key

- **WHEN** the user sets `claude.tier` to `max5x`
- **THEN** `config.toml`'s top-level `subscription.tier` is written as `"max5x"` and `subscription.tiers.claude` is NOT written (the legacy key is the canonical Claude storage)

### Requirement: Settings info modal

The Settings screen SHALL bind the `i` key to open a focused overlay rendering reference metadata: ccmux version, absolute config-file path, log-file path, last config-save time, and last-save error if any. The legacy `ccmux version <v>` and `config file <path>` rows SHALL NOT render in the default Settings view.

#### Scenario: `i` opens the info modal

- **WHEN** the user is on the Settings screen and presses `i`
- **THEN** the info overlay opens; the previous Settings view is hidden until the overlay closes

#### Scenario: Default Settings view omits reference metadata

- **WHEN** the Settings screen renders at width ≥ 120 columns
- **THEN** neither `ccmux version` nor `config file` appears in the body; they live in the info overlay
