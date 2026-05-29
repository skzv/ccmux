## 1. Field sub-section grouping

- [x] 1.1 Restructure `editableFields()` (or add a sibling `groupedEditableFields()`) returning the field list segmented into three groups: `Subscription`, `Projects`, `Agents`.
- [x] 1.2 Update `View()` to render each group with a `s.Type.Subtitle` heading and the design-system 2-cell indent step.
- [x] 1.3 Align the existing `Sleep prevention` and `Hosts` bottom blocks to the same heading style.

## 2. Accent-bar active-field treatment

- [x] 2.1 Replace the `▸ ` cursor marker with the design-system accent-bar treatment for the active field. Reuse `components.RenderListRow` or an inline equivalent that produces the same visual.
- [x] 2.2 Add a render test covering active-field rendering at width 120.

## 3. Chip rendering for fixed-enum values

- [x] 3.1 For boolean / enum fields (`subscription.tier`, `agents.default`, `sleep.mode`), render the current value as `[value]` chip — accent on the active row, muted off-row.
- [x] 3.2 Add a render test covering chip presence + colour selection.
- [x] 3.3 Map each chip value to a semantic color (`on`/`safe`/`mirror` → `Success`; `off`/`dangerous`/`exclusive` → `Warning`; `api`/`pro`/`max5x`/`max20x` → `Info`). Apply to chips in the Sleep prevention and Daemon blocks too. Active-row chips render bold without changing hue.
- [x] 3.4 Add `options` slices to `subscription.tier`, `sessions.attach_mode`, `update.auto_check` so Enter cycles them — keeps every chip field's discoverability + edit affordance consistent with `agents.default`.
- [x] 3.5 In the wide layout, render the active field's enum as an `Options` list in the right detail pane (one value per line, current value chipped); free-text fields show none.
- [x] 3.6 Add a render test that the options track lists each value, brackets the current one, and is omitted for free-text rows / when the editor is open.

## 4. `i` info modal

- [x] 4.1 Add `settingsInfoOverlay` model rendering ccmux version, config path, log path, last-save time, last-save error if any.
- [x] 4.2 Wire `i` key in `app.go` with `!modalCapturingText()` guard.
- [x] 4.3 Drop the `ccmux version` and `config file <path>` rows from the default Settings view.

## 5. `bubbles/spinner` for async probes

- [x] 5.1 Replace the `Detecting Moshi…` (and equivalent host-probe) muted placeholders with `bubbles/spinner`.

## 4b. Per-agent subscription tiers

- [x] 4b.1 Add `Tiers map[string]string` to `SubscriptionConfig` alongside the legacy `Tier` field; the legacy field stays as the Claude tier (top-level TOML key for back-compat), the map holds every other agent's entry.
- [x] 4b.2 Add `TierFor(agentID)` / `SetTierFor(agentID, tier)` so callers route to the right field without knowing the split.
- [x] 4b.3 Replace the single `subscription.tier` row in `editableFields()` with four per-agent rows (`claude.tier`, `codex.tier`, `antigravity.tier`, `cursor.tier`), each a chip + cycle-picker with that agent's enum vocabulary. Extend `chipColorFor` with the new tier values.
- [x] 4b.4 Migrate every consumer: `dashboard.go` reads `cfg.Subscription.TierFor("claude")` and labels the Subscription block `Subscription · Claude · 5h window`; `app.go`'s `tierDetectedMsg` writes via `SetTierFor("claude", …)`; `setupwizard/wizard.go` likewise.
- [x] 4b.5 Add config tests covering `TierFor` / `SetTierFor` round-trips and the legacy-field non-mutation invariant for non-Claude agents.

## 8. Two-column master-detail layout

- [x] 8.1 In the wide layout (≥ 120), render Settings as two framed panes via `lipgloss.JoinHorizontal`: a left list pane (Moshi + grouped fields + Sleep/Daemon/Hosts) and a right detail pane for the active field. Focused pane uses `PaneFocused`.
- [x] 8.2 Move the active field's full description, enum options, and inline editor into the right detail pane; keep the single-column stacked layout (description/editor below the row) as the narrow-layout (< 120) fallback.
- [x] 8.3 Regenerate the settings goldens to capture the two-pane layout (list + detail) and the editing state.

## 9. Accurate tiers + availability gating

- [x] 9.1 Correct the per-agent tier enums: Antigravity `api / free / ai-pro / ai-ultra`, Cursor `free / pro / pro+ / ultra / teams`; extend `chipColorFor` with the new values.
- [x] 9.2 Tag each tier `editableField` with its `agent.ID`; add `settingsModel.fields()` to drop tier rows for unavailable agents (Claude always shown) and route the cursor/Update/commit/View through it.
- [x] 9.3 Detect available agents once in `App.New` (`agent.AllAvailable`) and pass the IDs via `SetAvailableAgents`.
- [x] 9.4 Replace the `;` clause separators in field descriptions with newlines.
- [x] 9.5 Update `TestEditableFields_PerAgentTiers` for the new enums; add `TestSettings_HidesUnavailableAgentTiers`.

## 6. Goldens

- [x] 6.1 Regenerate `internal/tui/testdata/golden/settings.txt` capturing the grouped sections + accent-bar active row + chips.
- [x] 6.2 Add `internal/tui/testdata/golden/settings_editing.txt` capturing the "editing field" state with the inline textinput focused.

## 7. Validate

- [x] 7.1 Run `go test ./...` and `make lint`; confirm green.
- [x] 7.2 Run `openspec validate redesign-tui-settings --type change --strict --no-interactive`.
- [x] 7.3 Run `openspec instructions apply --change redesign-tui-settings --json` and confirm `state != "blocked"`.
- [ ] 7.4 After merge: `openspec archive redesign-tui-settings --yes`.
