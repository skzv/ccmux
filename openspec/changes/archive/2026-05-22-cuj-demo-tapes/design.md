## Context

ccmux's value is visual but it ships no motion demos (see `proposal.md`).
Three ad-hoc tapes exist in `docs/vhs/` and prove [VHS](https://github.com/charmbracelet/vhs)
works: a `.tape` file is a declarative script of keystrokes + sleeps,
`vhs file.tape` renders it to a GIF.

Constraints that shape the design:

- **Determinism.** A demo that records the contributor's *live* machine
  shows whatever sessions/usage/projects happen to exist — different
  every render, and potentially leaking private project names. The e2e
  suite already solved the identical problem: `internal/e2e/harness_test.go`
  installs stub `claude`/`codex`/`agy` agents on `PATH` and runs against
  a temp `$HOME` + isolated `TMUX_TMPDIR`. Tapes must reuse that.
- **Two consumer repos.** The README lives in `ccmux`; the landing page
  and docs live in the sister repo `../ccmux-website` (Astro). Rendered
  assets must reach both.
- **Every demo must be a TUI recording.** A tape launches `ccmux` and
  drives the TUI with keystrokes; no tape is a bare CLI-command demo,
  even for journeys (new project, update) that also have a CLI form.
- **Multi-machine needs a real multi-host environment.** C7 cannot be
  faked convincingly; it needs several `ccmuxd` instances the dashboard
  genuinely talks to. C8 (phone) needs a phone-width viewport and a
  push notification — the viewport is recordable, the push is not.
- **VHS is a contributor tool, not a runtime dependency.** It must not
  enter `go.mod` or any install path.

Stakeholders: the project owner (markets ccmux), contributors (render
tapes), newcomers (the audience).

## Goals / Non-Goals

**Goals:**

- A single canonical CUJ catalog that the demos and the e2e suite can
  both point at.
- One reproducible tape per demoable CUJ, rendered hermetically.
- A one-command render (`make tapes`) and a CI-cheap coverage check
  (`make tapes-check`).
- Rendered demos live on the README, the ccmux.ai landing page, and the
  ccmux.ai docs tutorials.

**Non-Goals:**

- Rendering tapes *in CI*. VHS needs `ttyd` + `ffmpeg`; wiring that into
  the CI matrix is a follow-up. CI v1 only runs the file-existence
  `tapes-check`.
- A video/voiceover production pipeline. These are short silent loops.
- Changing any ccmux runtime behavior — this is docs/assets/build only.
- Auto-syncing assets between the two repos. v1 commits rendered assets
  into each repo by hand; automation is an open question.

## Decisions

### D1 — Hermetic render harness reusing the e2e stub pattern

A `docs/vhs/render.sh` wrapper script will, for each tape:

1. Create a temp `$HOME` and `TMUX_TMPDIR`.
2. Symlink/copy the e2e stub agents (`claude`, `codex`, `agy`) — the
   `installStubAgents` logic — onto a `PATH`-prepended dir. Stubs echo a
   marker then `sleep`, so a "session" looks alive without a real agent.
3. Seed a small fixture: 2–3 fake projects under the temp `Projects`
   dir, a couple of pre-canned `~/.claude/projects/*.jsonl` transcripts
   (for the conversations/resume demo), a fixed `config.toml`.
4. Run `vhs <tape>` with that environment.
5. Tear the temp tree down.

Rationale: the e2e harness already proves this isolation works on Linux
and macOS CI. Reusing it keeps one notion of "a fake ccmux world."
Alternative considered — recording the real machine and scrubbing —
rejected: non-reproducible and leaks private data.

The stub logic is currently a Go test helper (`//go:build integration`).
To share it with a shell script, factor the fixture seeding into a small
committed script (`docs/vhs/fixture.sh`) that both the tape harness and,
optionally, a future test can call. The agent stubs themselves are tiny
(`echo` + `sleep`) and will be written directly by `fixture.sh`.

### D1b — Every tape drives the TUI

Each tape's body is: launch `ccmux` (the TUI), then keystrokes. Journeys
that today are reached via a CLI subcommand are recorded through their
TUI equivalent instead:

- C1 (new project) → the Projects-screen `n` form, not `ccmux new`.
- C11 (update) → the dashboard's update banner and the in-TUI
  check/apply flow, not `ccmux update` at a shell prompt.

Rationale: the TUI is the product; a README full of shell-prompt GIFs
undersells it and is also harder to follow. The CLI still exists and is
documented in text — it just isn't what the *demos* show.

### D1c — `netsim`: a simulated network of machines

C7 (multi-machine) records against a real multi-host environment built
by a committed `docs/vhs/netsim.sh`:

1. Start N `ccmuxd` instances, each bound to a distinct loopback
   address (`127.0.0.2`, `127.0.0.3`, … — the whole `127.0.0.0/8` block
   is loopback on macOS and Linux), each with its own isolated fake
   `$HOME`/`TMUX_TMPDIR` and a couple of stub-agent sessions.
2. Register them in the recording ccmux's `hosts.toml` under friendly
   names (`mac-mini`, `linux-box`, …).
3. The TUI's dashboard then genuinely health-pings and lists all of
   them — the multi-host view is real, not mocked.
4. Attach shells `mosh`; the fixture's stub `mosh` just `tmux attach`es
   into the right daemon's socket, so the Enter→attached beat records
   honestly.

`netsim` is built so it is reusable beyond recording — it is also a
fixture a future multi-host e2e/stress test can stand on. Teardown kills
every spawned `ccmuxd` and removes the temp trees.

Rationale: a screenshot-style mock of "remote sessions" would be
dishonest and brittle. Driving real daemons over loopback is the
smallest setup that makes the dashboard's remote-host code path the
thing under the camera. Alternative — two real machines / containers —
rejected as too heavy for a recording harness and not CI-portable.

### D2 — Catalog is a living doc; tapes are named by CUJ ID

- The catalog content lives at `docs/01_Specs/04_CUJ_Catalog.md` —
  alongside the existing specs, linked from the docs map in `CLAUDE.md`.
  It is the human-readable table from `proposal.md`, kept current.
- Tapes are named `cujNN_<slug>.tape` (e.g. `cuj01_new_project.tape`),
  so the CUJ↔tape mapping is mechanical and `tapes-check` is a glob.
- The three legacy tapes map into the scheme but are **re-authored, not
  just renamed**: `01_new_project` and `03_update` are today bare-CLI
  recordings and must become TUI recordings (`cuj01_new_project`,
  `cuj11_update`); `02_dashboard` → `cuj02_dashboard` is already
  TUI-driven and only needs the hermetic fixture. All three drop their
  dependence on the recorder's live machine state.

### D3 — One tape emits both GIF and a web-friendly format

A VHS tape can declare multiple `Output` lines. Each tape emits:

- `out/cujNN_<slug>.gif` — for the GitHub README (GitHub renders GIFs
  inline everywhere; MP4 autoplay is unreliable in READMEs).
- `out/cujNN_<slug>.webp` — animated WebP, ~5–10× smaller than GIF, for
  ccmux.ai where we control the `<img>`/`<video>` markup.

Rationale: GIF for the lowest-common-denominator surface, WebP for the
surface where bytes matter. MP4 considered for the site but WebP keeps
it a plain `<img>` with no player chrome and no autoplay-policy fights.

### D4 — Asset storage and the cross-repo flow

- ccmux repo: rendered assets commit to `docs/vhs/out/`. They are
  regenerated rarely and a demo GIF is ~1–3 MB — acceptable in-repo,
  and it keeps the README working on a fresh clone with no build step.
- ccmux-website repo: the WebP assets are copied into its `public/demos/`
  and committed there in a separate PR in that repo.
- `docs/vhs/README.md` documents both the render command and the
  "copy WebP into ccmux-website/public/demos/" step.

Alternative considered — a shared release asset bucket / Git LFS —
rejected as overkill for ~11 small files that change rarely.

### D5 — `make tapes` renders; CI only checks coverage

- `make tapes` → runs `docs/vhs/render.sh` over every `docs/vhs/*.tape`.
- `make tapes-check` → pure shell: for every `full`/`stubbed` row in the
  catalog, assert a `cujNN_*.tape` exists; exit non-zero naming any gap.
  No VHS needed, so it runs in the existing CI `test` job cheaply.
- Rendering stays a local/manual step in v1 (see Non-Goals).

### D6 — Triage of the non-single-terminal CUJs

- **C7 multi-machine** → `stubbed`. Recorded against `netsim` (D1c): the
  TUI dashboard shows the local host plus the simulated `mac-mini` /
  `linux-box` daemons, color-coded, and `Enter` attaches a remote
  session via the stub `mosh`. The multi-host code path is genuinely
  exercised; only the network underneath is simulated.
- **C8 phone** → `stubbed`. VHS `Set Width`/`Set Height` to an
  iPhone-portrait viewport (~52×40 cell), recording the real
  narrow-layout TUI. The bell→push step cannot be shown in a terminal;
  the tape ends on the visible "waiting for input" marker and the
  catalog/caption explains the push.
- Any CUJ that still cannot be shown honestly is reclassified `still`
  with a committed annotated screenshot — none are expected today.

### D7 — Headline vs long-tail on the README

The README gets the two headline demos inline (C1 scaffold, C2
dashboard) replacing `<!-- DEMO_GIF -->`; the remaining CUJs are a
linked list to a gallery section, to keep the README from becoming a
multi-megabyte page.

## Risks / Trade-offs

- **Tape drift** — a UI change silently invalidates a tape's keystrokes.
  → `tapes-check` guarantees a tape *exists*, not that it's current; the
  mitigation is rendering as part of the release checklist (documented
  in `docs/vhs/README.md`) and keeping tapes short.
- **Stub agents diverge from real agents** — the demo could show
  behavior the real agent doesn't. → Stubs are deliberately minimal and
  only ever shown mid-"thinking"; demos never depict an agent's actual
  output, only ccmux's chrome around it.
- **In-repo GIF weight** — ~11 GIFs at 1–3 MB grows the clone. →
  Accepted; WebP keeps the website light, and the count is bounded by
  the catalog. Revisit with LFS only if it becomes a real problem.
- **Cross-repo skew** — ccmux-website embeds assets that ccmux renders;
  they can fall out of sync. → v1 documents the manual copy step; D-open
  proposes automation.
- **VHS not installed** — `make tapes` fails for a contributor without
  VHS. → `render.sh` checks for `vhs` up front and prints the
  `brew install vhs` hint; `tapes-check` never needs VHS.

## Migration Plan

0. **Prerequisite:** the separate project-scaffolding-removal change
   lands first — it redefines C1 (creating a project = directory +
   agent session, no `CLAUDE.md`/`docs/`/`git init`). The C1 tape is
   authored against that new behavior.
1. Land the catalog doc + spec + this design (proposal phase output).
2. Add `fixture.sh` + `netsim.sh` + `render.sh` + the `make` targets.
3. Re-author the three legacy tapes into the scheme as TUI recordings;
   make them hermetic.
4. Author the remaining tapes, render, commit GIFs to `docs/vhs/out/`.
5. Replace the README placeholder; add the gallery section.
6. Separate PR in `../ccmux-website`: copy WebP assets, embed in the
   landing page and the mapped tutorial MDX files.

Rollback: the change is additive — reverting the README hunk and
deleting `docs/vhs/out/` fully undoes it; no runtime surface is touched.

## Open Questions

- **Automated rendering in CI** — worth a dedicated workflow (install
  VHS + `ttyd` + `ffmpeg`, render, commit assets back via a bot PR)?
  Deferred; v1 is manual render + `tapes-check`.
- **Cross-repo asset sync** — should ccmux-website pull demos from the
  ccmux repo at build time (git submodule / npm dependency / fetch
  during `astro build`) instead of a hand-copied `public/demos/`?
  Deferred to the website PR.
- **Catalog ownership of e2e** — `openspec/specs/cuj-e2e-coverage`
  already enumerates CUJs for tests. Should the new
  `docs/01_Specs/04_CUJ_Catalog.md` become the single list both the
  e2e spec and the tapes reference? Proposed yes, but reconciling the
  two enumerations is left as a follow-up so this change stays scoped.
