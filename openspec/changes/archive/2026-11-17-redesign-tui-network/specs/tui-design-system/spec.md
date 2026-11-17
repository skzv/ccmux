## ADDED Requirements

### Requirement: Per-host status chips

The Network screen SHALL render per-host status chips on each device row, drawn from a fixed vocabulary:

- `[Tailscale SSH ✓]` — `hostStatus.TailscaleSSH == true`.
- `[↑ update]` — peer's ccmuxd version differs from the local build version.
- `[unreachable]` — `NeedsInstall` or probe failed.
- `[SSH ✓]` — `internal/sshsetup` reports the peer is in `known_hosts` and pubkey auth is verified.
- `[Moshi]` — `Mobile == true`.
- `[no ccmuxd]` — peer pings but `/v1/health` returns 404 / not found.

The local row SHALL NOT carry `[↑ update]` or `[SSH ✓]`. Chips SHALL collapse to glyph-only form on narrow terminals (e.g., `✓` instead of `Tailscale SSH ✓`).

#### Scenario: Update-available chip on a version-divergent peer

- **WHEN** the Network list renders a non-local row whose `Version` differs from the local build's `Version`
- **THEN** the row includes the `[↑ update]` chip in the `Semantic.Warning` foreground

#### Scenario: Local row never shows the update chip

- **WHEN** the Network list renders the local row
- **THEN** the row does NOT include the `[↑ update]` chip regardless of version comparison

### Requirement: Source-grouped device list

The Network screen SHALL group device rows by source into four ordered sections: `Local`, `Configured`, `Discovered`, `Mobile`. Each section SHALL render a `Styles.Type.Subtitle` heading. Empty sections SHALL NOT render. Rows within a section SHALL indent one design-system step (2 cells) beneath the section heading.

The `hostStatus` struct SHALL carry a `Source` field (`"local"`, `"configured"`, `"discovered"`, `"mobile"`) populated during the App's refresh loop.

#### Scenario: All four source groups render when each has at least one device

- **WHEN** the Network screen renders with at least one device per source
- **THEN** the body contains four labeled sections in the order `Local`, `Configured`, `Discovered`, `Mobile`

#### Scenario: Empty source groups are skipped

- **WHEN** the Network screen renders with no Mobile peers
- **THEN** the body contains only the populated section headings; no empty `Mobile` heading appears

### Requirement: Network host-detail modal

The Network screen SHALL bind the `i` key to open a focused overlay rendering the selected peer's full network detail: tailnet IP, public IP (when available), ccmuxd version + last-probe timestamp, SSH status (key fingerprint, last-auth check result), session count, last-attached timestamp. The overlay SHALL close on `i` or `esc`.

#### Scenario: `i` opens the host-detail modal

- **WHEN** the user is on the Network screen with a peer selected and presses `i`
- **THEN** the host-detail overlay opens and renders the peer's network detail

### Requirement: Network legend dropped

The Network screen SHALL NOT render an inline `networkLegend()` row (or equivalent icon-glossary text) when the per-host status chips are rendered. The chips serve as the legend.

#### Scenario: Wide-mode Network screen omits the legend row

- **WHEN** the Network screen renders at width ≥ 120 columns with at least one device row
- **THEN** the screen body contains no muted-text icon glossary row above or below the device list
