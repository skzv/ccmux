## Context

The Network tab lists every device ccmux knows about — local + configured remotes (`cfg.Hosts`) + auto-discovered tailnet peers + mobile peers (Moshi). `enter` drops into a plain SSH shell; `s` (added on `main` by `#109`–`#113`, after PR #114 forked) opens the SSH-setup wizard.

PR #114 added `networkModel.HelpBarProps` (`enter ssh`, `r refresh`) but missed `s`, missed any chip vocabulary for status, and didn't group the heterogeneous device list (local vs configured vs discovered vs mobile). The screen has a `networkLegend()` row explaining the icon-to-meaning mapping, which is itself a hint that the icons aren't self-explanatory.

The Dashboard's Devices panel already renders per-device chips (`[↑ update]`, `(Moshi)`, `(unreachable)`). Network is the same data with more detail; reusing the chip vocabulary unifies the cross-tab signal.

## Goals / Non-Goals

**Goals:**

- Apply the same chip + colour vocabulary the Dashboard Devices panel uses, so a peer's status reads the same way wherever ccmux surfaces it.
- Group devices by source (Local / Configured / Discovered / Mobile) so the user can mentally segment "machines I added" from "machines tailnet found for me".
- Drop the `networkLegend()` row once chips make the icon legend unnecessary.
- Update the HelpBar to advertise the `s` SSH-setup wizard that already works.

**Non-Goals:**

- Changes to the SSH-setup wizard itself (`internal/sshsetup`). This change consumes; doesn't modify.
- Changes to the discovery loop (tailnet scan, ccmuxd probe).
- A host-add / host-remove form. That's `ccmux host add` CLI territory.

## Decisions

### Source-grouped layout

**Decision:** Sort and group the `m.hosts` slice into four groups on render:

1. `Local` — `h.Local == true`
2. `Configured` — entries that originated from `cfg.Hosts` (track this with a flag set during refresh — currently the rows don't carry the source distinction).
3. `Discovered` — entries auto-found via `tailnet.Scan` (same flag).
4. `Mobile` — `h.Mobile == true`

Each group gets a `s.Type.Subtitle` heading; rows indent one design-system step inside the group. Empty groups don't render.

**Rationale:** The four sources have meaningfully different semantics (Local is "this machine", Configured is "I added this", Discovered is "tailnet found this", Mobile is "iOS Moshi peer"). Today they're interleaved; grouping reveals the structure.

**Implementation note:** `hostStatus` doesn't currently distinguish Configured vs Discovered. Add a `Source string` field (`"local"`, `"configured"`, `"discovered"`, `"mobile"`) populated during the App's refresh loop.

### Chip set

**Decision:** Render the following chips per row (multiple chips can appear; rendered left-to-right after the host name):

- `[Tailscale SSH ✓]` — `h.TailscaleSSH == true`. Means no setup wizard is needed.
- `[↑ update]` — peer's ccmuxd version differs from local. Yellow.
- `[unreachable]` — `h.NeedsInstall` or probe failed. Muted.
- `[SSH ✓]` — `internal/sshsetup` reports the peer is in `known_hosts` and pubkey auth is verified.
- `[Moshi]` — `h.Mobile`.
- `[no ccmuxd]` — peer responds to ping but `/v1/health` 404s.

**Rationale:** Each chip maps to an actionable state. The user reads the row left-to-right and knows what (if anything) needs attention.

### Drop the legend

**Decision:** Remove `networkLegend()` from the render. The chips are the legend.

**Rationale:** If a chip says `[Tailscale SSH ✓]`, the user doesn't need a separate legend row explaining what a green dot means. The text IS the legend.

### `i` host detail modal

**Decision:** Reserve `i` for the host-detail overlay. Renders full network detail: tailnet IP, public IP if any, ccmuxd version + last-probe time, ssh status, session count, last-attached timestamp. Parallel to the Dashboard's `u` and the other tabs' `i`.

**Rationale:** Same overlay idiom across every tab.

## Risks / Trade-offs

- **[Risk] More chips per row = more width pressure on narrow terminals.** → Mitigation: chips collapse to glyph-only on narrow (`✓` instead of `Tailscale SSH ✓`); the design-system's existing narrow-collapse rule applies.
- **[Risk] `[SSH ✓]` chip implies key verification we haven't done.** Today `internal/sshsetup`'s validation runs on demand, not at every refresh. → Mitigation: cache the last validation outcome per host in `hostStatus`; refresh on `s` keypress or via a slower 60s background probe. If unknown, omit the chip rather than render `[SSH ?]`.
- **[Trade-off] Source-grouping adds 4 heading rows.** → Justified: the screen rarely has more than ~10 hosts total, so the headings don't crowd; they make the layout legible.

## Migration Plan

1. Add the `Source` field to `hostStatus`; populate during refresh. No render change yet.
2. Implement the chip mapping. Regenerate per-host detail.
3. Implement source-grouped layout.
4. Drop the inline legend.
5. Update HelpBar to advertise `s`.
6. Add the `i` modal.
7. Add `network.txt` + `network_empty.txt` goldens.

Rollback: revert. No persisted state.

## Open Questions

- Should the Dashboard's Devices strip get the same `[Tailscale SSH ✓]` / `[SSH ✓]` chips? **Tentative:** Yes — same chip vocabulary, same one-line strip; consistency across tabs.
- Should the `[SSH ✓]` chip render on the Local row? **Tentative:** No — the local row is always reachable; the chip is "remote peer is reachable" not "local machine is reachable".
