## Context

ccmux's TUI is built on the Charm stack (Bubble Tea, Lipgloss, Bubbles, Glamour, Huh) and already has a thin token layer at `internal/tui/styles/styles.go`: a `Palette` (Catppuccin Mocha) and a derived `Styles` struct with named lipgloss styles. The CLAUDE.md rule says screens must consume from `styles/`, but in practice:

- Each screen file builds its own headers, footers, list rendering, panel layouts, and status chips. There are roughly 8+ bespoke header/footer treatments across `app.go`, `dashboard.go`, `sessions.go`, `conversations.go`, `projects.go`, `notes.go`, `settings.go`, and the various modal forms.
- Spacing (padding, margins, gaps between panels) is hardcoded per screen.
- Selection treatment varies: some screens use a background fill, some use a border, some use a Lavender foreground accent.
- Reference Charm projects the user pointed to (`https://charm.land/`, plus soft-serve, gum, glow, crush) share a recognizable design language: rounded borders, generous interior padding, single-accent palettes, dim secondary text, accent-bar selection, status chips with consistent height.

The existing `Styles.Pane`, `Styles.ListItemSelected`, `Styles.Title`, etc. are a starting point but not a contract. The redesign promotes the token layer into a proper design system with shared components, while preserving the existing screen behaviors and information density.

## Goals / Non-Goals

**Goals:**

- Make every screen consume from a single, named token layer (palette + spacing + typography + radii + semantic).
- Make every screen render headers, footers, and selectable lists through shared components, eliminating bespoke header/footer/list code from individual screens.
- Make the default theme feel intentional and Charm-native (think soft-serve / glow / crush) without losing information.
- Make visual drift catchable in CI via `teatest` golden files at a fixed terminal size.
- Make the contract clear for future contributors: where styles live, what's allowed inline, what's not.

**Non-Goals:**

- A theme picker, multi-theme support, or runtime theme switching.
- Reducing information density (no hiding the usage panel, agent stats, quota bar, etc.).
- New TUI features, screens, or keybindings.
- Touching anything outside `internal/tui/`, README, and the design-system doc page.
- Replacing Bubble Tea / Lipgloss / Bubbles / Glamour / Huh with anything else.

## Decisions

### Token layer location and shape

**Decision:** Keep tokens in `internal/tui/styles/` but split the single `styles.go` into focused files:

- `palette.go` — `Palette` struct + the default palette (rename `CatppuccinMocha` to the chosen default; see "Default theme identity" below).
- `tokens.go` — non-color tokens: `Spacing` (XS=0, SM=1, MD=2, LG=3, XL=4 in cells), `Radius` (Soft = lipgloss.RoundedBorder()), `Typography` roles (Display, Title, Subtitle, Body, Caption, Code as named lipgloss.Style values), and `Semantic` (Primary, Success, Warning, Danger, Info, Muted, Accent).
- `styles.go` — the derived `Styles` struct that screens consume, expanded to expose the new tokens and component-style slots.

**Rationale:** Tokens-first separation matches how Charm's own libraries and downstream projects organize style sources (see soft-serve's `ui/styles` directory). It also makes the "no inline hex, no inline spacing" rule mechanically checkable: tokens and semantic colors are imported by name; if a screen references `lipgloss.Color("#…")` directly, that's the lint signal.

**Alternatives considered:**

- _Single file, more constants._ Lower diff, but does not establish the conceptual separation between palette (theme-swappable) and tokens (theme-invariant), which matters when we eventually add multi-theme support.
- _External package (`pkg/charmcss` etc.)._ Premature; tokens are private to the TUI.

### Default theme identity

**Decision:** Keep Catppuccin Mocha as the default palette (it already ships and looks intentional), but rename the exported value from `CatppuccinMocha` to `Default` and make the file structure theme-agnostic so a future palette can be dropped in without renames. Tune one or two values (specifically `BG` and `Border`) to give the redesigned components more contrast against the background.

**Rationale:** The user said one polished default theme; Catppuccin Mocha is already polished and familiar. Renaming establishes the door for multi-theme work later without forcing it now.

### Component layer

**Decision:** Introduce a new package `internal/tui/components/` that contains the shared Header, Footer/HelpBar, and List render helpers. Screens import this package and pass content into the components. Components depend on `internal/tui/styles` for tokens; they have no other internal dependencies.

- `components.Header(styles.Styles, HeaderProps) string` — renders a one-line header with a left slot (title + breadcrumb), right slot (status chips), and an accent rule.
- `components.HelpBar(styles.Styles, HelpBarProps) string` — renders the footer hint line; takes a `[]KeyHint{Key, Label}` and a max width. Narrows by dropping lower-priority hints (caller supplies priority).
- `components.List(styles.Styles, ListProps[T]) string` — renders a selectable list with accent-bar selection. Each item supplies primary text, optional secondary text, and optional trailing metadata. This is a render helper, NOT a wrapper around `bubbles/list` (the existing screens manage their own selection state; we just standardize how rows look).

**Rationale:** A package separate from `styles` keeps the dependency graph one-way (components → styles, screens → both). Function-style render helpers (rather than Bubble Tea sub-models) avoid pulling state into shared code; selection still lives in each screen's model.

**Alternatives considered:**

- _Sub-models implementing `tea.Model`._ Would let the components own state, but at the cost of plumbing all screens through new message types; for static rendering that adds no value here.
- _Embed inside `internal/tui/styles`._ Mixes the "passive tokens" and "active rendering" responsibilities; harder to reason about.

### Selection treatment

**Decision:** Selection uses a 1-cell accent bar on the left + slightly elevated background. No heavy border, no all-caps, no bold-everything. Secondary metadata renders in `FGMuted`.

**Rationale:** This is the soft-serve/crush convention and looks calmer than the current "Lavender foreground, Selected background" approach. Single accent across the whole TUI ensures users always recognize "the selected row" regardless of screen.

### Header / footer contract

**Decision:** Every screen renders exactly one header (top) and one footer (bottom) via the shared components. The header always shows: app name + current screen + (when relevant) project/session context on the left, and screen-specific status chips on the right. The footer always shows: a context-aware hint list of key bindings, with a `?` hint reserved for full help where applicable.

**Rationale:** Predictable framing is a major part of why Charm apps feel cohesive. Standardizing the slots also makes width-adaptation easier: narrow widths drop right-slot chips first, then footer hints by priority, never the primary content identifier.

### Adaptive screen layout interaction

**Decision:** The new components honor the existing single-breakpoint rule from `adaptive-screen-layout` (narrow = width < 120). Header right-slot chips collapse first; footer hints collapse by descending priority; list secondary text hides at very narrow widths. No new breakpoints introduced.

**Rationale:** Keeps the existing `adaptive-screen-layout` spec unchanged; redesign is purely styling and component composition.

### Visual regression testing

**Decision:** Per-screen golden-file tests live alongside the screen's existing test file (e.g., `internal/tui/dashboard_test.go` gets a `TestDashboardGolden` that snapshots the rendered view at 120x40). Goldens regenerate when `CCMUX_UPDATE_GOLDEN=1` is set. The golden files live under `internal/tui/testdata/golden/<screen>.txt`.

**Rationale:** Co-located tests keep the diff scoped; `teatest` already supports this pattern. The regenerate flag follows Go convention.

**Alternatives considered:**

- _Single mega snapshot per screen at multiple widths._ Defer; the existing `adaptive-screen-layout` width tests already cover correctness at narrow widths. Style goldens at one canonical width keep diffs readable.

### Migration order

**Decision:** Land the change in five phases (visible in `tasks.md`):

1. Tokens layer + theme rename.
2. Components package (Header, HelpBar, List).
3. Migrate Dashboard (highest-visibility, most-busy screen) and capture a golden.
4. Migrate remaining screens in a single sweep, each with its golden.
5. Docs + README screenshot refresh.

**Rationale:** Phase 3 acts as a confidence checkpoint — if the Dashboard restyle looks wrong, we catch it before fanning out across every screen.

## Risks / Trade-offs

- **[Risk] Golden-file churn becomes noisy on unrelated PRs.** → Mitigation: golden tests only at a single canonical width per screen; `CCMUX_UPDATE_GOLDEN=1` workflow documented; CI fails closed but locally regen is one command.
- **[Risk] Information density preserved + Charm aesthetic = visual conflict.** Charm's design language leans on whitespace, but the user wants every panel visible. → Mitigation: keep panels but tighten via tokenized spacing (consistent `Spacing.MD`-cell gutters); rely on calmer color choices and quieter chrome to reduce visual noise without removing data. Capture the Dashboard golden first to validate.
- **[Risk] Header/Footer abstraction is leaky.** Some screens (Tour, modals, Huh forms) won't fit the standard header/footer slots cleanly. → Mitigation: spec calls out modals and full-screen overlays as exceptions; they still consume tokens but may omit the standard header/footer.
- **[Risk] Refactoring while other branches are in flight causes merge conflicts.** Multiple worktrees exist (`codex/group-conversations-by-agent`, `codex/cursor-support`, etc.). → Mitigation: this change touches `internal/tui/styles/`, every screen file, and adds a new components package; we accept that this will conflict with any in-flight UI work and plan to land it during a quiet window. Out of scope for this planning artifact.
- **[Trade-off] No multi-theme support.** Users with personal palette preferences must wait. → Justified: the token layer is structured to make the future addition mechanical (drop in a new `Palette`, expose a config-key in Settings).
- **[Trade-off] Render-helper functions, not Bubble Tea sub-models, for shared components.** Loses encapsulation but keeps the change purely about rendering; matches how the current code already passes view strings around.

## Migration Plan

1. Add the new tokens + components packages without removing existing code. Verify `go build ./...` and `go test ./...` are still green.
2. Migrate the Dashboard screen first; commit and run goldens.
3. Migrate the remaining screens in order: Sessions, Conversations, Projects, Notes, Settings, ProjectMenu, NewProject, NewSession, RenameForm, Confirmation modals, Tour, ClaudeConfig, AntigravityConfig, CodexConfig.
4. Remove dead styles from `Styles` once no screen consumes them. Run staticcheck.
5. Update README screenshots + add a short design-system page under `docs/02_Architecture/`.
6. Tighten `CLAUDE.md`'s styling rule to also forbid inline spacing.

**Rollback:** the change is purely additive plus restyling; reverting the commit restores prior visuals. No data migrations, no daemon protocol changes.

## Open Questions

- Should the design-system doc live at `docs/02_Architecture/04_TUI_Design_System.md` or under `docs/04_Guides/`? **Tentative:** Architecture (it's a contract, not a how-to).
- Do we want a small visual "before/after" gallery in the PR description, or just the README screenshots? **Tentative:** README screenshots + a couple of PR-only before/after captures.
- Are there modal/form screens that should keep their own header style (e.g., Tour)? Decide during phase 2 when the components package solidifies. The spec must therefore allow a narrow exception for full-screen overlays.
