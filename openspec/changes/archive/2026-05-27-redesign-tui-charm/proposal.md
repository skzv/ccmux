## Why

The current TUI works but feels busy and ad-hoc: each screen builds its own header, footer, and list rendering on top of a thin shared `Styles` set, panels compete visually instead of cooperating, and there is no contract that prevents new screens from drifting further. We want ccmux to feel like a first-class Charm-stack application (in the spirit of soft-serve, glow, and crush) so the daily-driver TUI reads as elegant and intentional without losing any of the information it shows today.

## What Changes

- Introduce a formal design-token layer in `internal/tui/styles/` so palette, spacing, typography roles, border radii, and semantic colors are the single source of truth. Screens MUST consume tokens; inline hex/spacing is forbidden.
- Ship one polished default theme built on the tokens. No theme picker or multi-theme support in this change (deferred).
- Add a shared `Header` component (project/screen context on the left, status chips / key hints on the right, consistent height and accent). **Opt-in**, not mandatory: revised from the original proposal after the Phase 3 visual review found that a per-screen Header row on the home/Dashboard duplicated the tab strip's `Sessions` label and the status bar's session count, which worked against the "calmer chrome" goal. Header is now available to screens that have a genuine breadcrumb (e.g., a Notes detail showing the active file path) but is not required for screens whose identity is fully carried by the tab strip.
- Add a shared `Footer` / `HelpBar` component that renders a context-aware shortcut list with consistent separators, accent, and graceful narrowing. **Mandatory** — replaces the legacy hardcoded `? help • q quit • r refresh …` string at the bottom of every screen.
- Add a shared selectable-list rendering pattern used by Sessions, Conversations, Projects, and Notes: accent-bar selection (not a heavy border), dim secondary metadata, optional second-line description, identical hit-area across screens.
- Restyle every existing screen (Dashboard, Sessions, Conversations, Projects, Notes, Settings, Tour, and all modals/forms) on top of the new tokens and components while preserving all currently-visible information. **Non-goal: hiding panels or reducing information density.**
- Add `teatest` golden-file visual-regression tests for every redesigned screen at a fixed terminal size so future style drift fails CI.
- Update the README + relevant `docs/` to describe the design tokens and component contracts so contributors know where to add styles.

**Non-goals (called out so the spec stays scoped):**

- No new TUI features, screens, or keybindings.
- No multi-theme system or theme picker.
- No information-density reduction on the Dashboard or anywhere else.
- No changes to the daemon, CLI surface, or `internal/` packages outside `internal/tui/`.

## Capabilities

### New Capabilities

- `tui-design-system`: Defines the design tokens (palette, spacing, typography, radii, semantic colors), the shared component contracts (Header, Footer/HelpBar, selectable list), and the cross-screen styling rules ccmux's TUI must follow. Owns the "every screen consumes tokens, no inline colors/spacing" contract and the golden-file coverage requirement.

### Modified Capabilities

<!-- None: existing capabilities (tui-session-management, adaptive-screen-layout, conversation-agent-grouping, etc.) describe behavior, not visual styling. The visual restyle does not change their requirements. -->

## Impact

- **Affected code:** all files under `internal/tui/` (every screen file plus `internal/tui/styles/`). Likely introduces `internal/tui/components/` (or expands `internal/tui/styles/`) to host the shared Header / Footer / List helpers. No changes to `cmd/`, `internal/daemon/`, `internal/tmux/`, `internal/claude/`, `internal/project/`, or `internal/notes/`.
- **Tests:** new golden files under each redesigned screen's `_test.go` (or a dedicated `internal/tui/golden/` package), regeneration gated on an env flag. Existing screen tests stay; any tests that asserted incidental style strings get rewritten to semantic checks.
- **Docs:** README's screenshots refresh; `docs/02_Architecture/` gets a short "TUI design system" page; `CLAUDE.md`'s styling rule (`Styling: all colors and shapes live in internal/tui/styles. Never hard-code a color in a screen file.`) tightens to also forbid inline spacing.
- **Dependencies:** no new third-party dependencies expected; uses Lipgloss / Bubbles / teatest already in `go.mod`.
- **User-visible:** the visual appearance of every screen changes. No keybindings, navigation flows, or command semantics change. Users who scripted against `ccmux` CLI output are unaffected because only the TUI rendering is touched.
