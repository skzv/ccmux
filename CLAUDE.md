# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

# Project Identity

**ccmux** — a TUI for cross-device AI-coding-agent session management on top of tmux, Mosh, Tailscale, and Obsidian. Built in Go using the Charm stack (Bubble Tea, Lipgloss, Bubbles, Huh, Glamour).

The pitch: one tool to start, resume, and supervise every coding-agent session you've got running — Claude Code, Codex, Cursor, Grok, and more — from anywhere: your Mac, your iPhone, or any device on your tailnet. tmux keeps each session alive; ccmux is the single dashboard over all of them, color-coded by what each agent is doing, on every device you own. Obsidian-backed project context follows you.

# Architecture (one-screen summary)

Every machine that installs ccmux can act as a **client**, a **server**, or **both**. There is one binary trio: `ccmux` (TUI/CLI), `ccmuxd` (daemon), and `ccmux-mcp` (MCP server for coding agents).

- **Local mode** — `ccmux` connects to the local `ccmuxd` via Unix socket at `~/.local/state/ccmux/ccmuxd.sock`. Manages tmux/agent sessions on this machine. Holds a `caffeinate`/systemd-inhibit lock (`internal/sleeplock`) while sessions are active so the laptop doesn't sleep with the lid closed (on AC power).
- **Server mode** — `ccmux` on machine A connects to `ccmuxd` on machine B over Tailscale via an HTTP API bound to the tailnet IP (`100.x.x.x:7474`). Lists remote sessions. Attach action execs `mosh B -- tmux attach -t <session>`. Pairing (`ccmux pair`) registers phones so the daemon can push APNs/FCM notifications.
- **Mixed** — the dashboard shows local sessions _and_ sessions on each configured remote host, color-coded by origin.

**The core abstraction is the agent strategy interface** (`internal/agent.Agent`): one implementation per CLI — Claude, Codex, Antigravity, Cursor, Pi, Grok, plus a second wave (OpenCode, Kimi, Droid, Copilot, Qoder, Kilo, Hermes, Amp, Kiro). Every layer that used to hardcode "claude" (launch command, state classifier, usage walker, config tab, initial prompt) now goes through this interface. Which agent a project runs is recorded per-project in `<project>/.ccmux/agent` (`internal/project.ReadAgent` / `SetAgent`); missing file → Claude for back-compat. State detection is data-driven: `internal/agentdetect` matches per-agent TOML rule files against pane body + OSC title, falling back to the legacy time-based "went quiet = needs input" heuristic and to `internal/claude`'s fixture-pinned classifier. See `docs/01_Specs/02_Multi_Agent.md`.

Components:

- **`cmd/ccmux`** — the user-facing binary. Default behavior launches the TUI. Subcommands provide scripting hooks: `new`, `attach`, `list`, `kill`, `rename`, `resume`, `project`, `setup`, `setup-ssh`, `doctor`, `host add/remove/list`, `pair`, `shell`, `agents` (`models`, `set-default-model`), `notes` (`list`/`read`/`search`), `list-conversations` / `delete-conversation`, `mcp`, `clipboard-pipe`, `moshi-setup`, `daemon start/status/stop/restart/install/uninstall/unit`, `update`, `uninstall`. The feature-surface policy below requires every feature to be reachable from both the TUI and one of these.
- **`cmd/ccmuxd`** — background daemon. Polls tmux pane content + OSC pane titles, classifies each session via the project's agent, rings the terminal bell and sends APNs/FCM pushes on needs-input transitions, holds the sleep lock, serves the HTTP API over the Unix socket and (optionally) the tailnet listener. Socket binding is race-guarded (bind-lock + handoff probe) so a second daemon exits cleanly instead of orphaning the socket. Service management lives in `internal/daemonservice` (launchd/systemd, build-tagged). White-box poll-loop tests live here (`pollOnce` with injectable `capture`/`paneTitle`/`bell` seams).
- **`cmd/ccmux-mcp`** — MCP server (JSON-RPC 2.0 over stdio) that exposes ccmux to coding agents. Proxies to the local ccmuxd (or a tailnet peer via `CCMUX_HOST`). Read-only by default; `--allow-mutate` exposes spawn / send-keys / kill. See `docs/01_Specs/04_MCP_Server.md`.
- **`cmd/ccmux-crawl`** — teatest-driven monkey-tester for the TUI. **`cmd/ccmux-stress`** — daemon load harness (20+ sessions, notification storms, long-haul, pprof/FD-leak). Both are testing tooling, not user binaries.
- **`internal/agent`** — the strategy interface + all implementations. `Agent` methods: identity (`ID`/`DisplayName`/`Binary`), `LaunchCmd(continueFlag)`, `ConfigRoot` (which config file the Agents tab shows), `TranscriptsRoot` (what the usage walker reads), `InitialPrompt`, `Classify`. `ByID`/`ParseID`/`All()` must stay in sync (test-enforced). Also handles model pinning (`ANTHROPIC_MODEL`), OpenRouter routing injection, and PATH-based executable discovery.
- **`internal/agentdetect`** — the state-detection engine. Rule files under `internal/agentdetect/rules/*.toml` (one per agent) match pane body + OSC title; `require_idle` rules re-apply the idle gate to avoid spurious bells. This is the surface the `FuzzClassify` fuzzer and the CI 100k-exec pass protect.
- **`internal/tui`** — Bubble Tea models and screens. `app.go` is the router; screens include Dashboard, Sessions, Projects, Notes, Conversations, Agents, Network, Settings, Setup. A matrix/adaptive layout renders phone-width terminals. `internal/tui/styles` (design tokens) + `internal/tui/components` (shared chrome) — screen files must obey the styling lint below.
- **`internal/tmux`** — wrapper around the tmux CLI. All session operations (`new`, `attach`, `kill`, `list`, `capture-pane`, `pane-title`, `rename`, `send-keys`) go through here; no direct shell-outs from the TUI layer. `internal/tmuxchrome` styles the tmux status bar.
- **`internal/daemon`** — IPC client + server protocol. `Client` (used by the TUI, CLI, and MCP), `TokenStore` (pair tokens), `EventBus` (SSE to /v1/events), `DeviceStore` (paired phones). The wire types (`SessionState`, `NewSessionRequest`, …) are the protocol — `agent.State` strings must not drift.
- **`internal/project`** — discovers projects under `~/Projects` (configurable); a "project" is any directory with `CLAUDE.md` or a `.git`. Agent sidecar read/write. `internal/scaffold` bootstraps new projects (dir + agent session + sidecar).
- **`internal/notes`** — notes ops across each project's whole markdown tree (VCS/dependency/build dirs pruned; new notes under `docs/`). Glamour rendering, templated frontmatter, ripgrep search, Obsidian `obsidian://` URI builder.
- **`internal/conversations`** — cross-agent transcript listing (Claude/Codex/Cursor/Antigravity), headless/SDK runs excluded by default.
- **`internal/usage`** — per-agent token/cost aggregation; delegates Claude to `internal/claudeusage` (the rich walker driving the 5-hour quota bar), with per-agent walkers (`internal/agentusage`, `internal/codexusage`, …) and `internal/openrouterusage` for OpenRouter spend.
- **`internal/claude` / `internal/claudemodels`** — Claude transcript + classifier; live model catalog (Anthropic Models API, 24h refresh, disk cache, curated in-binary fallback).
- **`internal/claudeconfig` / `internal/codexconfig` / `internal/cursorconfig` / `internal/antigravityconfig`** — read/write each agent's own config files, backing up before mutation and preserving unknown JSON fields. Powers the per-agent config tabs.
- **`internal/claudeauth`** — reads `claude auth status` JSON (cached 5min) for subscription-tier auto-detection.
- **`internal/config`** — `~/.config/ccmux/config.toml` preferences (projects dir, theme, keybindings, `[agents]`, `[sessions]`, `[openrouter]`, `[apns]`, `[fcm]`). Treat it as secret-bearing when APNs/FCM/OpenRouter keys are set.
- **`internal/sshsetup`** — one-time SSH bootstrap for remote hosts: probe, password-bootstrap key install via `golang.org/x/crypto/ssh`, post-auth user enumeration, TOFU `known_hosts`. Shared by the Network-screen `s` key, `ccmux setup-ssh`, `doctor`, and the post-attach-failure auto-route. See `docs/04_Guides/SSH_Setup.md`.
- **`internal/tailnet` / `internal/moshi` / `internal/remoteattach` / `internal/apns` / `internal/fcm` / `internal/clipboard` / `internal/keychain`** — tailnet peer scanning, Moshi status, remote attach, Apple/Google push senders, OSC 52 cross-device clipboard, secrets storage.
- **`internal/setupwizard` / `internal/selfupdate` / `internal/ghauth`** — first-run wizard, `ccmux update` via GitHub releases, GitHub auth for release metadata.

# Build & Run

Requires Go 1.26+. On macOS, the build ad-hoc codesigns each binary so GateKeeper accepts it, and `make install` strips `com.apple.provenance` + re-signs.

```bash
make build         # builds bin/ccmux, bin/ccmuxd, bin/ccmux-mcp
make install       # installs all three to ~/.local/bin/ (cp-then-rename so a running binary can be replaced), restarts ccmuxd
make setup         # build + install + interactive setup wizard (idempotent)
make bootstrap     # dep-check (go/git/make/brew) then chains into setup
make run           # go run ./cmd/ccmux   (alias: make tui)
make daemon        # go run ./cmd/ccmuxd
make mcp           # build just bin/ccmux-mcp
make test
make lint          # gofmt + go vet + staticcheck if installed
make fuzz          # 5min/target over FUZZ_TARGETS (≈40min total)
make fuzz-quick    # FUZZTIME=100000x — mirrors CI's PR-time smoke pass
make test-e2e      # builds binaries, runs the integration suite (needs tmux)
make tapes         # render all VHS demo tapes to docs/vhs/out/ (needs vhs + ffmpeg)
make tapes-check   # cheap CI check: CUJ catalog ⇄ tape/GIF artifact invariant
make brew-test     # sandbox-HOME test of the Homebrew formula
```

# Conventions

- **Standard layout:** code under `cmd/` and `internal/`, no top-level `pkg/` directory unless a stable external API is committed.
- **Errors:** wrap with `fmt.Errorf("...: %w", err)`. No naked returns of errors from anywhere user input touches.
- **Logging:** use `charmbracelet/log`. The daemon logs to `~/.local/state/ccmux/ccmuxd.log`. The TUI logs to the same file (never stdout — it corrupts the alt-screen).
- **TUI:** keep Bubble Tea models small and composable. Each screen is its own model implementing `tea.Model`. The root model is a router.
- **Styling:** all colors, shapes, and spacing values live in `internal/tui/styles/` (tokens) and `internal/tui/components/` (shared chrome). Screen files (anything under `internal/tui/` outside those two packages) MUST NOT introduce a literal hex color (`lipgloss.Color("#…")`) or a bare integer in `.Padding(…)` / `.Margin(…)` / `.Padding<Side>(…)` / `.Margin<Side>(…)` — pull from `s.Spacing.{XS,SM,MD,LG,XL}` and `s.Semantic.*` / `s.P.*` instead. The lint check in `internal/tui/styles_lint_test.go` enforces both rules. Theme is loaded once at startup.
- **Subprocess discipline:** every `exec.Command` call must take a `context.Context` so the TUI can cancel hung shells. The daemon additionally caps request bodies (64 KiB) and sets read deadlines so a tailnet peer can't pin a handler goroutine.
- **Adding an agent:** drop a new `Agent` implementation in `internal/agent/`, register it in `All()` / `ByID()` / `ParseID()` (the test suite enforces the three stay in sync), and add a rule file under `internal/agentdetect/rules/` so state detection covers it.

# Testing

- Unit tests for `internal/tmux` and `internal/project` use table-driven tests against fake `tmux` outputs.
- TUI screens get golden-file tests via `teatest` (Charm's snapshot tester); `cmd/ccmux-crawl` is the teatest-based monkey-tester.
- **Integration / e2e tests** are tagged `//go:build integration` and live in two places:
  - `internal/e2e/` — hermetic subprocess tests (real `ccmux` + `ccmuxd` binaries, isolated tmux server via `TMUX_TMPDIR`, temp `$HOME`). Covers session/project/notes/conversations lifecycle, daemon IPC, onboarding, MCP robustness, and PTY-driven TUI flows.
  - `cmd/ccmuxd/` — daemon poll-loop white-box tests (direct `pollOnce` calls, injectable `capture` + `paneTitle` + `bell` seams). Covers classify, bell, capture-failure, bind-lock, pairing.
  - Run with: `make test-e2e` (builds binaries then runs both packages). Requires `tmux` on PATH.
  - CI: the `integration` job in `.github/workflows/ci.yml` runs `make test-e2e` on ubuntu-latest and macos-latest.
- **Fuzz targets.** Native Go fuzzers cover the parsers + heuristic surfaces. The root `Makefile`'s `FUZZ_TARGETS` variable is the single source of truth used by CI and locally — adding a `FuzzXxx` target means appending to it. CI runs `make fuzz FUZZTIME=100000x` (an **execution-count** budget per target, not wall-clock: short `-fuzztime=10s` runs flake with `context deadline exceeded` at shutdown). That is just enough to catch a freshly-broken invariant on the PR that introduced it. Run deeper sweeps locally when you touch one of those surfaces:
  - `make fuzz` — 5min/target (≈40min total), the default for "I want real coverage"
  - `make fuzz-quick` — 100000x/target, mirrors CI exactly
  - `make fuzz FUZZTIME=1h` — overnight sweep before a release
    Failing seeds auto-archive under `<pkg>/testdata/fuzz/<FuzzName>/<sha>` (Go's native convention) — commit them so `go test ./...` picks them up as regression seeds.
- **Always run `go test ./...` before every commit.** A clean suite is a precondition for `git commit` in this repo, not a follow-up. If any test fails, fix it (or mark + explain the skip) before the commit. Cross-compile sanity (`GOOS=windows`, `GOOS=linux`) is part of "tests pass" for changes that touch OS-specific code.
- **Every new feature ships with tests.** No PR should add user-visible behavior without at least one test that fails before the change and passes after. The bar is "exercised in code" — unit tests for pure helpers, fake-driven tests for protocol changes, table-driven keymap tests for new TUI bindings, live-driven integration tests for things that need a real tmux/daemon. A feature without a test is unfinished work, not a candidate for review.
- **Stress + crawl tooling exists but has no CI slot yet.** `cmd/ccmux-stress` and `cmd/ccmux-crawl` are run manually. Nightly long-budget fuzz (a Mac mini cron running `make fuzz FUZZTIME=1h` and committing seeds back as PRs) remains a TODO — CI's 100k-exec/target pass is intentionally a smoke check, not a deep search. Details in `docs/01_Specs/03_Testing_And_CI.md`.

# Feature surface policy

- **Every feature must be reachable from both the TUI and the CLI.** Pick a key/screen in the TUI _and_ a `ccmux <subcommand>` (or flag on an existing one) — never one without the other. Reasoning: the TUI is the daily driver, the CLI is for muscle memory + scripting; shipping a feature in only one creates a discoverability cliff and breaks the "TUI-first, CLI when you want it" promise on the front page.
- Concretely, a PR that adds a new behaviour should land:
  - The implementation in `internal/...`.
  - A TUI affordance: keybinding, screen, form row, or detail-pane action wired through `internal/tui`.
  - A CLI affordance: a Cobra subcommand in `cmd/ccmux/cmd/` (new file, or a flag added to an existing one).
  - Tests that exercise the implementation directly _and_ at least one of the two surfaces.
  - Updates to the README + the website's docs/MDX where the feature is user-visible.
- Acceptable exceptions: telemetry/internals that have no user-facing semantics (e.g. the bell-suppress predicate); pure refactors; private daemon endpoints that ship alongside a TUI/CLI consumer in the same PR.

# Docs Map

- `docs/01_Specs/00_Vision.md` — the why and the user story
- `docs/01_Specs/01_Feature_Catalog.md` — every feature, scoped to a release phase
- `docs/01_Specs/01_CUJ_Catalog.md` — the CUJ list; source of truth for the VHS tapes (`make tapes-check` enforces tape/GIF ↔ catalog sync)
- `docs/01_Specs/02_Multi_Agent.md` — multi-agent spec and the three locked decisions (per-project switchable agent, `c-` prefix retained, BEL-only notifications for non-Claude in v1)
- `docs/01_Specs/03_Testing_And_CI.md` — testing + CI strategy, stress/crawl TODOs
- `docs/01_Specs/04_MCP_Server.md` — the ccmux-mcp server spec
- `docs/02_Architecture/00_System_Design.md` — components, data flow, daemon protocol
- `docs/02_Architecture/01_Notes_System.md` — markdown-on-disk model, TUI Notes tab, optional Obsidian paths
- `docs/02_Architecture/02_iOS_Mobile_Setup.md` — Moshi + moshi-hook (primary); Blink/Termius (fallback)
- `docs/02_Architecture/03_Tailscale_Networking.md` — how Tailscale sits underneath the whole stack
- `docs/02_Architecture/04_Agents.md` / `docs/02_Architecture/05_HTTP_API.md` — agent support details; the ccmuxd HTTP API (tailnet-safe routes vs local-only)
- `docs/02_Architecture/04_TUI_Design_System.md` — tokens, components, modal exception rule, indent hierarchy
- `docs/04_Guides/` — user-facing setup guides (SSH_Setup, Windows) published to the README
- **OpenSpec workflow:** `openspec/specs/*/spec.md` holds accepted specs; `openspec/changes/*/{proposal,design,tasks}.md` holds work in flight (archive into `openspec/changes/archive/` on completion). New feature work that would normally be a one-line TODO should start as an openspec change proposal.

# Owner

Alexander "Sasha" Kuznetsov — me@skz.dev
