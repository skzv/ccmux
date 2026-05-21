## Context

ccmux's TUI is built on Bubble Tea + Lipgloss. Terminal width arrives via `tea.WindowSizeMsg`, is stored in `App.width`, and each screen's `View(width, height int)` receives it. Screens already branch on `isNarrow(width)` (`width < 80`, defined `projects.go:541`) — but that branch only changes **geometry**: stack panels vertically vs. lay them side-by-side. The *content* of every panel is identical at 50 columns and 250 columns. `usagePanel` renders the same ~20 lines regardless of width.

iPhone terminal emulators render narrow — portrait ~40–65 columns, landscape ~90–110. On a phone the densest panels (usage especially) overflow the screen, and Home stacks panels bottom-heavy so the most time-sensitive content (which sessions need input) is starved of vertical space while reference data (token-cost breakdown, agent install hints) hogs it.

One panel already does the right thing: `renderSessionLine` (`dashboard.go:679`) gates the `@host` tag behind `inner > 50` and the `[agent]` tag behind `inner > 60` — genuine progressive disclosure. This change generalizes that line-level pattern to the panel and screen level: on a narrow screen, show a *curated subset* of content, not a reflowed copy of the desktop screen.

## Goals / Non-Goals

**Goals:**
- The no-overflow contract holds at every width: no rendered line exceeds the terminal width.
- Below the narrow breakpoint, secondary content is **omitted** (curated away), not merely reflowed — the phone shows a different, shorter screen.
- A content-priority model (T0/T1/T2) classifies every screen element; the renderers read the classification off the table in this document.
- Width-sweep tests assert both no-overflow *and* that T2 content is absent on narrow.

**Non-Goals:**
- A reveal affordance for hidden content — no expand key, no toggle, no "press → for more". T2 is simply absent on narrow (see Decision 3). The CLI is the escape hatch.
- A second breakpoint. One breakpoint only — narrow or wide, nothing between.
- Horizontal scrolling or pagination inside panels.
- Supporting widths below ~40 columns.
- Dynamic emoji-width correction (±2 col tolerance for multi-byte runes accepted).

## Decisions

### Decision 1: One breakpoint — `isNarrow` becomes `width < 120`

**Chosen:** change the existing `isNarrow(width) bool` from `width < 80` to `width < 120`. One breakpoint, applied everywhere through that single function.

**Rationale:** 120 catches the *whole phone* — portrait (40–65 cols) and landscape (~90–110 cols) both fall below it, so there is no width band where a real phone gets the cramped desktop layout. `isNarrow` is already the shared branch for the header, dashboard, projects, notes, and sessions; moving the constant updates every caller at once with no new symbol. An earlier draft kept 80 to avoid reclassifying desktop split panes; the call here is that an 80–119-col pane getting the *curated* narrow layout is fine — an improvement, even, since the curated view is good, not degraded.

**Consequence:** a desktop tmux pane between 80 and 119 columns now renders the narrow (curated) layout. This is intentional. The wide layout is reserved for genuinely wide terminals (≥ 120); everything below gets the phone-grade curated view.

**Cleanup required:** `conversations.go:249` branches on `detailW < 20` (effective width ≈ 56 cols), *not* `isNarrow` — an accidental third breakpoint. Unify it to `isNarrow` so Conversations goes narrow at the same width as every other screen.

### Decision 2: Content-priority model — T0 / T1 / T2

Every renderable element on every screen is assigned a tier:

| Tier | Name | Narrow (`< 120`) | Wide (`≥ 120`) |
|------|------|------------------|----------------|
| **T0** | glanceable | rendered | rendered |
| **T1** | useful | rendered, condensed (often one line) | rendered, full |
| **T2** | reference | **hidden** | rendered |

The per-element classification in the **Classification** section below is the contract the renderers implement against. A flat tier tag per element is deliberately chosen over fluid per-element width budgeting: `renderSessionLine`'s `inner > N` gates are right for *one line*, but a screen has dozens of elements — dozens of magic-number thresholds would be unreadable and untestable. `renderSessionLine` keeps its line-level gates as a fine T1-condensing implementation; the screen level uses tiers.

### Decision 3: T2 is hidden, not reachable

On narrow, T2 content is simply **absent** — no expand key, no toggle, no detail overlay.

**Rationale:** cheapest possible implementation — each T2 element becomes a width check that returns `""`. No new keybindings, no expand/collapse state, no modal. Ship it, then learn from what is actually missed.

**Not a one-way door:** elements are *tagged* T2 in the model; "hidden" is only how T2 renders *today*. Adding a reveal affordance later (a detail overlay, or making T2 reachable behind a key) is a localized change — the model already records what is T2.

**Escape hatch:** genuinely-needed hidden detail stays reachable via the CLI (`ccmux ...`), consistent with the repo's feature-surface policy. A mobile user who needs the token cache breakdown runs the CLI.

### Decision 4: Curate content first, then the height math stops being bottom-heavy

`homeView` gives the sessions list only the *leftover* height after the stats/devices/usage tiles claim theirs. Once `usagePanel` collapses to one line on narrow (and the hero/devices help text is dropped), the tiles shrink and the sessions list — a T0 element — gets the vertical space it deserves. The height arithmetic itself does not change; curation makes it stop starving T0.

The no-overflow width clamp still applies at every width. Curation changes *what* renders; `lipgloss` width handling on each composed panel still guarantees *no overflow*. Measure with `lipgloss.Width()` (ANSI-stripped), never `len()`.

### Decision 5: Chrome rows curate, then clamp

The three persistent chrome rows — header (`renderHeader`), status bar (`renderStatusBar`), footer (`renderFooter`) — wrap every screen, and today each collapses differently: the header curates (drops labels, keeps numbers), but the status bar amputates (drops its whole right block as one unit) and the footer hard-truncates (`forceSingleLine` cuts left-to-right). A blind right-cut on the footer removes `? help` — the T0 gateway to every screen feature and the escape hatch for everything the screens hide on narrow.

**Chosen:** every chrome row follows the same T0/T1/T2 model as the screen bodies. Each row composes a curated string for the current width tier *first* (drop T2, condense T1); `forceSingleLine` stays only as the final overflow net. Within each row, content is ordered **T0-first** so that if the net does fire, truncation removes T2 before T0/T1.

**Rationale:** `forceSingleLine` guarantees "never overflow" but says nothing about *what* survives — left-to-right truncation keeps whatever happens to sit on the left. Ordering by importance turns the safety net from "keeps the arbitrary prefix" into "keeps the important prefix." It is the seatbelt; the curated string is the steering.

**Not in this pass:** moving toasts out of the footer into a centered overlay, and dropping the footer row entirely on narrow to reclaim a body row. Both are viable and would compound the win, but they are layout reshapes, not curation — deferred. A long error toast that still overflows one line on a phone is tolerated for now; its full text is preserved in the `?` activity log.

### Decision 6: Home is two columns on a monitor; `narrow` is terminal-derived

Below the breakpoint the Home screen is a single full-width column (Decision 4). At or above the breakpoint it splits into two halves — the sessions list+detail on the left, the hero and the three stat tiles stacked on the right. This uses the monitor's horizontal space instead of forcing a tall single column the user must scroll.

**The trap this exposes:** a half-width column on a 200-col monitor is ~100 cols — itself *below* the 120 breakpoint. A panel that decides "am I narrow?" from its *own* width would then curate away its reference content *on a monitor*, and a detail sub-pane would collapse to its phone form on a full screen (the reported `renderDetail` bug). Width tells a component how much room it has to *wrap/truncate*; it must never be the source of the *curate* decision.

**Chosen:** the narrow/wide decision is always made once from the *terminal* width and propagated down — `dashboardModel` carries a `narrow bool` field set by `homeView`, and `sessionsModel.View` / `renderDetail` / `projectsModel.renderList` / `notesModel.renderList` take an explicit `narrow bool` parameter. No render path re-derives narrowness from a sub-component width.

## Classification

`hide` = omitted on narrow. `condense` = rendered on narrow but shortened. `keep` = rendered as-is at all widths.

### Home — hero (`heroPanel`)
| Element | Tier | Narrow |
|---|---|---|
| "Hello." title + welcome subtitle | T2 | hide |
| update-available banner | T1 | condense to one line |

### Home — Session summary (`statsPanel`)
| Element | Tier | Narrow |
|---|---|---|
| active / idle / waiting counts | T0 | keep |
| "Session summary" heading | T1 | keep |
| live clock line | T2 | hide (the phone has a clock) |

### Home — Devices (`devicesPanel`)
| Element | Tier | Narrow |
|---|---|---|
| device rows (icon + name + info) | T0 | keep |
| "this build: <version>" line | T2 | hide |
| "unreachable peer? install…" help (2 lines) | T2 | hide |

### Home — Claude usage (`usagePanel`) — collapses to a single line on narrow
| Element | Tier | Narrow |
|---|---|---|
| Claude prompt count | T0 | keep (in the one-liner) |
| resets-at, block cost | T1 | keep (in the one-liner) |
| quota bar | T1 | hide (one-liner carries the number) |
| tokens in/out | T1 | hide |
| cache create/read | T2 | hide |
| "~$X at API rates" | T2 | hide |
| top-3 projects | T2 | hide |
| Codex / Antigravity blocks (esp. install hints) | T2 | hide |

Narrow render target: `Claude · 47 prompts · $12 · resets 18:00`

### Home — sessions list (`renderSessionLine`)
| Element | Tier | Narrow |
|---|---|---|
| name, state glyph, attached badge | T0 | keep |
| age, `@host`, `[agent]` tags | T1 | already line-gated by `inner > 50/60` |

### Sessions detail pane (`renderDetail`)
| Element | Tier | Narrow |
|---|---|---|
| name, host, state, project | T0 | keep |
| attached line | T1 | keep |
| detach instructions ("To return after attaching") | **T1** | **condense to one line — never hidden** |
| path, windows, created, changed | T2 | hide |
| key cheatsheet (enter/x/R/k/s) | T2 | hide |

Today the *entire* detail pane vanishes on narrow. New behavior: narrow renders a condensed detail (T0 + T1, including the detach line) for the selected row, instead of nothing. The detach instructions are the single highest-stakes call — a mobile user needs them *more* than a desktop user, so they are T1, never T2.

### Conversations
| Element | Tier | Narrow |
|---|---|---|
| header | T0 | keep |
| list rows (agent, preview) | T0 | keep |
| list row relative time | T1 | keep / condense |
| detail pane (transcript preview) | T2 | hide (list only — already happens; just unify the breakpoint) |
| "enter resume · x delete…" hint line | T2 | hide |

### Projects
| Element | Tier | Narrow |
|---|---|---|
| project name | T0 | keep |
| host group subtitles ("on local") | T1 | keep |
| marks (git / CLAUDE / docs/) | T1 | keep / condense |
| header hint "(/: filter   n: new …)" | T2 | hide |
| detail pane (session name, agent, Keys) | T2 | hide (list only — already happens) |

### Notes
| Element | Tier | Narrow |
|---|---|---|
| header | T0 | keep |
| entry list grouped by section | T0 | keep |
| search box (while searching) | T0 | keep |
| hint line "p: switch · /: search …" | T2 | hide |
| preview pane (rendered markdown) | T2 | hide (list only — already happens) |

### Settings
| Element | Tier | Narrow |
|---|---|---|
| editable field rows | T0 | keep |
| field hint for the cursor row | T1 | keep |
| "Settings" heading, Sleep-prevention block | T1 | keep / condense |
| ccmux version + config-path lines | T2 | hide (path is long) |
| "(↑/↓ to move, enter to edit…)" instructional subtitle | T2 | hide |

### Network
| Element | Tier | Narrow |
|---|---|---|
| header, device rows | T0 | keep |
| "Selected" name + ssh-action line | T0/T1 | keep |
| legend line | T2 | hide |
| "Selected" os / address / dial / ccmuxd version | T2 | hide |
| empty-state help paragraph | T2 | hide |

### Agents
The screen is a sub-tab router (`agents.go`): a subtab row over a per-agent config body (`claudeModel` / `codexModel` / `antigravityModel` View, e.g. `claudeconfig.go:236`).
| Element | Tier | Narrow |
|---|---|---|
| subtab row labels (Claude / Codex / Antigravity) | T0 | keep |
| subtab hint "(tab / h·l: switch agent)" | T2 | hide |
| "<Agent> Code Configuration" heading | T1 | keep |
| "settings: <path>" line | T2 | hide (path is long) |
| config blocks — heading + current value (model, effort, safety, hooks, MCP, permissions, commands, skills) | T0/T1 | keep |
| per-block "press X to change" hint lines | T2 | hide |
| "Keys" cheatsheet block | T2 | hide |
| "last write backed up to…" line | T2 | hide |

### Chrome — header (`renderHeader`)
| Element | Tier | Narrow |
|---|---|---|
| active tab — number + screen initial `[N X]` | T0 | keep |
| inactive tabs' numbers | T1 | keep |
| tab labels (full screen names) | T2 | hide (already happens) |
| ` ccmux ` brand title | T2 | hide |

### Chrome — status bar (`renderStatusBar`)
| Element | Tier | Narrow |
|---|---|---|
| `⚠ BATT` danger banner | T0 | keep — safety-critical, never drop |
| daemon status (`✓ daemon` / `⚠ offline`) | T0 | keep |
| `● host` name | T1 | keep / condense |
| `N sess` count | T1 | keep / condense |
| refreshed-at clock | T2 | hide |
| version chip | T2 | hide |

### Chrome — footer (`renderFooter`)
| Element | Tier | Narrow |
|---|---|---|
| `? help` | T0 | keep |
| error toast (when shown) | T0 | keep — takes the row |
| `q quit` | T1 | keep |
| info / success toast | T1 | keep |
| `n new` · `x kill` · `r refresh` action hints | T2 | hide (screen body + `?` cover them) |

Narrow footer collapses to `? help • q quit`.

### The cross-cutting rule
Every screen carries inline hint / cheatsheet lines ("enter: attach   x: kill", "p: switch project", "↑/↓ to move", legend lines). **All inline hint lines are uniformly T2 — hidden on narrow.** The `?` help screen (`help.go`) is the always-available home for that content.

## Risks / Trade-offs

- [Risk] A T2-tagged element turns out to be load-bearing on mobile (e.g. someone relies on the session `path`) → Mitigation: the classification is reviewable in this document *before* implementation; the CLI exposes the same data; re-tagging T1↔T2 is a one-line change.
- [Risk] The detach instructions are the highest-stakes element — if hidden, a mobile user could get stuck inside an attached session → Mitigation: detach instructions are **T1, not T2** — condensed to one line, never hidden.
- [Risk] ANSI escape codes inflate `len(line)` beyond visible width → Mitigation: measure with `lipgloss.Width()` in both renderers and tests.
- [Risk] Collapsing `usagePanel` to one line drops the quota bar some users glance at → Mitigation: the one-liner keeps the headline prompt count and resets-at; the bar is a visual restyling of the same number.
- [Trade-off] Desktop tmux panes 80–119 cols wide now render the curated narrow layout, not the full desktop layout — intentional (Decision 1); the narrow view is designed to be good, not a degraded fallback.
- [Risk] Moving `isNarrow` from 80 to 120 can break existing tests that render a screen at 80–119 expecting the wide layout → Mitigation: the implementation phase audits and updates those tests; the current header tests (60, 200) and `TestHomeView_TileOrder` (120) already sit on the correct side of the new line.
