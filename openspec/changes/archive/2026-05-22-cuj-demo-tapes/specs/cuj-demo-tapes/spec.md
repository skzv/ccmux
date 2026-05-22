## ADDED Requirements

### Requirement: Canonical CUJ catalog

The repository SHALL maintain a single documented catalog of ccmux's
key critical user journeys (CUJs) at a fixed, discoverable location.
Each catalog entry MUST carry a stable ID (`C1`, `C2`, …), a journey
name, a one-line description of what the journey does, a value
statement (why it sells the tool), and a `demoable` classification of
`full` (capturable in a single terminal recording), `stubbed` (capturable
only with faked hosts/agents), or `still` (not capturable in motion).

#### Scenario: Catalog is the source of truth for demos

- **WHEN** a contributor needs to know which journeys ccmux considers
  load-bearing
- **THEN** the CUJ catalog lists every one with its ID, description,
  value statement, and demoable classification

#### Scenario: New user-facing journey extends the catalog

- **WHEN** a change introduces a new CUJ-level user-facing capability
- **THEN** the catalog gains a new entry with the next sequential ID
  before that change is considered complete

### Requirement: A demo tape per demoable CUJ

Every catalog CUJ classified `full` or `stubbed` SHALL have a
corresponding VHS `.tape` source file under `docs/vhs/`, named with the
CUJ's ID so the mapping is unambiguous. A CUJ classified `still` MUST
instead have a committed annotated screenshot. No `full`/`stubbed` CUJ
may be left without a tape.

#### Scenario: Demoable CUJ has a tape

- **WHEN** the catalog classifies a CUJ as `full` or `stubbed`
- **THEN** a `.tape` file whose name encodes that CUJ's ID exists under
  `docs/vhs/`

#### Scenario: Non-recordable CUJ has a still

- **WHEN** the catalog classifies a CUJ as `still`
- **THEN** a committed annotated screenshot for that CUJ exists and is
  referenced from the catalog

### Requirement: Hermetic, reproducible tape rendering

VHS tapes SHALL render against stubbed agents on `PATH` and an isolated
environment (temporary `$HOME`, isolated `TMUX_TMPDIR`), reusing the
isolation approach the e2e harness already applies, so that a rendered
demo is deterministic and does not depend on the recording machine's
live sessions, real agent availability, or network state.

#### Scenario: Rendering does not touch the contributor's environment

- **WHEN** a contributor renders the tapes on their own machine
- **THEN** no real Claude/Codex/Antigravity account, no live tmux
  server outside the isolated `TMUX_TMPDIR`, and no network host is
  required or mutated

#### Scenario: Repeated renders are equivalent

- **WHEN** the same tape is rendered twice on different machines
- **THEN** the demo shows the same journey with the same on-screen
  content, modulo timing

### Requirement: Documented render build target

The project SHALL provide a documented `make tapes` target that renders
every catalog tape to an animated asset in a known output directory,
and a `make tapes-check` target that fails if any `full`/`stubbed`
catalog CUJ lacks a tape. `docs/vhs/README.md` MUST document the
contributor-only tooling dependency (VHS) and the render command.

#### Scenario: One command renders every demo

- **WHEN** a contributor runs `make tapes`
- **THEN** every catalog tape is rendered to the documented output
  directory

#### Scenario: A missing tape fails the check

- **WHEN** a `full` or `stubbed` catalog CUJ has no matching `.tape`
- **THEN** `make tapes-check` exits non-zero and names the missing CUJ

### Requirement: Demos surfaced on all three channels

The rendered CUJ demos SHALL be surfaced on each of the three audience
channels: the GitHub `README.md`, the ccmux.ai landing page, and the
ccmux.ai docs tutorials. The README's `<!-- DEMO_GIF -->` placeholder
MUST be replaced by real headline demos, and each ccmux.ai tutorial
that maps to a catalog CUJ MUST embed that CUJ's demo.

#### Scenario: README shows the headline demos

- **WHEN** a visitor opens the GitHub repository
- **THEN** the README shows the headline CUJ demos in place of the
  former `<!-- DEMO_GIF -->` placeholder

#### Scenario: Each mapped tutorial embeds its demo

- **WHEN** a reader opens a ccmux.ai docs tutorial that corresponds to a
  catalog CUJ
- **THEN** that tutorial page embeds the matching rendered demo
