## Why

PR #114 (`redesign-tui-charm`) added per-screen `HelpBarProps` to Settings (dropping the silent-no-op `r refresh` advert) but otherwise didn't touch the screen. Settings is a flat list of editable fields with sleep-prevention + hosts blocks tacked on at the bottom. There's no visual grouping — `projects.root`, `subscription.tier`, `agents.default`, the Sleep block, and the Hosts list all live at the same indent level competing for the eye.

Two more issues specific to Settings:

- It's not a `components.List` surface (it's an editable form), so it didn't get the unified selection treatment in PR #114. The cursor on the active field today renders as `▸ ` (lavender bold) — distinctive but inconsistent with the accent-bar treatment every other selectable surface uses.
- The Moshi block at the top is checked every 30s via an async probe, but during the probe the block renders without a spinner. Same for host probes.

This change groups the fields into sub-sections, applies the design-system selection treatment to the active field, and adds spinners for the async probes.

## What Changes

- **Sub-section grouping**: split the editable fields into three labeled groups (`Subscription`, `Projects`, `Agents`). Sleep prevention and Hosts already render as their own pseudo-sections; align their headings with the design-system 2-cell indent step and use `s.Type.Subtitle` for the section labels.
- **Accent-bar field selection**: replace the `▸ ` cursor marker with the design-system accent-bar treatment (`▌ ` + elevated background) so the active field is signalled the same way as a selected list row elsewhere in the TUI. Reuse `components.RenderListRow` or an inline equivalent so the visual treatment matches exactly.
- **HelpBar audit**: pin the existing entries. Add `enter` (edit field) and `esc` (cancel edit) when the inline editor is open.
- **Field value chips for boolean / enum fields**: render the current value as a chip (`[max5x]`, `[claude]`, `[off]`) so the field's purpose is readable without consulting `?` help.
- **`bubbles/spinner` for Moshi probe + host probes**: replace `Detecting Moshi…` and the equivalent host placeholders with a real spinner widget.
- **Read-only metadata moved to an info modal**: `ccmux version` and `config file <path>` are reference info, not actionable. Move behind an `i` keybind (parallel to the new `i` modals on other tabs).
- **Per-screen golden refresh**: regenerate `internal/tui/testdata/golden/settings.txt`. Add a second golden capturing the "editing field" state (inline textinput focused).

**Non-goals:**

- No changes to the config file format or to which fields are editable.
- No "reset to defaults" or "diff vs defaults" features. Reasonable follow-ups; out of scope.
- No multi-pane layout. Settings stays a single column.

## Capabilities

### Modified Capabilities

- `tui-design-system`: adds Settings-specific scenarios for sub-section field grouping, accent-bar treatment on editable-form active rows (consistent with selectable-list rows elsewhere), and chip rendering for fixed-enum field values.

## Impact

- **Affected code:** `internal/tui/settings.go` (View grouping, cursor treatment, chip rendering, spinner integration, `i` modal trigger), `internal/tui/app.go` (overlay routing for `i`).
- **Tests:** existing `settings_test.go` stays; one new test for chip rendering; `settings.txt` golden regenerates; new `settings_editing.txt` golden.
- **Dependencies:** no new third-party.
- **User-visible:** Settings reads as three labeled sections; active field uses the same selection idiom as everything else; version + config-path move behind `i`.
- **CLI:** no changes.
