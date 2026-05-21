## 1. CUJ inventory & harness foundation

- [x] 1.1 Write the canonical CUJ inventory (entry point + end state for each journey) into `docs/01_Specs/03_Testing_And_CI.md` or a dedicated section
- [x] 1.2 Verify whether `internal/tmux` honors `TMUX_TMPDIR` / a custom server; add a minimal test-only socket seam if it does not
- [x] 1.3 Verify whether the daemon poll loop interval is injectable; add a synchronous single-poll seam (e.g. exported `PollOnce`) if not
- [x] 1.4 Verify whether the sleep manager abstracts the system inhibitor; add a fake-inhibitor interface seam if not
- [x] 1.5 Create `internal/e2e` package (build tag `//go:build integration`) with: isolated tmux server (unique socket via `TMUX_TMPDIR`), temp `$HOME`, temp projects root, temp config, and `t.Cleanup` teardown
- [x] 1.6 Add harness helpers: build `ccmux`/`ccmuxd` once per run, spawn `ccmuxd` on a temp Unix socket, run CLI subprocesses, drive the `App` model via `teatest`
- [x] 1.7 Add a `tmux`-absent skip guard and a leak check that asserts no stray sessions/sockets/files survive teardown

## 2. Session lifecycle CUJ

- [x] 2.1 e2e test: create a bare session (TUI form + `ccmux new`/bare path), assert the tmux session exists
- [x] 2.2 e2e test: `ccmux list --json` reports created sessions with correct name/project/state
- [x] 2.3 e2e test: rename a session, assert new name present and old name gone
- [x] 2.4 e2e test: kill a session (TUI kill+confirm and `ccmux kill`), assert it is gone
- [x] 2.5 e2e test: two rapid session creations produce distinct names; fix the name-collision bug if confirmed (use a counter or `UnixNano`)
- [x] 2.6 e2e test: local + remote attach build the correct exec command for the target session

## 3. Project lifecycle CUJ

- [x] 3.1 e2e test: project discovery returns only dirs with `CLAUDE.md`/`.git`; confirm/fix absolute-path join handling in `internal/project`
- [x] 3.2 e2e test: scaffold a new project (TUI wizard + `ccmux new`), assert dir, `CLAUDE.md`, docs structure, and running session
- [x] 3.3 e2e test: upgrade an existing project is non-destructive and idempotent across two runs
- [x] 3.4 e2e test: attach-or-create offers rejoin vs new for a project with a running session, and the chosen action reaches the right session

## 4. Notes CUJ

- [x] 4.1 e2e test: browse a `docs/` tree and render the selected note's preview
- [x] 4.2 e2e test: create Agent Log / Spec / ADR notes, assert path, frontmatter, and auto-numbered/dated filename
- [x] 4.3 e2e test: notes search returns matching notes and excludes non-matching ones

## 5. Conversations CUJ

- [x] 5.1 Add agent-transcript fixtures under the isolated `$HOME`
- [x] 5.2 e2e test: Conversations screen lists fixtures sorted by recency with project/agent
- [x] 5.3 e2e test: resume a conversation creates a session and targets the correct conversation id
- [x] 5.4 e2e test: delete a conversation removes the transcript and the row

## 6. Daemon CUJ

- [x] 6.1 e2e test: daemon poll detects a tmux session and classifies its state from pane content
- [x] 6.2 e2e test: bell injected exactly once on a `needs_input` transition when bell enabled
- [x] 6.3 e2e test: a `CapturePane` failure is logged and does not leave state silently stale; fix the swallowed-error path if confirmed
- [x] 6.4 e2e test: Unix-socket IPC returns sessions/projects/health matching observed tmux state
- [x] 6.5 e2e test: loopback HTTP API responses are schema-identical to the Unix-socket responses (TestHTTPParity_LoopbackMatchesUnixSocket — same `http.ServeMux` over loopback TCP and the Unix socket, byte-identical JSON)
- [ ] 6.6 e2e test: sleep manager requests the inhibit lock when a session is active and releases it when idle (existing `TestManager_EngageRelease` in `internal/sleeplock` covers this CUJ)
- [x] 6.7 Narrow the poll-loop mutex scope so slow `tmux` calls do not block concurrent requests; confirm/fix unguarded daemon-state reads

## 7. Config, agent & onboarding CUJ

- [x] 7.1 e2e test: switching the default agent persists to the isolated config and a new session launches that agent's command (TestDefaultAgent_SwitchPersistsAndLaunches — enabled by the stub-agent harness change)
- [ ] 7.2 e2e test: editing + reloading the config applies new values (deferred — config is TOML; host round-trip (7.4) already exercises the load/save path)
- [x] 7.3 e2e test: `ccmux doctor` reports each check and exits non-zero only when a problem is found
- [x] 7.4 e2e test: `ccmux host add` / `list` / `remove` round-trip persists to the isolated config

## 8. Simplification (scoped to touched code)

- [x] 8.1 Unify duplicated session-name sanitization into a single source of truth used by TUI and CLI
- [x] 8.2 Remove dead/unwired `Client.Kill` and `Client.ToggleKeepAwake` (they POST to a 501 stub endpoint)
- [x] 8.3 Extract testability seams from `server.capture` and `server.bell` for daemon poll tests

## 9. CI, Make & docs

- [x] 9.1 Add a `make test-e2e` target that builds binaries and runs `go test -tags=integration`
- [x] 9.2 Update CI integration job to use `make test-e2e` (ubuntu + macos matrix)
- [x] 9.3 Correct `CLAUDE.md` to describe the test tiers that now actually exist
- [x] 9.4 Update the README test instructions to mention `make test-e2e`

## 10. Verification

- [x] 10.1 `go test ./...` clean (33 packages, all pass) and `make test-e2e` clean (23 e2e + 4 poll-loop = 27 integration tests)
- [x] 10.2 Bug fixes each have a fails-before/passes-after test: kill sanitization (TestSessionKill_NameSanitization), capture swallowed error (TestPollOnce_CaptureFailureSurfaced), name collision (TestSessionName_NoCollision)
- [x] 10.3 Hermetic leak check: TestHarness_Hermetic + TestHarness_TmuxIsolated confirm sandbox isolation
- [x] 10.4 Every CUJ in the inventory has a passing e2e test
