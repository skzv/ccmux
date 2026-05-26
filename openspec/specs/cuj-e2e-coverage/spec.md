# cuj-e2e-coverage Specification

## Purpose
Define the end-to-end coverage contract for ccmux: a documented inventory of every Critical User Journey (CUJ) and a hermetic e2e test harness that verifies each journey against real `ccmux`/`ccmuxd` binaries and a real tmux server, gated in CI.
## Requirements
### Requirement: Documented CUJ inventory

The project SHALL maintain a documented inventory of every key Critical User Journey (CUJ). Each entry SHALL name the journey, its entry point (CLI subcommand, TUI screen/keybind, or daemon endpoint), and its observable end state. The inventory SHALL be the authoritative list against which e2e coverage is measured.

#### Scenario: Inventory enumerates every key journey

- **WHEN** a reviewer reads the CUJ inventory
- **THEN** it lists, at minimum, the session-lifecycle, project-lifecycle, notes, conversations, daemon, config/agent, and onboarding journeys, each with an entry point and an expected end state

#### Scenario: A new CUJ test maps to an inventory entry

- **WHEN** an e2e test is added for a journey
- **THEN** that journey has a corresponding entry in the inventory, so coverage and inventory never drift apart

### Requirement: Hermetic e2e test harness

The project SHALL provide an end-to-end test harness that runs each CUJ against a real tmux server and the real `ccmux`/`ccmuxd` artifacts. The harness MUST be hermetic: it MUST isolate the tmux server, `$HOME`, the projects root, and the ccmux config so that no test reads or mutates the developer's real sessions, transcripts, or configuration.

#### Scenario: e2e run leaves user state untouched

- **WHEN** the full e2e suite runs on a machine with live ccmux sessions and config
- **THEN** the developer's tmux sessions, `~/.claude`, and `~/.config/ccmux` are unchanged, and every session/socket/temp file the suite created is removed on completion

#### Scenario: tmux is unavailable

- **WHEN** the e2e suite runs on a machine without `tmux` installed
- **THEN** the affected tests skip with a clear message rather than failing, and the untagged `go test ./...` run is unaffected

#### Scenario: Polling is deterministic

- **WHEN** an e2e test needs the daemon to observe a tmux state change
- **THEN** the test triggers exactly one poll cycle synchronously and asserts the result without relying on a wall-clock sleep

### Requirement: e2e suite runs in CI

The e2e suite SHALL be runnable via a single `make test-e2e` target and SHALL execute in CI on every pull request, on both Linux and macOS runners. A failing e2e suite SHALL block the merge.

#### Scenario: Developer runs the suite locally

- **WHEN** a developer runs `make test-e2e`
- **THEN** the integration-tagged tests build the binaries and run against an isolated tmux server, reporting pass/fail per CUJ

#### Scenario: CI gates a regression

- **WHEN** a pull request breaks a key CUJ
- **THEN** the CI integration job fails on at least one runner and the pull request cannot merge

### Requirement: Session lifecycle CUJ is verified end to end

The system SHALL let a user create a session, see it listed with correct metadata, rename it, and kill it. Each step SHALL be covered by an e2e test driving the real CLI and/or the real `App` model against a real tmux server.

#### Scenario: Create and list a session

- **WHEN** a session is created (via the TUI new-session form or `ccmux new`/bare-session path)
- **THEN** a corresponding tmux session exists, and `ccmux list --json` reports it with its name, project, and state

#### Scenario: Rename a session

- **WHEN** a running session is renamed
- **THEN** the tmux session is renamed and subsequent listings show the new name and no stale entry under the old name

#### Scenario: Kill a session

- **WHEN** a session is killed (via the TUI kill+confirm flow or `ccmux kill`)
- **THEN** the tmux session no longer exists and it disappears from the next listing

#### Scenario: Generated session names do not collide

- **WHEN** two sessions are created in rapid succession
- **THEN** each receives a distinct name and both creations succeed

### Requirement: Session attach CUJ is verified

The system SHALL attach a user to a selected session. Because an interactive attach requires a controlling terminal, the e2e test SHALL verify the attach command is correctly constructed and the target session is valid, rather than performing a live interactive attach.

#### Scenario: Local attach builds the correct command

- **WHEN** the user attaches to a local session
- **THEN** the constructed command targets `tmux attach`/`switch-client` for the correct session name, and that session exists in the sandboxed tmux server

#### Scenario: Remote attach builds the correct command

- **WHEN** the user attaches to a session on a configured remote host
- **THEN** the constructed command invokes `mosh`/`ssh` against the host with a `tmux attach` for the correct session name

### Requirement: Project lifecycle CUJ is verified end to end

The system SHALL discover projects under the configured root, scaffold a new project, upgrade an existing project non-destructively, and attach-or-create a session for a project. Each SHALL be covered by an e2e test.

#### Scenario: Discover projects

- **WHEN** the projects root contains directories with a `CLAUDE.md` or `.git`
- **THEN** project discovery returns exactly those directories as projects and ignores unrelated directories

#### Scenario: Scaffold a new project

- **WHEN** a new project is created (via the TUI wizard or `ccmux new`)
- **THEN** the project directory exists with the scaffolded `CLAUDE.md` and docs structure, and a tmux session for it is running

#### Scenario: Upgrade an existing project

- **WHEN** an existing project without ccmux scaffolding is upgraded
- **THEN** the scaffolding is injected without overwriting pre-existing user files, and running the upgrade twice produces no further changes

#### Scenario: Attach-or-create for a project with a running session

- **WHEN** the user opens a project that already has a running session
- **THEN** the system offers to rejoin the existing session or create a distinctly-named new one, and the chosen action reaches the correct session

### Requirement: Notes CUJ is verified end to end

The system SHALL let a user browse a project's `docs/` tree, preview a rendered note, create Agent Log / Spec / ADR notes from templates, and search notes. Each SHALL be covered by an e2e test.

#### Scenario: Browse and preview notes

- **WHEN** a project's `docs/` tree contains markdown files
- **THEN** the Notes screen lists them grouped by section and renders the selected note's content

#### Scenario: Create a templated note

- **WHEN** the user creates an Agent Log, Spec, or ADR note
- **THEN** a markdown file is created in the correct `docs/` subdirectory with the templated frontmatter and the expected auto-numbered or dated filename

#### Scenario: Search notes

- **WHEN** the user searches notes for a term present in one note
- **THEN** the search returns that note and excludes notes without the term

### Requirement: Conversations CUJ is verified end to end

The system SHALL list past agent conversations through known-agent
navigation, resume a conversation into a new session, and delete a
conversation transcript. Each SHALL be covered by an e2e test using fixture
transcripts in the isolated `$HOME`.

#### Scenario: List conversations

- **WHEN** the isolated `$HOME` contains agent transcript fixtures
- **THEN** the Conversations screen shows Claude, Codex, Cursor, and Agy in
  the agent navigation
- **AND** the focused agent view lists that agent's conversations sorted by
  recency with their project and agent

#### Scenario: Empty agent section

- **WHEN** the isolated `$HOME` contains no visible conversations for a
  focused known supported agent
- **THEN** the Conversations screen shows that agent's view with a clear
  empty state

#### Scenario: Switch agent sections

- **WHEN** the Conversations screen contains multiple agent sections
- **THEN** Tab/Shift+Tab or left/right key input changes the focused agent
  section without moving row selection across section boundaries

#### Scenario: Resume a conversation

- **WHEN** the user resumes a listed conversation
- **THEN** a new tmux session is created for that conversation's project
  and the resume command targets the correct conversation id

#### Scenario: Delete a conversation

- **WHEN** the user confirms deletion of a conversation
- **THEN** the transcript file is removed and the row disappears from the
  next listing

### Requirement: Daemon polling and classification CUJ is verified

The daemon SHALL poll tmux, classify each session's state, and inject a terminal bell when a session transitions to `needs_input`. This SHALL be covered by an e2e test against a real tmux server.

#### Scenario: Daemon detects and classifies a session

- **WHEN** a session exists in the sandboxed tmux server and the daemon runs a poll cycle
- **THEN** the daemon reports the session with a classified state derived from its pane content

#### Scenario: Bell injected on needs-input transition

- **WHEN** a session's pane content matches the "needs input" heuristic and the daemon polls it with the bell enabled
- **THEN** the daemon injects a BEL byte into that session's pane exactly once for the transition

#### Scenario: Pane-capture failure is surfaced

- **WHEN** capturing a session's pane fails during a poll
- **THEN** the failure is logged and the session's state is not silently left stale

### Requirement: Daemon IPC and tailnet API CUJ is verified

The daemon SHALL serve session and project data over both a local Unix socket and an optional HTTP API, with identical semantics. This SHALL be covered by an e2e test that drives a real daemon over the socket and a second daemon over a loopback HTTP port.

#### Scenario: Unix socket IPC

- **WHEN** a client queries the local daemon over its Unix socket for sessions, projects, and health
- **THEN** the daemon returns the same data the e2e harness observes directly in the sandboxed tmux server

#### Scenario: HTTP API parity

- **WHEN** the same queries are issued to a daemon over its loopback HTTP port
- **THEN** the responses are schema-identical to the Unix-socket responses for the same underlying state

### Requirement: Sleep-lock CUJ is verified

The daemon SHALL request a system sleep-inhibit lock while sessions are active and release it when all sessions are idle or gone. The e2e test SHALL assert this decision against an injected fake inhibitor and MUST NOT acquire a real system lock.

#### Scenario: Lock requested when a session is active

- **WHEN** at least one active session exists
- **THEN** the sleep manager requests the inhibit lock through the injected inhibitor

#### Scenario: Lock released when idle

- **WHEN** all sessions become idle or are killed
- **THEN** the sleep manager releases the inhibit lock

### Requirement: Config and agent CUJ is verified

The system SHALL let a user change the default agent/model and reload edited configuration. Each SHALL be covered by an e2e test against the isolated config.

#### Scenario: Switch the default agent

- **WHEN** the user changes the default agent
- **THEN** the change is persisted to the isolated config and a subsequently created session launches that agent's command

#### Scenario: Reload edited config

- **WHEN** the ccmux config file is edited and a reload is triggered
- **THEN** the new values take effect in the running TUI and an invalid edit surfaces an error without discarding the previous in-memory config

### Requirement: Onboarding CUJ is verified end to end

The `ccmux doctor` and `ccmux host` commands SHALL behave as documented. Each SHALL be covered by an e2e test.

#### Scenario: doctor exit code reflects problem count

- **WHEN** `ccmux doctor` runs against the isolated environment
- **THEN** it reports each dependency check and exits non-zero when and only when at least one problem is found

#### Scenario: host add, list, and remove round-trip

- **WHEN** a host is added with `ccmux host add`, then listed, then removed
- **THEN** the host appears in `ccmux host list` after the add and is absent after the remove, persisted in the isolated config

### Requirement: Bug fixes are regression-locked

Every bug fixed under this change SHALL be accompanied by an automated test that fails on the unfixed code and passes after the fix.

#### Scenario: A fixed bug cannot silently regress

- **WHEN** a bug found while writing the e2e suite is fixed
- **THEN** a test exists that reproduces the bug red before the fix and green after, and that test runs as part of the standard suite

### Requirement: Destructive TUI confirmations are verified end to end
The e2e suite SHALL verify the TUI confirmation flows for quitting ccmux
and killing a session against the real ccmux binary in a sandboxed
environment. The tests MUST isolate `$HOME`, ccmux config, sockets, and
tmux state from the developer's real environment.

#### Scenario: Session kill cancel is sandboxed and non-destructive
- **WHEN** the e2e harness starts a sandboxed tmux session and drives the TUI to press `x`
- **THEN** the TUI displays the kill confirmation modal
- **THEN** cancelling the modal leaves the sandboxed session running
- **THEN** no developer tmux sessions or config are read or mutated

#### Scenario: Session kill confirm removes only the sandboxed target
- **WHEN** the e2e harness starts multiple sandboxed tmux sessions and confirms killing the selected one from the TUI
- **THEN** the selected sandboxed session is removed
- **THEN** the other sandboxed sessions remain running

#### Scenario: Quit cancel keeps the TUI running
- **WHEN** the e2e harness drives the TUI to press `q`
- **THEN** the TUI displays the quit confirmation modal
- **THEN** cancelling the modal leaves the TUI process running and interactive

#### Scenario: Quit confirm exits without killing sessions
- **WHEN** the e2e harness starts a sandboxed tmux session and confirms quitting ccmux from the TUI
- **THEN** the TUI process exits
- **THEN** the sandboxed tmux session remains running until harness cleanup
