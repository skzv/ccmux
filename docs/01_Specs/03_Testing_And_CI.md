# Testing, CI, and bug-finding infrastructure

Status: **research + plan, no code yet**.
Owner: skz.

Three open workstreams that all share a common goal — let ccmux survive
adoption by users on machines and configurations we don't directly
control. Today's test suite covers happy-path unit and protocol
behavior; this doc plans the next tier (CI, load, fuzz).

## Workstream 1 — CI integration

### Goal

Every PR to `main` runs the test suite and a cross-compile matrix
automatically. Today this is on the contributor's machine; nothing
prevents a regression from landing if someone skips `go test ./...`.

### Research

**Options surveyed:**

| Provider | Fit | Notes |
|---|---|---|
| **GitHub Actions** | ✅ Recommended | Repo is on GitHub; free for public OSS; Linux + macOS runners both available. tmux is available on every runner via apt/brew. Easy `goreleaser` plug-in path later. |
| Drone CI / Woodpecker | ⚠ Self-host | Cleaner but adds infra. Not worth it for a one-person project today. |
| sourcehut builds | ⚠ Alternative | Smaller community, weaker GitHub integration. |
| Buildkite / CircleCI | ❌ Paid | Overkill until there's a team. |

**Decision: GitHub Actions.** Free, integrates with the PR UI, has
runners for both macOS (the daemon's primary OS) and Linux (the
secondary target). Windows runners exist too but they wouldn't help
until the native-Windows work in [`docs/04_Guides/Windows.md`](../04_Guides/Windows.md) lands.

### Plan

Create `.github/workflows/ci.yml` with three jobs:

1. **`test`** (matrix: ubuntu-latest, macos-latest):
   - Checkout
   - `actions/setup-go@v5` with `go-version-file: go.mod` (no hardcoded
     version; tracks `go.mod`)
   - Install tmux (apt on Linux, preinstalled on macOS)
   - `go test ./...`
   - `go vet ./...`
   - `gofmt -d .` (fail on any diff)

2. **`cross-compile`** (single ubuntu-latest runner):
   - `GOOS=darwin GOARCH=arm64 go build ./...`
   - `GOOS=darwin GOARCH=amd64 go build ./...`
   - `GOOS=linux GOARCH=amd64 go build ./...`
   - `GOOS=linux GOARCH=arm64 go build ./...`
   - `GOOS=windows GOARCH=amd64 go build ./...`
   - `GOOS=windows GOARCH=arm64 go build ./...`

3. **`integration`** (ubuntu-latest, `//go:build integration` tag):
   - Same setup as `test`
   - `go test -tags=integration ./...`
   - These need a working tmux server in the runner's session;
     `tmux new-session -d -s ci -x 200 -y 50` from a setup step
     should be enough. Failing this job blocks the PR but is
     allowed to retry once on flake.

Optional follow-ups once the basics are stable:
   - `staticcheck ./...` step (Charm uses it; ccmux's `make lint`
     already references it)
   - `gosec` for security lint
   - Branch-protection rules requiring `test` + `cross-compile` to
     pass before merge

### Open questions

- Where do we cache the Go module download? `actions/setup-go@v5`
  handles this automatically with `go-version-file`; verify on the
  first run.
- Should integration tests run against multiple tmux versions? Probably
  not worth it today — tmux's CLI surface for our use is stable.
- Code coverage upload (Coveralls / Codecov) — defer to v0.2.

---

## Workstream 2 — Stress testing

### Goal

ccmux today is verified at "one user, four sessions, two devices."
The architecture should hold up at orders of magnitude more, but
nothing currently exercises the upper bound. Establishing realistic
load profiles + a benchmark harness now means we'll catch
regressions (memory leaks, FD leaks, daemon lock contention, poll
loop O(n²) traps) before they hit a power user.

### Research

**Load profiles to model:**

| Profile | Scale | What it stresses |
|---|---|---|
| Power user | 20 sessions, 1 host | Daemon poll loop fan-out, dashboard render with 20-row session list |
| Multi-device | 4 hosts × 10 sessions each | Tailnet discovery probe storm, cross-host project listing aggregation, dashboard refresh cadence |
| Notification storm | 50 sessions all transitioning to `needs_input` within 5s | BEL injection serialization, moshi-hook detection caching, dashboard repaint flicker |
| Long-haul | 4 sessions × 7 days uptime | Memory leak detection, log rotation gaps, FD accumulation, sleep-lock holder lifecycle across many idle→active cycles |
| Daemon churn | 1 ccmux client doing `list` once/sec × 1 hour | Socket dial/teardown overhead, daemon HTTP connection pool, GC pressure |
| Tailnet sprawl | Tailscale status with 100 peers | `tailnet.Scan` performance, /v1/health probe parallelism, hostStatus slice rendering |

**Tools surveyed:**

| Tool | Fit | Notes |
|---|---|---|
| **`go test -bench`** | ✅ For pure-Go hot paths | Already in toolchain. Benchmark the poll loop, the dashboard render, the daemon HTTP handlers. |
| **Custom Go harness** | ✅ For multi-session scenarios | Spawn N fake tmux panes, drive the daemon directly via its HTTP API. More control than `vegeta`/`hey`. |
| **`vegeta` / `hey` / `wrk`** | ⚠ Daemon HTTP only | Useful for raw `/v1/health` and `/v1/sessions` throughput but doesn't model the full flow. |
| **`pprof`** | ✅ Required | Built-in; profile CPU + heap during stress runs to spot O(n²) and accidental allocations. |
| **macOS Activity Monitor / `fs_usage` / `dtruss`** | ✅ For FD / syscall leaks | OS-level — catches things pprof won't (e.g. unclosed pipe). |

### Plan

A new `cmd/ccmux-stress/` binary (build-tagged so it doesn't bloat the
shipped CLI) that drives the load profiles above against a running
daemon. Shape:

```go
// cmd/ccmux-stress/main.go
//
// Stress-test harness. Spawns fake tmux sessions (via tmux directly
// against an isolated socket), exercises the local ccmuxd, and emits
// a one-page summary report: p50/p95 latencies, peak RSS, FD count
// over time, allocations per request.
```

Subcommands:
- `ccmux-stress sessions --count=20` — spawn N fake sessions, poll
  for N minutes, print resource report
- `ccmux-stress notifications --count=50 --burst=5s` — inject N
  needs_input transitions in a burst window, measure bell latency
- `ccmux-stress longhaul --duration=24h` — slow cadence, watch RSS
  + FD count, fail if either climbs unboundedly

Run on a dedicated tmux server socket (`-S /tmp/ccmux-stress.sock`)
so the user's real sessions aren't touched.

Outputs:
- Markdown report dropped at `docs/03_Agent_Logs/stress-<date>.md`
- pprof profiles archived under `bin/profiles/`

### Open questions

- Where should the long-haul test run? Locally is unrealistic (closes
  with the laptop). Possibly a dedicated cron on the Mac mini that
  emails / Moshi-pushes results.
- Memory leak ceiling — what's "unboundedly climbing"? Probably a
  pre-set delta threshold (e.g. >50MB / 24h with steady load means
  fail).
- Notification storm interaction with moshi-hook: do we test against
  a real moshi-hook or stub it? Stub is faster but doesn't catch
  moshi's own backpressure issues.

---

## Workstream 3 — Terminal crawling (fuzz)

### Goal

Find latent bugs in the TUI by exercising it with inputs no human
would think to try: random keystrokes, weird terminal dimensions,
malformed pane content fed to the classifier, glitchy session names,
unicode pathologies. This is the equivalent of "monkey testing" but
for terminal apps.

### Research

**Existing approaches:**

| Approach | Fit | Notes |
|---|---|---|
| **`teatest`** (Charm) | ✅ Foundation | Already in CLAUDE.md's testing plan. Renders the model to a virtual terminal and snapshots. We extend with random input generators. |
| **`testing/quick`** | ✅ For property tests | Standard library; good for the pure functions (`Classify`, `ParseID`, `nextAgent`, …). |
| **`gopter` / `rapid`** | ✅ For richer property tests | Shrinking + state-machine testing. `rapid` is the modern choice; small dep. |
| **`go test -fuzz`** | ✅ For parsers | Go 1.18+ native fuzzer. Perfect for the pane-content classifier, the agent-sidecar parser, the OSC-52 wire format, the tailnet probe shapes. |
| **VT100 emulator + scripted driver** | ⚠ Heavyweight | Like `vt100`/`vt10x` packages. teatest already wraps something similar. |

**Crawl targets (where the bugs probably hide):**

| Surface | Why it's risky |
|---|---|
| **Form keymap** (new-project) | 4 focus stops × 4 input types each = 16 transition pairs. Most aren't covered today. Fuzz: arbitrary key streams + assertions ("name field never contains tab character", "submit only when focus=0..3"). |
| **Pane-content classifier** | Heuristic-based; bizarre pane contents (very long lines, NUL bytes, ANSI escapes inside captured text) could trip `looksLikeClaudePrompt`. Use `go test -fuzz` with the existing test corpus as seeds. |
| **Session name handling** | Session names get used in tmux args + filesystem paths + URLs. Fuzz with names containing `..`, `/`, unicode normalization edge cases, shell metacharacters. |
| **Project name validation** | The daemon's `createProject` already rejects `/`, `\`, and leading dots — but only on those exact bytes. Fuzz for path-traversal cousins (`%2e`, NUL-injection, very long names). |
| **Tailnet hostStatus → dial host** | `shortPeerName`, `dialAddrFor` strip-port, `shortHostname` — already tested but property-test for "round-trip preserves enough info to ssh back." |
| **TUI render under tiny dimensions** | 1×1, 5×5, 80×1, 1×40 — Lipgloss panics on degenerate sizes today? Run teatest with random widths/heights in `[1, 300] × [1, 100]` and assert no panic + non-empty output. |
| **OSC 52 payload encoding** | Already tested against base64 round-trip; extend with fuzz inputs (length 0, length 1MB, every byte value). |

### Plan

Three deliverables:

1. **`internal/agent/...`-style property tests** in each package using
   `rapid`:
   - `internal/claude/claude_test.go` — fuzz `Classify` with random
     ASCII + UTF-8 + ANSI streams; assertion: never panics, always
     returns one of the five State values.
   - `internal/tmux/tmux_test.go` — fuzz `SessionNameForPath` over
     random path strings; assertion: result is a valid tmux session
     name (no `:`, `.`, control chars).
   - `internal/sleeplock/battery_test.go` — fuzz `parsePmsetBatt` with
     random byte slices; assertion: never panics, returns sane
     0≤Percent≤100.

2. **Go native fuzzers** (`FuzzXxx` functions) for parsers:
   - `FuzzParseID` against the agent-id parser
   - `FuzzReadAgent` against arbitrary sidecar contents
   - `FuzzWriteOSC52` then `ReadOSC52` round-trip
   - `FuzzPmsetParse`

3. **`cmd/ccmux-crawl/`** binary: a teatest-powered monkey-tester that
   drives the actual TUI through random navigation + random terminal
   sizes for N iterations, capturing any panic or non-rendering frame
   to a crash log. Modes:
   - `crawl tui --iters=10000` — random key sequences across all
     screens, dimensions resized every 100 iters
   - `crawl form --iters=10000` — focused on the new-project form
   - `crawl resize --iters=1000` — pure resize stress

The crawl binary lands under `cmd/` not `internal/` so it can be
distributed for community bug-bashing.

### Open questions

- Coverage: is `go test -fuzz` enough, or do we need a separate
  long-running fuzzer (OSS-Fuzz integration)? Probably not for v0.1 —
  the surface is small enough to fuzz on-developer-laptop.
- Crash reproducibility: what's the report format? Probably write
  failing seeds to `testdata/fuzz/<func>/` (Go's native fuzzer convention)
  + a small markdown sidecar with the panic stack.
- TUI fuzzing under tmux — does teatest get the same input handling
  the real TUI does? Need to verify before relying on results.

---

## Sequencing

Ordered by ROI / dependency:

1. **CI first (W1).** Cheapest win. Catches regressions today and
   becomes the runner for everything else later. Estimated 1–2 days.
2. **Native fuzzers + property tests (W3 parts 1–2).** Almost free
   once written; runs as part of the CI matrix. ~3 days.
3. **Stress harness (W2).** Most complex; needs care to avoid
   trampling user state. ~1 week including a long-haul run.
4. **TUI crawl (W3 part 3).** Highest novelty but also highest cost
   per bug found. ~1 week; pulls forward to here if stress turns up
   an obvious TUI-side regression we'd rather catch automatically.

## Definition of done

- `.github/workflows/ci.yml` exists; PRs without green CI cannot
  merge into main.
- `cmd/ccmux-stress/` exists with at least three subcommands; running
  it on a dev laptop produces a markdown report.
- `cmd/ccmux-crawl/` exists with TUI + form modes; a 10k-iter run
  on main passes (no panics).
- Property tests via `rapid` cover at least 5 of the surfaces listed
  in "Crawl targets" above.
- Native fuzzers exist for the four parsers listed under "Plan part 2".
- This doc updated with deferred items as they ship.
