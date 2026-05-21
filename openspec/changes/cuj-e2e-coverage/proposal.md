## Why

ccmux has 75 unit/protocol test files but **zero end-to-end tests** that exercise a full Critical User Journey against a real tmux server. CLAUDE.md claims `//go:build integration` tests "run against a real tmux server in CI" — none exist. The result: every CUJ (create a session, attach, kill, scaffold a project, browse notes, resume a conversation, run the daemon) is only verified by hand, and a regression in the glue between TUI → daemon → tmux can land silently. This change inventories every key CUJ, builds the missing e2e harness, covers each CUJ with an automated test, and fixes the bugs that writing those tests surfaces.

## What Changes

- **Define the canonical CUJ set.** Enumerate every key Critical User Journey across the TUI, the CLI, and the daemon, and pin each to its entry point and expected end state.
- **Build an e2e test harness.** A shared `internal/e2e` (or `//go:build integration`) harness that spins up an isolated tmux server on a private socket, a temp `$HOME`/projects root, and a real `ccmuxd` over a Unix socket — so tests never touch the user's sessions or config.
- **Cover each CUJ with an e2e test:**
  - *Session lifecycle* — create a bare session, attach, rename, kill, `ccmux list --json`.
  - *Project lifecycle* — discover projects, scaffold a new project (`ccmux new` + TUI wizard), upgrade an existing project, attach-or-create.
  - *Notes* — browse the `docs/` tree, render preview, create Agent Log / Spec / ADR, ripgrep search.
  - *Conversations* — list transcripts, resume, delete.
  - *Daemon* — Unix-socket IPC, tmux poll + session-state classification, bell injection on `needs_input`, sleep-lock lifecycle, tailnet HTTP API parity with the socket protocol.
  - *Config* — switch default agent/model, edit-and-reload config.
  - *Onboarding* — `ccmux doctor` exit codes, `ccmux host add/remove/list`.
- **Wire `make test-e2e` + CI.** Add the integration job the testing doc already specifies, so e2e runs on every PR.
- **Fix bugs found while writing the tests.** Candidate findings (to be confirmed during implementation): daemon poll-loop lock held across slow `tmux` calls; `CapturePane` errors swallowed without state update or log; session-name collisions on millisecond timestamps; unguarded reads of daemon sleep state; path-join not rejecting absolute entries; duplicated session-name sanitization across TUI and CLI.
- **Simplify where the tests expose complexity.** Unify duplicated session-name sanitization, narrow over-broad mutex scopes, remove dead fields (e.g. unwired `KeepAwake`), and reduce the oversized `App.Update` switch where it blocks testability — scoped to what the e2e work touches, not a blanket refactor.
- **Correct CLAUDE.md** to describe the test tiers that actually exist.

## Capabilities

### New Capabilities

- `cuj-e2e-coverage`: Every key Critical User Journey is defined with an entry point and expected end state, and is verified by an automated end-to-end test that drives the real CLI/TUI/daemon against an isolated tmux server. The harness must be hermetic (no user state touched) and run in CI on every PR.

### Modified Capabilities

<!-- No existing spec's requirements change. `adaptive-screen-layout` is unaffected.
     Bug fixes correct behavior to match intent but no current spec documents that behavior. -->

## Impact

- **New:** `internal/e2e/` (or `cmd/.../*_integration_test.go` behind `//go:build integration`) harness; per-CUJ e2e test files; `make test-e2e` target; CI integration job in `.github/workflows/`.
- **Modified (bug fixes / simplification):** `cmd/ccmuxd/main.go` (poll loop, lock scope, handlers), `internal/tmux/tmux.go` (session-name handling), `internal/project/project.go` (path join, agent read), `internal/daemon/` (protocol/client), `internal/tui/app.go` and screen models (testability seams), `internal/notes/`, `cmd/ccmux/cmd/` subcommands.
- **Docs:** `CLAUDE.md` testing section, `docs/01_Specs/03_Testing_And_CI.md`, README test instructions.
- **No breaking changes** to the daemon protocol, CLI surface, or config schema. Bug fixes change observably-wrong behavior only.
- **Dependencies:** no new runtime deps; tests rely on `tmux` (already required) and the existing `teatest` dev dependency.
