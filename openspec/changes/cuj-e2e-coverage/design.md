## Context

ccmux is a TUI (`ccmux`) + daemon (`ccmuxd`) over tmux. Today's 75 test files cover pure helpers, parsers, protocol round-trips, and `teatest` model snapshots — all of which stop at the `internal/tmux` boundary. No test starts a real tmux server, so the glue between TUI → `daemon.Client` → `ccmuxd` → `internal/tmux` → `tmux(1)` is unverified end-to-end. CLAUDE.md and `docs/01_Specs/03_Testing_And_CI.md` both describe `//go:build integration` tests "against a real tmux server"; that tier was specified but never built.

Constraints carried from the existing testing doc: GitHub Actions, runners ship tmux 3.4 (Linux) / 3.5a (macOS), ccmux uses only the stable tmux subset, macOS coverage is non-negotiable (caffeinate, pmset, moshi paths are darwin-flavored). The harness must never touch the developer's real sessions, `~/.claude`, or `~/.config/ccmux`.

## Goals / Non-Goals

**Goals:**
- A canonical, documented inventory of every key Critical User Journey, each pinned to an entry point and an observable end state.
- A hermetic e2e harness: isolated tmux server, temp `$HOME`, temp projects root, temp config — verified to leak nothing.
- One automated e2e test per key CUJ, driving the real `ccmux`/`ccmuxd` artifacts (CLI/daemon) or the real `App` model (TUI) against a real tmux server.
- Every bug fixed under this change has a test that **fails before, passes after** (the CLAUDE.md bar).
- `make test-e2e` plus a CI integration job, so e2e runs on every PR on Linux and macOS.
- Scoped bug fixes and simplification confirmed green by the new suite.

**Non-Goals:**
- Truly interactive `tmux attach` / `mosh` over a real controlling terminal in CI — covered by command-construction assertions + session-state checks, not a live attach.
- Real multi-host Tailscale networking — "remote" CUJs use a second in-process daemon on loopback TCP and assert protocol parity.
- Load / stress / long-haul — that is the separate `cmd/ccmux-stress` workstream.
- A wholesale rewrite of `App.Update` — only the seams needed for deterministic testing.
- The iOS/Moshi push pipeline beyond asserting the BEL byte the daemon injects.
- Windows e2e — cross-compile sanity only, per the existing CI plan.

## Decisions

**1. Tag with `//go:build integration`, harness in `internal/e2e`.**
Reuse the tag CLAUDE.md and the CI plan already name rather than inventing `e2e`. e2e tests are the CUJ-level subset of integration-tagged tests. Shared setup (tmux server, temp env, daemon spawn, assertion helpers) lives in `internal/e2e`; per-CUJ tests live in `internal/e2e/*_test.go` so they can drive multiple packages. `go test ./...` (untagged) is unchanged and stays fast. Alternative considered: a separate untagged `internal/e2e` package run always — rejected because real-tmux tests are slower and need tmux installed, which not every contributor build env guarantees.

**2. Isolate tmux via `TMUX_TMPDIR` + a unique server name per test.**
Each test sets `TMUX_TMPDIR` to a `t.TempDir()` and uses a unique tmux server (`-L ccmux-e2e-<n>` / dedicated socket). Child `tmux` processes spawned by `internal/tmux` inherit the env, so the entire server is sandboxed with no production-code change. `t.Cleanup` runs `tmux kill-server` on that socket. If the `internal/tmux` wrapper turns out to hardcode a socket path, add a minimal, test-only seam (a package var or context value) rather than threading a socket arg through every call. Alternative: a shared CI-wide tmux server — rejected, tests would race on session names and cleanup.

**3. Three harness layers, matched to the CUJ surface.**
- *CLI e2e* — build `ccmux`/`ccmuxd` once, run them as real subprocesses against the sandboxed tmux + temp `$HOME`. Tests the actually-shipped artifacts (`list --json`, `new`, `kill`, `doctor`, `host`).
- *Daemon e2e* — spawn the real `ccmuxd` on a Unix socket under the temp dir; drive it through `daemon.Client`; assert poll, classification, bell, and the tailnet HTTP API against the sandboxed tmux.
- *TUI e2e* — `teatest` drives the real `App` model in-process, but session operations hit the real sandboxed tmux. teatest has no terminal emulator and cannot perform an interactive attach, so TUI attach CUJs assert the constructed exec command + that the session exists and is well-formed.

**4. Make the daemon poll deterministic.**
Polling on a 2s wall-clock timer makes tests either slow or flaky. Add a test seam to trigger one poll cycle synchronously (exported `PollOnce`-style entry, or an injectable interval/tick channel). Tests then create a session, trigger exactly one poll, and assert classification — no `time.Sleep`. This is also a small production improvement (the poll loop becomes unit-addressable).

**5. "Remote" host CUJs use a loopback daemon.**
Server-mode CUJs (remote session listing, tailnet HTTP API parity) start a second `ccmuxd` bound to `127.0.0.1:<port>` and register it as a host in the temp config. This exercises the full HTTP client/server path and the socket-vs-HTTP protocol parity without a real tailnet. Tailscale peer discovery (`tailnet.ScanTailnet`) is stubbed/skipped — it shells out to `tailscale` which CI lacks.

**6. Bug fixes are test-driven, candidates are leads not facts.**
The proposal's bug list is the output of a fast read-only scan and includes unverified line numbers. Implementation confirms each against current code first. Workflow per bug: write the e2e (or unit) test that reproduces it red, fix, confirm green. Simplification is scoped to code the e2e work touches — unify duplicated session-name sanitization, narrow mutex scope around slow `tmux` calls, drop dead fields — not a blanket refactor.

**7. Sleep-lock CUJ asserts the state machine, not a real system lock.**
e2e must not hold a real `caffeinate`/`systemd-inhibit` lock (flaky, privileged, machine-affecting). Assert the sleep manager's decision (active sessions ⇒ wants lock; all idle ⇒ releases) behind an injected fake inhibitor. Add the seam if the manager doesn't already have one.

## Risks / Trade-offs

- **tmux not installed / version drift on a contributor machine** → integration tag keeps `go test ./...` working without tmux; the harness `t.Skip`s with a clear message if `tmux` is absent. Pin to runner-provided versions, stable subset only (already a resolved decision in the testing doc).
- **Flaky timing in poll/classify tests** → mitigated by Decision 4: synchronous single-poll trigger, no sleeps.
- **e2e suite slow enough to discourage running it** → keep the harness lean, build binaries once per `go test` invocation, run independent CUJs in parallel (socket isolation makes this safe), keep the budget within a couple of minutes.
- **Interactive attach genuinely uncovered** → accepted as a Non-Goal; mitigated by asserting exact command construction and post-conditions. A PTY-backed smoke test (`creack/pty`, test-only dep) is a possible stretch, deferred.
- **A bug fix changes behavior a hidden consumer relied on** → bug fixes correct observably-wrong behavior only; the new e2e suite plus the existing 75 unit tests are the regression net; no daemon-protocol/CLI/config schema changes.
- **macOS-only paths (caffeinate, pmset, moshi)** → CI integration job runs the matrix on both ubuntu and macos; darwin-specific CUJs guard with `runtime.GOOS`.

## Migration Plan

Additive and low-risk — this change ships tests, a Make target, a CI job, and scoped fixes. Sequence: (1) land the harness + CUJ inventory; (2) add CUJ tests incrementally, each with its bug fix if one is found; (3) add `make test-e2e` and the CI integration job; (4) correct CLAUDE.md / the testing doc. Rollback is a plain revert — no data, no schema, no deployed surface. Bug-fix commits are independently revertible since each carries its own failing-then-passing test.

## Open Questions

- Does `internal/tmux` honor `TMUX_TMPDIR` cleanly, or is a small socket seam needed? (Resolve first — it gates the harness.)
- Is the daemon poll interval already injectable, or does Decision 4 need a new seam?
- Does the sleep manager already abstract the inhibitor, or must Decision 7 add the interface?
- Include the PTY-backed attach smoke test now, or defer it past this change?
