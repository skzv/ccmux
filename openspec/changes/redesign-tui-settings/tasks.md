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

## 4. `i` info modal

- [x] 4.1 Add `settingsInfoOverlay` model rendering ccmux version, config path, log path, last-save time, last-save error if any.
- [x] 4.2 Wire `i` key in `app.go` with `!modalCapturingText()` guard.
- [x] 4.3 Drop the `ccmux version` and `config file <path>` rows from the default Settings view.

## 5. `bubbles/spinner` for async probes

- [x] 5.1 Replace the `Detecting Moshi…` (and equivalent host-probe) muted placeholders with `bubbles/spinner`.

## 6. Goldens

- [x] 6.1 Regenerate `internal/tui/testdata/golden/settings.txt` capturing the grouped sections + accent-bar active row + chips.
- [x] 6.2 Add `internal/tui/testdata/golden/settings_editing.txt` capturing the "editing field" state with the inline textinput focused.

## 7. Validate

- [x] 7.1 Run `go test ./...` and `make lint`; confirm green.
- [x] 7.2 Run `openspec validate redesign-tui-settings --type change --strict --no-interactive`.
- [x] 7.3 Run `openspec instructions apply --change redesign-tui-settings --json` and confirm `state != "blocked"`.
- [ ] 7.4 After merge: `openspec archive redesign-tui-settings --yes`.
