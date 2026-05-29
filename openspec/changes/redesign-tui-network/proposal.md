## Why

PR #114 (`redesign-tui-charm`) added per-screen `HelpBarProps` to the Network tab (`enter ssh`, `r refresh`) but didn't touch the rendering. The Network screen lists every device the dashboard knows about (local + configured remotes + auto-discovered tailnet peers + mobile peers) and lets the user `enter` to ssh in. The list is flat — no grouping, no status chips for the actionable signals (`[Tailscale SSH ✓]`, `[↑ update]`, `[unreachable]`), and the inline header hint `(N device(s) — enter: ssh   r: refresh)` duplicates the HelpBar.

The SSH-setup wizard that landed on `main` since PR #114 (`#109`–`#113`) added a parallel `s` keybind that opens the wizard for the selected peer. The HelpBar wasn't updated; that's a now-broken advert claim that this change needs to surface.

Two more visible gaps:

- The Devices panel on the Dashboard already renders per-device status chips (`[↑ update]` on version mismatch, `(unreachable)` on probe failure, `(Moshi)` on a mobile peer). The Network screen — same data with more detail — doesn't. Duplicate state, divergent visuals.
- The status legend (`networkLegend()`) only renders on wide. The accompanying glossary is a wall of muted text. Chips remove the need for a legend; the colour + bracket idiom is the legend.

This change applies the chip vocabulary, groups devices by source, and surfaces the SSH-setup wizard in the HelpBar.

## What Changes

- **HelpBar refresh**: add `s setup ssh` (the new wizard keybind) so the advert matches the handler. Confirm `enter ssh`, `r refresh`, `1-7 screens`, `? help`, `q quit` all wire to real actions.
- **Per-host status chips** (consistent with the Dashboard's Devices strip):
  - `[↑ update]` — peer's ccmuxd version differs from local.
  - `[Tailscale SSH ✓]` — peer uses Tailscale SSH; no setup wizard needed.
  - `[unreachable]` — ccmuxd is not responding.
  - `[SSH ✓]` — peer is in `known_hosts` and pubkey auth is verified.
  - `[Moshi]` — mobile peer (replaces the existing inline string).
  - Local row never carries `[↑ update]`.
- **Sub-section grouping by source**:
  - `Local` (the row for this machine).
  - `Configured` (peers from `cfg.Hosts`).
  - `Discovered` (tailnet peers auto-found via the tailnet scan).
  - `Mobile` (Moshi peers).
    Each group gets a `s.Type.Subtitle` heading; rows indent one design-system step inside the group. Empty groups don't render.
- **Drop the inline `(N device(s) — enter: ssh   r: refresh)` reference** from the pane title. The HelpBar carries the keys.
- **Drop the `networkLegend()` row when chips are sufficient**: with explicit chips, the legend reads as redundant.
- **`i` keybind + host detail modal**: open an overlay showing the selected peer's full network detail — tailnet IP, public IP if any, ccmuxd version + last-probe time, ssh status, session count, last-attached timestamp. Parallel to the Dashboard's `u` overlay.
- **Per-screen golden**: add `network.txt` (multi-source state) and `network_empty.txt` (empty state).
- **`bubbles/spinner` for the per-host probe**: the network refresh fires probes against every peer; render a per-row spinner where the chip would be, swapping to the actual chip once the probe lands.

**Non-goals:**

- No changes to the SSH-setup wizard itself (`internal/sshsetup`). This change consumes it; it doesn't modify it.
- No changes to the host-discovery loop. Pure rendering.
- No host-add / host-remove form in this screen — that's `ccmux host add` CLI territory.

## Capabilities

### Modified Capabilities

- `tui-design-system`: adds Network-specific scenarios for per-host status chips, source-grouped device lists, and the legend-via-chips replacement.

## Impact

- **Affected code:** `internal/tui/network.go` (renderRow chip rendering, source-grouped layout, drop inline legend), `internal/tui/app.go` (overlay routing for `i`).
- **Tests:** existing `network_test.go` stays; new tests for chip mapping (`[Tailscale SSH ✓]`, `[↑ update]`, `[unreachable]`, `[SSH ✓]`) and source-group ordering. Two new goldens: `network.txt` and `network_empty.txt`.
- **Dependencies:** no new third-party. Uses existing `sshsetup` package for the auth-verified detection.
- **User-visible:** Network reads as four grouped lists with chips per row; `s` opens the SSH-setup wizard; `i` opens a per-host detail modal.
- **CLI:** no changes.
