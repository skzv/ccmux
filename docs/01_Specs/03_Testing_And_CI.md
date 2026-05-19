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

3. **`integration`** (matrix: ubuntu-latest, macos-latest;
   `//go:build integration` tag):
   - Same setup as `test`
   - `go test -tags=integration ./...`
   - These need a working tmux server in the runner's session;
     `tmux new-session -d -s ci -x 200 -y 50` from a setup step
     should be enough. Failing this job blocks the PR but is
     allowed to retry once on flake.
   - **macOS coverage is non-negotiable here.** ccmux's primary
     deployment target is macOS (daemon runs caffeinate on darwin,
     sleeplock's `pmset` battery reader is darwin-only, moshi-hook
     and the OSC 52 terminal-compat checks are macOS-flavored).
     Linux-only integration coverage would miss every regression in
     the most-used code path.

Optional follow-ups once the basics are stable:
   - `staticcheck ./...` step (Charm uses it; ccmux's `make lint`
     already references it)
   - `gosec` for security lint
   - Branch-protection rules requiring `test` + `cross-compile` to
     pass before merge

### Decisions (resolved from research)

**Caching.** Resolved: use `actions/setup-go@v5` with `cache: true`
(default ON since v4) and `cache-dependency-path: go.sum` so the cache
key is computed from `go.sum` rather than `go.mod` — `go.mod` changes
less often than transitive deps, so a `go.sum`-keyed cache invalidates
exactly when it should. The action delegates to `actions/cache`
internally and caches both the module download (`$GOMODCACHE`) and the
build cache (`$GOCACHE`). If the cache lookup fails the action prints
a warning and the job proceeds anyway. (Source:
<https://github.com/actions/setup-go>.)

**tmux versioning.** Resolved: pin to whatever the runner image
provides; don't matrix across versions. Concrete numbers as of 2026-05:
`ubuntu-latest` (Ubuntu 24.04) ships **tmux 3.4**; `macos-latest`
(macOS 14) ships **tmux 3.5a** via the preinstalled Homebrew. ccmux
uses only the stable subset (`has-session`, `list-sessions -F`,
`new-session -d -s`, `kill-session`, `rename-session`, `send-keys`,
`capture-pane -p`, `set-option`, `show-options -g`) plus the
`#{session_*}` format keys, all of which have been stable since
tmux 2.1 (2015). Multi-version testing would burn runner minutes
without finding bugs. Revisit if/when ccmux starts using a 3.x-only
feature.

**Coverage upload.** Resolved: defer Codecov / Coveralls. Instead,
have the CI `test` job emit `-coverprofile=coverage.out` and upload
the file as a workflow artifact (free, no third-party dependency, lets
a reviewer download and inspect locally with `go tool cover`).
Migrate to Codecov when the project has external contributors who
want a coverage badge / PR comment; until then the artifact is plenty.

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

### Decisions (resolved from research)

**Long-haul host.** Resolved: dedicated launchd job on the Mac mini.
Why: GitHub-hosted runners cap at **6h per job, 72h per workflow run**
— a 24h stress run is impossible on `macos-latest`. Self-hosted GHA
runners can exceed the per-job limit but still hit the 72h workflow
cap, and require us to keep a runner registered. The Mac mini is
already always-on (it runs ccmuxd itself, hosts remote-attach
targets, etc.), so a daily launchd plist that invokes
`ccmux-stress longhaul --duration=24h` and posts a report through
moshi-hook (or just writes to `~/.local/state/ccmux/stress-reports/`)
matches the project's actual deployment model. Cost: zero. (Source:
<https://github.com/orgs/community/discussions/26679>.)

**Memory leak threshold.** Resolved with measured baseline.

Real numbers from this dev machine on 2026-05-12 (two ccmuxd
processes alive — both tracking the same two sessions):

  pid 48190  RSS 21.1 MB   (4h47m uptime)  ← well-behaved
  pid 48205  RSS 388.7 MB  (4h47m uptime)  ← anomaly

That 18× RSS divergence between two daemons doing nominally the same
work IS the kind of regression the stress test should catch. Concrete
thresholds for `ccmux-stress longhaul`:

- **Fail** if RSS at any sample > **150 MB** (absolute ceiling — well
  above the healthy baseline, well below the anomaly we just
  observed).
- **Fail** if RSS at end-of-run > 3× RSS at start-of-run *and* the
  delta is > 30 MB (relative ratio — catches the slow-growth case
  where absolute stays under 150 MB but the trajectory is wrong).
- **Warn** (don't fail) if FD count grows by > 50 over the run —
  hardcoded threshold because macOS makes accurate FD counts a
  sudo-only operation; warning is what the report should surface
  rather than killing the run.

These numbers explicitly trace back to a real measurement so the
spec doesn't drift to wishful thinking. Update them when the
baseline shifts (e.g. when we add SQLite metrics persistence).

**Moshi interaction.** Resolved: stub by default, real-moshi smoke
test once per release.

- The default `ccmux-stress notifications` run replaces
  `moshi.Detect` with a fixed `Status{Paired: true, …}` via a build-
  tagged `_stress.go` file. Faster runs (no `moshi-hook status` shell
  out per probe), reproducible across machines, doesn't require
  pairing the dev machine before each run.
- A separate `ccmux-stress notifications --real-moshi` flag uses the
  actual moshi.Detect path. Run before each tagged release as a
  smoke test for the bell-suppress + push-categorize pipeline. Don't
  run it on every PR — moshi-hook isn't on every CI runner anyway.

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

### Decisions (resolved from research)

**Fuzzer scope.** Resolved: `go test -fuzz` only. Skip OSS-Fuzz.

OSS-Fuzz's stated criteria are "significant user base and/or critical
to the global IT infrastructure"; acceptance is decided case-by-case
by the OSS-Fuzz maintainers and weighs remote-attack exposure +
dependent-user count. ccmux is alpha, single-author, and doesn't
process untrusted network input from anyone except the user's own
tailnet peers — none of those gates apply today. (Source:
<https://google.github.io/oss-fuzz/getting-started/accepting-new-projects/>.)

Practical replacement: every PR runs the fuzz targets with a SHORT
per-target budget (`-fuzztime=10s`, driven by `make fuzz FUZZTIME=10s`
in CI). That's a smoke check — enough to catch a freshly-broken
invariant on the same PR that introduced it, not enough to discover
deep bugs. Two longer entry points exist for that:

- **Contributor local**: `make fuzz` (default `FUZZTIME=5m`, ≈35min
  for all 7 targets) is the bar before tightening a parser or after
  touching a heuristic surface. `make fuzz-quick` (10s/target, ≈70s
  total) mirrors CI exactly for fast iteration. `make fuzz FUZZTIME=1h`
  is the pre-release overnight sweep.
- **Planned nightly cron**: a Mac mini job that runs `make fuzz
  FUZZTIME=1h` and opens a PR with any failing seeds it finds.

The target list lives in `FUZZ_TARGETS` in the root `Makefile` —
single source of truth. CI's fuzz step is a one-line `make fuzz
FUZZTIME=10s` so a contributor reproduces CI's behavior byte-for-byte
with the same Makefile.

Failing seeds get auto-archived into `<pkg>/testdata/fuzz/<FuzzName>/`
(Go's standard convention) and stay there as regression seeds.

**Crash reproducibility.** Resolved: Go's standard convention plus a
sidecar.

Go's fuzzer writes failing inputs to
`testdata/fuzz/<FuzzName>/<sha>` automatically; those files are
binary and serve as deterministic regression seeds on the next
`go test` run. Convention works as-is. Two small additions:

- For every NEW failing seed, the harness also writes a sibling
  `<sha>.md` next to it containing: the panic stack, the OS / arch /
  Go version it was caught on, and the timestamp. The binary seed
  reproduces the bug; the markdown sidecar tells a future reader
  what went wrong without having to re-run.
- Failing seeds are committed (`git add testdata/fuzz/...`) so they
  permanently lock down the regression. Storage cost is negligible
  (each seed is < 1 KB; sidecars are bigger but still small).

(Source: <https://go.dev/doc/security/fuzz/>,
<https://go.dev/doc/tutorial/fuzz>.)

**teatest input fidelity.** Resolved: teatest is fine for ccmux's
model-level testing, but it's NOT a substitute for raw-terminal-byte
fuzzing.

Teatest's `Send()` accepts a `tea.Msg` (e.g. `tea.KeyMsg{Type: tea.KeyTab}`)
and pushes it straight into the program's message queue. Output is
captured to an in-memory `bytes.Buffer` wrapped with mutex locks and
`tea.WithANSICompressor()`. There is **no virtual terminal emulator**;
the model's `View()` output is the raw bytes the bubbletea renderer
would have written to stdout. (Source: reading
`exp/teatest/teatest.go` in
<https://github.com/charmbracelet/x/tree/main/exp/teatest>.)

What this means for ccmux:

- ✅ Model-level fuzz works perfectly. Sending random KeyMsgs at the
  new-project form, the dashboard, etc. exercises every code path
  the real TUI exercises once a keystroke has been parsed into a
  KeyMsg. The existing `newproject_test.go` already does this.
- ✅ WindowSizeMsg and MouseMsg work the same way — random resizes
  at random dimensions are testable.
- ⚠ The byte-stream parser inside bubbletea (the layer that turns
  `\x1b[A` into `tea.KeyMsg{Type: tea.KeyUp}`) is upstream code we
  don't own. teatest bypasses it. If we ever care about how ccmux
  handles malformed escape sequences (we probably don't — that's
  bubbletea's job), we'd need a separate harness driving a real PTY
  via `expect`-style tooling. Out of scope for v1.

The crawl plan stays as written: teatest covers the model layer
(plenty), `go test -fuzz` covers the parser layer (pane classifier,
sidecar, OSC 52 wire format) where we actually own the parsing code.

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
