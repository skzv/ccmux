# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

# Project Identity
**ccmux** — a TUI for Claude Code session management on top of tmux, Mosh, Tailscale, and Obsidian. Built in Go using the Charm stack (Bubble Tea, Lipgloss, Bubbles, Huh, Glamour).

The pitch: one tool to start, resume, and supervise Claude Code sessions from anywhere — your Mac, your iPhone, or any device on your tailnet — with Obsidian-backed project context that follows you.

# Architecture (one-screen summary)

Every machine that installs ccmux can act as a **client**, a **server**, or **both**. There is one binary pair: `ccmux` (TUI/CLI) and `ccmuxd` (daemon).

- **Local mode** — `ccmux` connects to the local `ccmuxd` via Unix socket. Manages tmux/Claude sessions on this machine. Holds `caffeinate -s` while sessions are active so the laptop doesn't sleep with the lid closed (on AC power).
- **Server mode** — `ccmux` on machine A connects to `ccmuxd` on machine B over Tailscale via an HTTP API bound to the tailnet IP (`100.x.x.x:7474`). Lists remote sessions. Attach action execs `mosh B -- tmux attach -t <session>`.
- **Mixed** — the dashboard shows local sessions *and* sessions on each configured remote host, color-coded by origin.

Components:
- **`cmd/ccmux`** — the user-facing binary. Default behavior launches the TUI. Subcommands provide scripting hooks (`new`, `attach`, `list`, `kill`, `setup`, `doctor`, `host add/remove/list`).
- **`cmd/ccmuxd`** — background daemon. Polls tmux pane content, detects when Claude is waiting for input, rings the terminal bell so the iOS terminal client raises a push notification. Manages `caffeinate` lock for sleep prevention. Persists session metrics to SQLite. Exposes both a local Unix socket *and* (optionally) a tailnet-bound HTTP API for remote ccmux clients.
- **`internal/tui`** — Bubble Tea models and screens. Top-level app routes between Dashboard, Sessions, Projects, Notes, Setup, and Settings screens. Lipgloss handles styling, Bubbles provides the list/table/textinput/viewport widgets.
- **`internal/tmux`** — wrapper around `tmux` CLI. All session operations (`new`, `attach`, `kill`, `list`, `capture-pane`) go through here. No direct shell-outs from the TUI layer.
- **`internal/claude`** — Claude session detection. Reads `~/.claude/projects/<encoded-path>/` for transcripts, derives "needs input" state from pane content patterns.
- **`internal/project`** — discovers projects under `~/Projects` (configurable). A "project" is any directory with `CLAUDE.md` or a `.git`.
- **`internal/notes`** — notes operations on each project's `docs/` tree. Markdown rendering via Glamour, note creation with templated frontmatter, ripgrep-backed search. Obsidian is treated as an optional desktop integration here (just an `obsidian://` URI builder when the app is detected).
- **`internal/claudeconfig`** — reads and writes Claude Code's own configuration (`~/.claude/settings.json`, `~/.claude/CLAUDE.md`, `~/.claude/commands/`, `~/.claude/skills/`). Backs up the file to `~/.claude/backups/` before every mutation. Preserves unknown JSON fields across round-trips. Powers the "Claude" screen in the TUI.
- **`internal/claudeauth`** — reads `claude auth status` JSON for login/plan info, cached 5min. Used by App.New() to auto-detect the user's subscription tier when config.subscription.tier is empty or "api".
- **`internal/claudeusage`** — walks `~/.claude/projects/*/*.jsonl` to aggregate per-window token usage and user-prompt counts. Drives the dashboard's usage panel + the 5-hour quota bar.
- **`internal/config`** — `~/.config/ccmux/config.toml` for user preferences (projects dir, theme, keybindings).
- **`internal/daemon`** — IPC client/server. TUI talks to ccmuxd over a Unix socket at `~/.local/state/ccmux/ccmuxd.sock`.

# Build & Run
```bash
make build         # builds bin/ccmux and bin/ccmuxd
make install       # installs to ~/.local/bin/
make run           # go run ./cmd/ccmux
make test
make lint          # gofmt + go vet + staticcheck if installed
```

# Conventions
- **Standard layout:** code under `cmd/` and `internal/`, no top-level `pkg/` directory unless a stable external API is committed.
- **Errors:** wrap with `fmt.Errorf("...: %w", err)`. No naked returns of errors from anywhere user input touches.
- **Logging:** use `charmbracelet/log`. The daemon logs to `~/.local/state/ccmux/ccmuxd.log`. The TUI logs to the same file (never stdout — it corrupts the alt-screen).
- **TUI:** keep Bubble Tea models small and composable. Each screen is its own model implementing `tea.Model`. The root model is a router.
- **Styling:** all colors and shapes live in `internal/tui/styles`. Never hard-code a color in a screen file. Theme is loaded once at startup.
- **Subprocess discipline:** every `exec.Command` call must take a `context.Context` so the TUI can cancel hung shells.

# Testing
- Unit tests for `internal/tmux` and `internal/project` use table-driven tests against fake `tmux` outputs.
- TUI screens get golden-file tests via `teatest` (Charm's snapshot tester).
- Integration tests are tagged `//go:build integration` and run against a real tmux server in CI.
- **Fuzz targets.** Native Go fuzzers cover the parsers + heuristic surfaces (`FuzzParseID`, `FuzzReadAgent`, `FuzzClassify`, `FuzzOSC52RoundTrip`, `FuzzParsePmsetBatt`, `FuzzSessionNameForPath`, `FuzzRenderSessionLine_DegenerateInputs`). CI runs each for **10s** as a smoke pass; that is just enough to catch a freshly-broken invariant on the PR that introduced it. Run deeper sweeps locally when you touch one of those surfaces:
  - `make fuzz` — 5min/target (≈35min total), the default for "I want real coverage"
  - `make fuzz-quick` — 10s/target (≈70s total), mirrors CI exactly
  - `make fuzz FUZZTIME=1h` — overnight sweep before a release
  Failing seeds auto-archive under `<pkg>/testdata/fuzz/<FuzzName>/<sha>` (Go's native convention) — commit them so `go test ./...` picks them up as regression seeds. Adding a new `FuzzXxx` target? Append it to `FUZZ_TARGETS` in the root `Makefile` — it's the single source of truth used by CI and locally.
- **Always run `go test ./...` before every commit.** A clean suite is a precondition for `git commit` in this repo, not a follow-up. If any test fails, fix it (or mark + explain the skip) before the commit. Cross-compile sanity (`GOOS=windows`, `GOOS=linux`) is part of "tests pass" for changes that touch OS-specific code.
- **Every new feature ships with tests.** No PR should add user-visible behavior without at least one test that fails before the change and passes after. The bar is "exercised in code" — unit tests for pure helpers, fake-driven tests for protocol changes, table-driven keymap tests for new TUI bindings, live-driven integration tests for things that need a real tmux/daemon. A feature without a test is unfinished work, not a candidate for review.
- **TODOs (research + plan in [`docs/01_Specs/03_Testing_And_CI.md`](docs/01_Specs/03_Testing_And_CI.md)):**
  - **Stress testing.** A `cmd/ccmux-stress/` harness exercising the daemon under realistic load (20+ sessions, multi-host fan-out, notification storms, 24h long-haul) with pprof + FD-leak detection.
  - **Terminal crawling.** A `cmd/ccmux-crawl/` monkey-tester (built on `teatest`) plus `rapid` property tests for the heuristic surfaces that the native `go test -fuzz` corpus doesn't already cover (pane classifier deeper invariants, render under degenerate dimensions).
  - **Nightly long-budget fuzz cron.** A Mac mini cron running `make fuzz FUZZTIME=1h` and committing failing seeds back as PRs. CI's 10s/target PR pass is intentionally a smoke check, not a deep search.

# Feature surface policy
- **Every feature must be reachable from both the TUI and the CLI.** Pick a key/screen in the TUI *and* a `ccmux <subcommand>` (or flag on an existing one) — never one without the other. Reasoning: the TUI is the daily driver, the CLI is for muscle memory + scripting; shipping a feature in only one creates a discoverability cliff and breaks the "TUI-first, CLI when you want it" promise on the front page.
- Concretely, a PR that adds a new behaviour should land:
  - The implementation in `internal/...`.
  - A TUI affordance: keybinding, screen, form row, or detail-pane action wired through `internal/tui`.
  - A CLI affordance: a Cobra subcommand in `cmd/ccmux/cmd/` (new file, or a flag added to an existing one).
  - Tests that exercise the implementation directly *and* at least one of the two surfaces.
  - Updates to the README + the website's docs/MDX where the feature is user-visible.
- Acceptable exceptions: telemetry/internals that have no user-facing semantics (e.g. the bell-suppress predicate); pure refactors; private daemon endpoints that ship alongside a TUI/CLI consumer in the same PR.

# Docs Map
- `docs/01_Specs/00_Vision.md` — the why and the user story
- `docs/01_Specs/01_Feature_Catalog.md` — every feature, scoped to a release phase
- `docs/02_Architecture/00_System_Design.md` — components, data flow, daemon protocol
- `docs/02_Architecture/01_Notes_System.md` — markdown-on-disk model, TUI Notes tab, optional Obsidian/web-viewer paths
- `docs/02_Architecture/02_iOS_Mobile_Setup.md` — Moshi + moshi-hook (primary); Blink/Termius (fallback)
- `docs/02_Architecture/03_Tailscale_Networking.md` — how Tailscale sits underneath the whole stack; what ccmux uses it for and what it leaves alone
- `docs/04_Guides/` — user-facing setup guides published to the README

# Owner
Alexander "Sasha" Kuznetsov — me@skz.dev
