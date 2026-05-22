## Why

ccmux is a terminal tool whose value is almost entirely *visual* — a
dashboard of live sessions, a notes browser, a multi-machine view — yet
it ships with **zero motion demos**. The GitHub README still carries a
literal `<!-- DEMO_GIF -->` placeholder; ccmux.ai shows two static PNG
screenshots; the docs tutorials are text-only. A newcomer cannot see
what the tool *does* without installing it.

Three existing VHS tapes (`docs/vhs/01_new_project`, `02_dashboard`,
`03_update`) prove the approach works but were written ad hoc, are not
rendered anywhere automatically, and don't map to a deliberate list of
the journeys that actually sell the tool.

This change first **establishes the canonical catalog of ccmux's key
critical user journeys (CUJs)** — the documentation artifact the work
hangs on — and then produces a VHS tape per demoable CUJ, wired into
the GitHub README, the ccmux.ai landing page, and the ccmux.ai docs.

## What Changes

### 1. The canonical CUJ catalog

A documented, agreed list of the journeys that make ccmux valuable.
Each is the basis for one demo. Drawn from the Vision doc, the Feature
Catalog, and the existing e2e CUJ taxonomy (session / project / notes /
conversations / daemon / onboarding).

Every CUJ is demonstrated **through the TUI** — each tape launches
`ccmux` and drives it with keystrokes. Where a journey also has a CLI
form, the tape still shows the TUI surface; no tape is a bare
shell-command demo.

| # | CUJ | What it shows (in the TUI) | Why it sells the tool |
|---|-----|---------------|-----------------------|
| C1 | **Start a new project** | On the Projects screen, `n` opens the new-project form (name, agent, first prompt); ccmux creates the directory and boots the agent session | Dashboard → a working agent in a fresh project, no shell glue |
| C2 | **The dashboard — every session at a glance** | Launch the TUI: sessions across projects, status (active / idle / waiting-for-input), usage panel + 5h quota bar | Makes the invisible legible — the core pitch |
| C3 | **Attach, work, detach** | `Enter` attaches a session, do work, `Ctrl-b d` detaches back to the dashboard | tmux is the database; ccmux is the lens |
| C4 | **Resume where you left off** | Open a project → menu of its running sessions *and* past conversations → resume one | Conversation continuity across restarts |
| C5 | **Pick your agent** | Choose Claude / Codex / Antigravity per project; the session boots that agent | Multi-agent, not Claude-only |
| C6 | **Project notes, terminal-native** | Notes tab: the project's markdown tree, Glamour-rendered preview, `/` ripgrep search | Notes that travel with the project, no sync service |
| C7 | **Sessions from anywhere — multi-machine** | Dashboard shows local + remote (Tailscale) hosts color-coded; `Enter` attaches a remote session over Mosh | The "reachable from every device" promise |
| C8 | **The phone workflow** | Narrow-layout TUI in a phone-width pane; an agent needs input → terminal bell → iOS push | The most-used real-world path |
| C9 | **Tune Claude Code itself** | The Agents screen: switch model, edit `CLAUDE.md`, browse commands / skills | ccmux manages the agent's own config |
| C10 | **First-run setup & doctor** | The Setup screen (dependency checks, SSH key, QR for phone) and the doctor health view | Setup is a flow, not a README |
| C11 | **Stay current** | The dashboard's update banner; checking for and applying an update from the TUI | Low-friction upgrades |

### 2. A VHS tape per CUJ — all TUI-driven

- One `.tape` file per CUJ under `docs/vhs/`, replacing the three ad-hoc
  tapes with a numbered, catalog-aligned set (`cuj01_*.tape` …).
- **Every tape drives the TUI.** Each tape launches `ccmux` and records
  the journey as keystrokes inside the TUI — never a bare CLI command.
- Tapes render **hermetically** — stub agents on `PATH` and an isolated
  `$HOME`/`TMUX_TMPDIR`, reusing the pattern the e2e harness already
  uses — so a demo is deterministic and reproducible, not dependent on
  the recorder's live machine state.
- A **network simulator** (`netsim`) stands up several `ccmuxd`
  instances on distinct loopback addresses, each with its own fake
  sessions, registered as named hosts — so the multi-machine journey
  (C7) records a *real* multi-host dashboard, not a mock-up.
- A `make tapes` target renders every tape to GIF (and, where smaller,
  animated WebP) into a known output directory.
- C8 (phone) records the real narrow-layout TUI at a phone-width
  viewport; the bell→push step is the one beat a terminal cannot show
  and is covered by the catalog/caption.

### 3. Wiring into the three surfaces

- **GitHub README** — replace the `<!-- DEMO_GIF -->` placeholder with
  the headline tapes (C1, C2) and link the rest.
- **ccmux.ai landing** (`../ccmux-website/src/pages/index.astro`) — hero
  demo + a CUJ showcase strip.
- **ccmux.ai docs** (`../ccmux-website/src/content/docs/`) — embed the
  matching tape in each tutorial MDX (tutorials 01–06 already map onto
  C1/C2/C8/C5/C7/C11).

## Capabilities

### New Capabilities
- `cuj-demo-tapes`: the canonical CUJ catalog plus the requirement that
  each demoable CUJ has a hermetic, reproducible VHS tape rendered by a
  documented build target and surfaced on the README, the ccmux.ai
  landing page, and the ccmux.ai docs.

### Modified Capabilities
<!-- None. This change adds demo/marketing assets and a documented CUJ
catalog; it does not alter the requirements of any existing capability
spec in openspec/specs/. -->

## Impact

- **`docs/vhs/`** — replaces the three current tapes with a
  catalog-aligned numbered set; updates `docs/vhs/README.md`.
- **`Makefile`** — new `tapes` target (and a `tapes-check` lint that
  fails if a catalog CUJ has no tape).
- **`README.md`** — `<!-- DEMO_GIF -->` placeholder replaced; rendered
  GIF assets committed (or linked from a release asset).
- **`../ccmux-website`** (sister repo) — landing page + tutorial MDX
  embed the tapes; rendered assets land under its `public/`.
- **Tooling dependency** — [VHS](https://github.com/charmbracelet/vhs)
  (`charmbracelet/vhs`) and `ttyd`/`ffmpeg`, needed to render tapes;
  documented as a contributor-only dependency, not a runtime one.
- **No Go code changes** — this is a docs/assets/build change; the stub
  agents it renders against already exist in the e2e harness.
- **Sequencing dependency** — the C1 ("start a new project") tape must
  be authored *after* the separate project-scaffolding-removal change
  lands, since that change redefines what creating a project does
  (directory + agent session only — no `CLAUDE.md`/`docs/`/`git init`).
