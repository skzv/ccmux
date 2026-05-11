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
- **`internal/claudeconfig`** — reads and writes Claude Code's own configuration (`~/.claude/settings.json`, `~/.claude/CLAUDE.md`, `~/.claude/commands/`, `~/.claude/skills/`, per-project `.claude/`). Backs up the file before every mutation. Powers the "Claude" screen in the TUI.
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

# Docs Map
- `docs/01_Specs/00_Vision.md` — the why and the user story
- `docs/01_Specs/01_Feature_Catalog.md` — every feature, scoped to a release phase
- `docs/02_Architecture/00_System_Design.md` — components, data flow, daemon protocol
- `docs/02_Architecture/01_Notes_System.md` — markdown-on-disk model, TUI Notes tab, optional Obsidian/web-viewer paths
- `docs/02_Architecture/02_iOS_Mobile_Setup.md` — Blink/Termius + Mosh + Tailscale
- `docs/04_Guides/` — user-facing setup guides published to the README

# Owner
Alexander "Sasha" Kuznetsov — alexander.kuznetsov96@gmail.com
