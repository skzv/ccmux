## Context

Settings is an editable-form screen, not a `components.List` surface. The cursor moves over a flat sequence of fields (`projects.root`, `subscription.tier`, `agents.default`, …) with Enter opening an inline textinput, Enter committing, Esc cancelling. Multi-line fields launch `$EDITOR` against `~/.config/ccmux/config.toml`. Sleep prevention and Hosts render as ad-hoc bottom blocks.

PR #114 added `settingsModel.HelpBarProps` (`? help`, `q quit`, `e edit config`, `1-7 screens`), dropped the silent-no-op `r refresh` advert, and pinned the wide-mode `config file <path>` line via `SetCfgPath` so the golden doesn't drift across machines. The screen's structure is untouched.

The visual gap: every field competes equally for the eye. The cursor marker (`▸ `) is distinctive but inconsistent with the accent-bar treatment every list-bearing screen now uses. The Moshi block renders without a spinner during its async probe. Reference info (`ccmux version`, `config file <path>`) sits at the top of the wide layout where actionable content should be.

## Goals / Non-Goals

**Goals:**

- Make Settings read as labeled sections (`Subscription`, `Projects`, `Agents`, `Sleep prevention`, `Hosts`) instead of a flat list.
- Apply the design-system accent-bar treatment to the active field so Settings looks like the same TUI as every other selectable surface.
- Use spinners for the async probes that already exist.
- Move reference metadata (version, config path) behind `i`.

**Non-Goals:**

- Config file format changes. `internal/config` is read-only from this change's perspective.
- New editable fields. The set stays the same.
- A diff-vs-defaults or reset-to-defaults UI. Reasonable follow-up, out of scope.

## Decisions

### Field grouping

**Decision:** Group `editableFields()` into three logical sub-sections in the order:

1. `Subscription` — `subscription.tier`
2. `Projects` — `projects.root`
3. `Agents` — `agents.default`

Render each sub-section with `s.Type.Subtitle` heading and the design-system 2-cell indent. The existing Sleep prevention block becomes a fourth labeled section; Hosts becomes a fifth.

**Rationale:** Three top-of-screen fields is small enough that flat works, but adding more in the future (sleep tunables, host management) is where the absence of grouping bites. Setting the precedent now is cheap.

**Alternatives:**

- _Keep flat._ Easier, but the existing ad-hoc Sleep / Hosts sections already break the flat assumption — grouping makes the structure honest.
- _Tabbed sub-sections._ Overkill; we have 5 sections, not 50.

### Active-field treatment

**Decision:** Replace the `▸ ` cursor with `components.RenderListRow(s, "<label>  <value>", isActive, paneInner)`. The active field gets the accent-bar prefix and elevated background; the inactive fields render with the 2-space prefix.

**Rationale:** Unifies the selection idiom across every selectable surface. The form is conceptually a list; rendering it that way removes a per-screen vocabulary mismatch.

### Chip rendering for fixed-enum values

**Decision:** For boolean and enum-typed fields (e.g., `subscription.tier`, `agents.default`, `sleep.mode`), render the current value as a chip: `[max5x]`, `[claude]`, `[off]`. Active-row chips use `s.Semantic.Accent` foreground; off-row chips stay muted.

**Rationale:** Today the value renders inline as plain text. The chip shape signals "this is a fixed-set value, not free-form" without requiring the user to consult `?` help.

### `i` info modal

**Decision:** Move `ccmux version`, `config file <path>`, and `log file <path>` behind an `i` keybind that opens an overlay with version, config path, log path, last-config-save time, last-save error if any.

**Rationale:** The top-of-screen real estate is better used for the editable groups. The metadata is reference info; one keystroke away is fine.

## Risks / Trade-offs

- **[Risk] Grouping with three top-level fields feels sparse.** → Mitigation: accept the sparsity now so adding fields later doesn't force a layout migration.
- **[Trade-off] Hiding the version / config-path is a discoverability loss for new users.** → Justified: the `?` help overlay surfaces `i` for info; first-launch tour can mention it.

## Migration Plan

1. Wrap `editableFields()` into a grouped structure (data only; no render change yet).
2. Update `View()` to render grouped sections + accent-bar field treatment.
3. Add chip rendering for fixed-enum fields.
4. Add the `i` overlay.
5. Add the spinner during Moshi probe.
6. Regenerate the `settings.txt` golden + add the `settings_editing.txt` variant.

Rollback: revert. No persisted state changes.

## Open Questions

- Should the `Hosts` section move into the Network tab entirely? It already overlaps with what Network shows. **Tentative:** No — Settings shows configured hosts (the user's `cfg.Hosts` list); Network shows discovered + configured + mobile. Different surfaces.
