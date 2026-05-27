# TUI Design System

ccmux's TUI is built on the Charm stack (Bubble Tea, Lipgloss, Bubbles,
Huh, Glamour). This page describes the rules every screen must follow
so the TUI stays visually coherent as features land.

> **TL;DR.** Tokens live in `internal/tui/styles/`. Shared chrome lives
> in `internal/tui/components/`. Screens import from these two packages
> and never construct raw colours or hand-rolled padding integers. The
> rule is enforced by `TestNoInlineStyleLiteralsInScreens`.

## Layers

```
internal/tui/styles/        ← palette + tokens + the Styles aggregate
internal/tui/components/    ← shared render helpers (Header / HelpBar / List)
internal/tui/                ← screens consume from both packages only
```

Strict one-way dependency: `screens → components → styles`. The lint
test fails any screen file outside `styles/` or `components/` that
introduces a `lipgloss.Color("#…")` literal or a bare integer inside a
`.Padding(…)` / `.Margin(…)` / `.Padding<Side>(…)` / `.Margin<Side>(…)`
call.

## Tokens (`internal/tui/styles/`)

Three files:

- **`palette.go`** — the `Palette` type and `DefaultPalette` (Catppuccin
  Mocha values). A theme swap is a new `Palette` value passed to
  `FromPalette`.
- **`tokens.go`** — palette-independent and palette-derived tokens:
  - `SpacingScale{XS=0, SM=1, MD=2, LG=3, XL=4}` — cell counts for any
    `.Padding(…)` / `.Margin(…)` argument.
  - `RadiusSet{Soft}` — the rounded border every pane uses.
  - `TypographyRoles{Display, Title, Subtitle, Body, Caption, Code}` —
    role-named lipgloss styles derived from the palette.
  - `SemanticColors{Primary, Success, Warning, Danger, Info, Muted, Accent}` —
    intent-named colours derived from the palette.
  - `Matrix*Style` exports for the easter-egg overlay (theme-invariant
    by design — the joke is "The Matrix", not "the current palette").
- **`styles.go`** — the `Styles` aggregate every screen consumes via
  `styles.Default()`. Exposes the tokens above as `s.Spacing`,
  `s.Radius`, `s.Type`, `s.Semantic`, plus the pre-built styles
  (`s.Pane`, `s.StatusGood`, `s.ListItemSelected`, etc.) screens
  use directly. `s.P` gives raw palette access for the few cases
  that need a colour ramp (host-name hashing, gradient lookups).

### Picking the right surface

| Want                         | Use                                                                                                                                  |
| ---------------------------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| A "this is the title" colour | `s.Type.Title` or `s.Emphasis`                                                                                                       |
| A "this is muted" colour     | `s.Type.Subtitle` or `s.Muted`                                                                                                       |
| A "this means good" colour   | `s.Semantic.Success` (or `s.StatusGood`)                                                                                             |
| A spacing value              | `s.Spacing.{XS,SM,MD,LG,XL}`                                                                                                         |
| The pane border              | `s.Pane` (the whole style — borders, padding)                                                                                        |
| A row's selected treatment   | `components.RenderListRow(s, content, true, w)`                                                                                      |
| A new screen-level chip      | Wrap in `[brackets]` with a foreground colour — avoid CSS-padded chips inside fixed-width terminal boxes since they break alignment. |

## Components (`internal/tui/components/`)

Three render helpers, all stateless pure functions of `(styles.Styles, props) string`:

- **`Header(s, HeaderProps)`** — two-line screen-level title bar with
  a left slot (Title + optional Breadcrumb) and a right slot (Chips),
  plus an accent rule beneath. **Opt-in**: screens use it when a
  breadcrumb adds information not already on the tab strip or status
  bar; the home/Dashboard screen for example does _not_ use it because
  the tab strip already says "Sessions" and the status bar already
  carries the daemon / host chips.
- **`HelpBar(s, HelpBarProps)`** — the priority-driven help line at the
  bottom of every screen. Replaces the legacy hardcoded `? help · q
quit · r refresh …` string. Each screen exposes its own
  `HelpBarProps` so the line is context-aware (Conversations advertises
  `H headless: hidden/shown`, Notes advertises `/ search`, etc.) and
  collapses gracefully on narrow terminals by dropping the lowest-
  priority entries first.
- **`List[T any](s, ListProps[T])`** + **`RenderListRow(s, content, selected, width)`** —
  the unified selectable-row treatment. Selected row gets a 1-cell
  accent bar on the left (`▌` in `s.Semantic.Accent`) plus an elevated
  background fill that extends to the pane edge. `List[T]` is for
  simple lists; `RenderListRow` is for screens with interstitial
  subheaders (Notes folder groups, Projects host groups,
  Conversations agent sections).

### Header / HelpBar / List contract

- **Consistent height across screens.** When two screens render their
  Header at the same width, the Header occupies the same number of
  lines. Same for HelpBar.
- **Adaptive collapse order.** Header right-slot chips drop first;
  HelpBar entries drop by ascending priority; list secondary
  metadata hides on the narrowest widths. The primary content
  identifier (screen title, row's primary text) survives at any
  width ≥ 40 columns.
- **Selection is identical across screens.** Sessions, Conversations,
  Projects, Notes, ProjectMenu, the ClaudeConfig picker — they all
  use `components.RenderListRow` and look the same selected.

## Modal / overlay exception

Full-screen overlays and modal forms (Tour, confirmation modals,
Huh-based forms, the Matrix easter egg, the `?` help overlay, the
`u` usage overlay) MAY omit the shared `components.Header` and
`components.HelpBar` when doing so would distort the overlay's
intent — they still consume tokens from `styles/` for every colour,
spacing value, and border, and they MUST NOT introduce inline
literals (the lint rule still applies).

## Usage panel hierarchy (the indent rule)

The Dashboard's `Usage` panel demonstrates the design system's
indent contract:

```
Usage                                ← panel title (0 indent)
 Claude · 5h window                  ← agent heading (1 indent)
   47 / 225 (est.) prompts · 21%     ← agent body (3 indent)
   █████░░░░░░░░░░░░░░░░░░░░
   resets in 4h 57m

   Cost · billing block              ← Claude sub-section (3 indent, peer of agent body)
     $48.21 spent · $9.2/hr          ← sub-section body (5 indent)
     projected $73.18 by 19:30

   Tokens · this window
     1.20M in · 380.0K out · 2.10M cache hit

 Codex · recent                      ← peer agent heading (1 indent)
   no conversations yet              ← peer agent body (3 indent)
```

Two-cell indent step at every level. Sub-sections nest under their
owning agent (Cost and Tokens are Claude-specific data, so they live
under Claude rather than as siblings).

## Visual regression coverage

Every primary navigation screen has a `teatest` golden test that
snapshots its render at a canonical terminal size (120 × 40 for most;
119 × 40 for Conversations to avoid the absolute-timestamp drift in
its detail pane).

- Goldens live in `internal/tui/testdata/golden/`.
- Regenerate after a deliberate visual change with `CCMUX_UPDATE_GOLDEN=1 go test ./internal/tui/...`; review the diff before committing.
- The helper (`goldenAssert`) round-trips raw ANSI bytes so the
  snapshot captures exactly what a terminal would render.

See `internal/tui/testdata/golden/README.md` for the full workflow.

## Lint rules

Two tests enforce the system:

- **`TestNoInlineStyleLiteralsInScreens`** (`internal/tui/styles_lint_test.go`) —
  fails the build if any screen file introduces a literal hex colour
  or a bare integer in `.Padding(…)` / `.Margin(…)` / `.Padding<Side>(…)` /
  `.Margin<Side>(…)`.
- **Per-component snapshot tests** (`internal/tui/components/*_test.go`) —
  pin the Header / HelpBar / List rendering at 120, 100, 80, 60, and 40
  columns to catch silent breakage when the components themselves change.

## Where the redesign relaxed the original spec

The original `redesign-tui-charm` proposal required every primary screen
to render its top frame through `components.Header`. The Phase 3 visual
checkpoint surfaced that on the Dashboard, the per-screen Header
duplicated the tab strip's `Sessions` label and the status bar's `N sess`
count — competing with the body content for vertical space without adding
information. The spec was relaxed to make Header **opt-in**: the component
is available for screens that have a genuine breadcrumb (a Notes detail
showing the active file path; a Conversations detail showing the active
project filter), but is not forced on screens whose identity is already
carried by the tab strip.
