<div align="center">

# ccmux

**One TUI to start, resume, and supervise Claude Code sessions — from your Mac, your phone, or anywhere on your tailnet.**

[![Go Version](https://img.shields.io/badge/go-1.22+-00ADD8.svg)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Status: alpha](https://img.shields.io/badge/status-alpha-orange.svg)](#status)
[![Made with Charm](https://img.shields.io/badge/made_with-Charm-FF66CC.svg)](https://charm.sh/)

`tmux` + `Claude Code` + `Mosh` + `Tailscale`, finally legible.

<!-- DEMO_GIF -->
*(animated demo GIF rendered here — recorded with [VHS](https://github.com/charmbracelet/vhs) once the TUI is interactive)*

</div>

---

## What it is

ccmux is a terminal UI for managing long-running [Claude Code](https://claude.ai/code) sessions. It treats tmux as the durability layer, Mosh + Tailscale as the connectivity layer, and Claude Code as the workload — and it wraps all three in a TUI that's nice enough to use from your phone.

A typical workflow:

> You start a Claude session for a project from anywhere. The session keeps running — through disconnects, lid closes, even reboots if you opt into auto-restore. When Claude needs your input, your phone gets a push notification (terminal-bell-to-iOS-push via Blink Shell). You tap, you're attached, you answer Claude, you detach. The session keeps going. Tomorrow on a different device, it's right where you left it.

**Local mode** runs on your laptop and prevents sleep while sessions are active (lid-closed-on-power supported). **Server mode** runs on a Mac Mini or remote machine; your laptop and phone connect over Tailscale. **Mixed mode** is the default — your dashboard lists local sessions and remote sessions side-by-side, color-coded by origin.

## Why

There's a pattern emerging among heavy Claude Code users: keep one always-on Mac (often a Mac Mini) running tmux sessions for every project, and Mosh + Tailscale in from whatever device you're on. It works really well — but the user experience is "remember the tmux session names and ssh in." There's no UI showing what's running, what's idle, what needs you.

ccmux is the UI that makes this workflow legible. It's not a replacement for tmux, Claude Code, or Tailscale — it's the front door.

## Features

### Session management
- Live dashboard of every Claude session across every project, with status (active / idle / **waiting for your input**)
- One-key attach, kill, rename, snapshot
- Per-session "keep awake" pin (the daemon holds a `caffeinate` lock so your laptop doesn't sleep)
- **Three sleep-prevention modes** — Safe (AC only, default), Dangerous (battery too, opt-in with low-battery auto-release and a red banner), Very Dangerous (system-wide lid-close override, sudo-gated, user-invoked)
- Live preview pane: tail any session without attaching
- Session graveyard — review transcripts of recently killed sessions

### Local & server modes
- **Local mode** — manages tmux sessions on this machine. Prevents sleep while sessions are active.
- **Server mode** — daemon binds an HTTP API to your Tailscale interface so other devices can list/attach to this machine's sessions.
- **Mixed** — dashboard shows local + remote sessions, color-coded. Same UX for both. Attach to a remote session execs `mosh host -- tmux attach`.

### Claude Code config management
- A dedicated "Claude" screen surfaces and edits everything Claude Code reads from `~/.claude/`
- Default model picker (Opus 4.7 / Sonnet 4.6 / Haiku 4.5 / opusplan / custom) — global or per-project
- Browse and create slash-command aliases under `~/.claude/commands/`
- Manage MCP servers, hooks, permission allowlists
- View & edit global and per-project `CLAUDE.md` from the TUI
- "Effective config" merged view — global + project + local + env — for debugging "why is Claude doing X"

### Project bootstrapping
- `ccmux new <name>` — scaffolds a project, writes `CLAUDE.md`, creates the `docs/` notes structure, makes the GitHub repo, kicks off the first Claude session. (Successor to the `mkproj` zsh function.)
- `ccmux upgrade` — retrofits the same structure into an existing directory.
- Templates: blank, Python (uv), Go, Next.js, Rust.

### Notes, terminal-native
- Per-project Notes tab — tree view of `docs/` with inline markdown rendered by [Glamour](https://github.com/charmbracelet/glamour).
- Quick-actions: new Agent Log (today's, auto-templated), new Spec, new ADR.
- Auto-appends a session-start line to today's Agent Log — your daily journal builds itself.
- Ripgrep-backed search.
- **No required sync service.** Files live on disk; the disk is the source of truth.
- *(Optional)* Tailnet web viewer — Safari on any device renders the same notes.
- *(Optional)* `obsidian://` URI handoff if you have Obsidian on your Mac.

### Mobile workflow (iOS / Android, via [Moshi](https://getmoshi.app/))
- **Categorized push notifications** — Moshi's `moshi-hook` daemon plugs into Claude Code's hooks system on the host, so notifications carry rich categories (`approval_required`, `task_complete`, `session_started`) instead of an opaque terminal bell.
- **One-command setup** — `ccmux moshi-setup` installs `moshi-hook`, runs the pairing flow, and writes the Claude Code hooks config for you.
- **Auto-detection** — when `moshi-hook` is detected, ccmuxd suppresses its own bell-trigger so you don't get duplicate notifications.
- **Persistent outer tmux session** — Moshi's connection command `tmux new-session -A -s ccmux ccmux` puts you straight back in the TUI every time you open the app.
- **Fallback for non-Moshi clients** — Blink Shell, Termius, anything that turns BEL into a notification still works; ccmuxd injects `\a` on needs-input transitions in that mode.

### Setup wizard
- `ccmux setup` checks tmux / mosh / tailscale / claude / moshi-hook, offers `brew install` for missing pieces
- `ccmux moshi-setup` runs the full Moshi pairing + hooks install flow
- Generates SSH keys if missing
- Tests the Tailscale connection
- `ccmux doctor` is the non-interactive version for scripting (now reports moshi-hook status too)

### Quality of life
- Catppuccin Mocha by default. Theme switcher: Dracula, Nord, Gruvbox, Tokyo Night.
- `?` opens contextual key help.
- Vim-style nav (`h/j/k/l`) and arrow keys both work.
- Auto-switches to a narrow-terminal layout under 80 cols (phone mode).
- `Ctrl-P` global command palette.
- Mouse support on by default.

## Install

**From source (current — Homebrew tap coming with v0.1 release):**

```bash
git clone https://github.com/skzv/ccmux.git
cd ccmux
make setup
```

That's the whole flow. `make setup` builds, installs `ccmux` + `ccmuxd` into `~/.local/bin/`, then drops you into the interactive setup wizard which checks dependencies (`tmux`, `mosh`, `tailscale`, `claude`, `ripgrep`, optionally `moshi-hook`), offers a single `brew install` for whatever's missing, verifies Tailscale + Moshi pairing, generates an SSH key if needed, and writes `~/.config/ccmux/config.toml`. The wizard is **idempotent** — re-run it any time; it skips what's already done.

Requirements:
- Go 1.22+ (build only)
- macOS or Linux
- `~/.local/bin` on your PATH

Then:

```bash
ccmux          # launch the TUI
ccmux setup    # re-run the wizard whenever
ccmux doctor   # non-interactive health check
```

## Uninstall

A clean undo for everything ccmux installed:

```bash
ccmux uninstall            # interactive: shows what it'll do, asks y/N
ccmux uninstall --yes      # skip the prompt
ccmux uninstall --dry-run  # preview only
```

What gets removed:
- Running `ccmuxd` (SIGTERM)
- `~/.local/bin/ccmux` and `~/.local/bin/ccmuxd`
- `~/.local/state/ccmux/` (socket, logs, pid)
- `~/.local/share/ccmux/` (snapshots, daemon db)
- `~/.config/ccmux/` (unless `--keep-config`)
- The ccmux-styled tmux status bar on every `c-*` session (unless `--keep-chrome`)

What is **never** touched:
- Your project directories under `~/Projects/`
- Notes under `<project>/docs/`
- `~/.claude/` (Claude Code state + moshi-hook entries)
- Your `~/.zshrc` shims for `cc` / `mkproj` / `upgrade-proj` (remove by hand)

To also remove `moshi-hook` (which is its own product):

```bash
brew services stop moshi-hook
brew uninstall moshi-hook
brew untap rjyo/moshi
```

## Quick start

```bash
# 1. First-time setup (installs missing deps, walks through iOS config)
ccmux setup

# 2. Start the background daemon (or skip — TUI works without it, just fewer features)
ccmux daemon start

# 3. Launch the TUI
ccmux

# Or skip the TUI and use the CLI directly:
ccmux new my-project              # scaffold + start Claude session
ccmux attach my-project           # attach to existing session
ccmux list                        # what's running
ccmux host add mini mini.tail-xxxxx.ts.net    # add a remote ccmuxd host
```

## How it works

```
        LAPTOP (client + local)                MINI (local + server)
   ┌─────────────────────────────┐         ┌──────────────────────────────┐
   │  ccmux TUI                  │ ──http──►  ccmuxd HTTP                 │
   │   ├─ local sessions ◄─unix──┤ tailnet │   ├─ sessions (mini-foo)     │
   │   │   • laptop-bar          │ ────────►   │   • mini-foo (active)    │
   │   │   • laptop-baz          │         │   │   • mini-cas (waiting 🔔)│
   │   └─ remote: mini           │         │   └─ caffeinate -s while active
   │      • mini-foo             │         │                              │
   │      • mini-cas  🔔         │         │ ccmuxd Unix socket           │
   │                             │         │  (for local TUI on mini)     │
   └─────────────────────────────┘         └──────────────────────────────┘
                                                       ▲
                                                       │ Mosh
                                                       │ (when phone connects)
                                              ┌────────┴──────────┐
                                              │  iPhone (Blink)   │
                                              │  → ccmux on mini  │
                                              └───────────────────┘
```

Full architecture: [`docs/02_Architecture/00_System_Design.md`](docs/02_Architecture/00_System_Design.md).

## Roadmap

ccmux ships ambitious from day one. Detailed phasing in [`ROADMAP.md`](ROADMAP.md), but the headline:

- **v0.1 (current target)** — TUI, sessions, notes, setup wizard, daemon, local + server + mixed modes, terminal-bell notifications, Homebrew tap.
- **v0.2** — Snapshots, themes, command palette, tailnet web viewer for notes, cost tracking from Claude transcripts.
- **v0.3** — Multi-select session ops, activity heatmap, daily-journal rollups, mDNS host discovery.
- **Long term** — Native SwiftUI iOS app that talks directly to ccmuxd over Tailscale. Native APNs push, touch-optimized session list, conversation view built for thumb input.

## Design principles

1. **Terminal-first, not terminal-only.** Must work in a Mosh pane on an iPhone.
2. **One source of truth: tmux.** ccmux is a view; tmux is the database.
3. **Plain markdown on disk** beats any vendor lock-in. No required cloud, no required sync subscription.
4. **Notifications by terminal bell.** Reuses what every iOS terminal client already supports.
5. **Setup is a flow, not a README.** First-run wizard installs what it can, instructs for what it can't.
6. **No telemetry.** Ever. Explicit non-goal.

## Built with

Standing on the shoulders of [Charm](https://charm.sh/):

- [Bubble Tea](https://github.com/charmbracelet/bubbletea) — the TUI framework
- [Lipgloss](https://github.com/charmbracelet/lipgloss) — styling
- [Bubbles](https://github.com/charmbracelet/bubbles) — list, viewport, textinput, spinner, help
- [Huh](https://github.com/charmbracelet/huh) — forms for the setup wizard
- [Glamour](https://github.com/charmbracelet/glamour) — markdown rendering
- [Log](https://github.com/charmbracelet/log) — pretty logging

Plus [Cobra](https://cobra.dev/) for the CLI surface and [SQLite](https://gitlab.com/cznic/sqlite) (pure-Go, no cgo) for daemon state.

## Status

**Alpha.** v0.1 is in active development. Expect rough edges, but core flows (attach, new, kill, notes) are designed to be solid before announcement.

If you're trying it: please open issues. If you're considering using it for real work: wait for the v0.1 release.

## Contributing

Issues and PRs welcome. See `CONTRIBUTING.md` (TBD) for the gist. The short version:

- Code style: `gofmt`, `go vet`, `staticcheck`. Long-tailed CI on those.
- Bug reports: include `ccmux doctor` output.
- Feature requests: read `docs/01_Specs/01_Feature_Catalog.md` first — it likely has thinking on what you want.

## License

MIT — see [LICENSE](LICENSE).

## Acknowledgements

The workflow this tool wraps was developed in public by the AI-first software engineering community over 2024–2026. Particular thanks to:

- Charm for making the best TUI stack in any language
- The Tailscale and Mosh teams for the connectivity layers
- Anthropic for shipping Claude Code
- The Blink Shell maintainers for making mobile terminals actually good
