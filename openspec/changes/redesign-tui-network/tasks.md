## 1. `Source` field on `hostStatus`

- [x] 1.1 Add a `Source string` field to `hostStatus`: `"local"`, `"configured"`, `"discovered"`, `"mobile"`.
- [x] 1.2 Populate the field during the App's refresh loop (`refreshSessionsCmd` and its host-discovery path).
- [x] 1.3 Update existing tests to seed the new field where they construct `hostStatus` values.

## 2. Per-host status chips

- [x] 2.1 Implement chip mapping in `network.go`'s `renderRow`:
  - [x] `[Tailscale SSH ✓]` when `h.TailscaleSSH`.
  - [x] `[↑ update]` when version differs and `!h.Local`.
  - [x] `[unreachable]` when `h.NeedsInstall` or probe failed.
  - [x] `[SSH ✓]` when `internal/sshsetup` reports verified pubkey auth.
  - [x] `[Moshi]` when `h.Mobile`.
  - [x] `[no ccmuxd]` when peer pings but `/v1/health` 404s.
- [x] 2.2 Render test: each chip appears under the expected condition; local row never shows `[↑ update]` or `[SSH ✓]`.

## 3. Source-grouped layout

- [x] 3.1 Implement the four-section render: `Local`, `Configured`, `Discovered`, `Mobile`. Sub-section headings use `s.Type.Subtitle`. Rows indent one design-system step.
- [x] 3.2 Empty sections skipped.
- [x] 3.3 Render test: section ordering pinned, empty sections omitted.

## 4. Drop the inline legend

- [x] 4.1 Remove the `networkLegend()` call from the wide-mode render.
- [x] 4.2 Drop or simplify the empty-state explanatory paragraph (still useful) — chips ARE the legend.

## 5. HelpBar refresh

- [x] 5.1 Add `s setup ssh` to `networkModel.HelpBarProps`.
- [x] 5.2 Confirm `enter ssh`, `r refresh`, `1-7 screens`, `? help`, `q quit` all wire to real actions.

## 6. `i` host-detail modal

- [x] 6.1 Add `hostDetailOverlay` model rendering tailnet IP, public IP, ccmuxd version + last-probe, SSH status, session count, last-attached.
- [x] 6.2 Wire `i` key in `app.go` with `!modalCapturingText()` guard.

## 7. Goldens

- [x] 7.1 Add `internal/tui/testdata/golden/network.txt` capturing a multi-source populated state.
- [x] 7.2 Add `internal/tui/testdata/golden/network_empty.txt` capturing the empty state.

## 8. `bubbles/spinner` for per-host probe

- [x] 8.1 Render a per-row spinner where a chip would be, while a probe is in flight; replace with the actual chip once it lands.

## 9. Validate

- [x] 9.1 Run `go test ./...` and `make lint`; confirm green.
- [x] 9.2 Run `openspec validate redesign-tui-network --type change --strict --no-interactive`.
- [x] 9.3 Run `openspec instructions apply --change redesign-tui-network --json` and confirm `state != "blocked"`.
- [ ] 9.4 After merge: `openspec archive redesign-tui-network --yes`.
