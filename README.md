<div align="center">

# ccmux

**A terminal UI for [Claude Code](https://claude.ai/code) session management — run long-lived AI coding sessions on your Mac (or a Mac Mini), supervise them from anywhere on your [tailnet](https://tailscale.com/), and get push notifications on your phone when Claude needs you.**

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

## Tutorials

Five hand-on walkthroughs. Each is self-contained — pick whichever maps to what you're trying to do.

### 1. Your first project, end-to-end (≈3 min)

The core loop: scaffold → talk to Claude → take notes → commit. Run it once, you'll have the muscle memory.

```bash
ccmux new auth-redesign -d "rebuild the login flow with passkeys"
```

That single command:
1. Creates `~/Projects/auth-redesign/` with `docs/01_Specs/`, `docs/02_Architecture/`, `docs/03_Agent_Logs/` — just the documentation vault. The source-code layout (cmd+internal? src? a Python package dir?) is chosen by Claude during `/init` based on the language you pick.
2. Writes a starter `README.md` + `.gitignore`, runs `git init`, makes the first commit.
3. Opens a tmux session named `c-auth-redesign`, starts Claude inside it.
4. After Claude boots, types your description as the first prompt — Claude reads it, asks 2-3 clarifying questions, and writes `docs/01_Specs/00_Initial_Concept.md` from your answers.

Everything stays local. When you're ready to push to GitHub (anytime — `gh` is optional but nice to have for this):

```bash
cd ~/Projects/auth-redesign
gh repo create --private --source=. --remote=origin --push
```

**To check on it without joining the conversation:**

```bash
ccmux list
# NAME              HOST   STATE        PATH
# c-auth-redesign   local  needs_input  /Users/you/Projects/auth-redesign
```

**To attach:**

```bash
ccmux attach auth-redesign
# (you're now inside the Claude session — Ctrl-b then d to detach)
```

The session keeps running after you detach. Your laptop's lid can close (on AC power) and it'll still be there tomorrow.

### 2. Juggling multiple Claude sessions (≈2 min)

You have three projects you're moving in parallel. Open the TUI:

```bash
ccmux
```

The Dashboard shows all sessions, color-coded by state:

- **active** — Claude is generating output right now.
- **idle** — Claude finished, no prompt visible. Nothing waiting on you.
- **needs_input** — Claude is showing its input box and the pane has been quiet for ≥ 3 seconds. **This is the one to watch.**

When any session transitions to `needs_input`, ccmuxd injects a terminal BEL. Any iOS terminal client that does BEL→notification (Blink, Termius) will raise a push. If you have [Moshi](https://getmoshi.app/) installed via `ccmux moshi-setup`, you get categorized notifications (approval_required vs task_complete) instead.

Useful keys on the Sessions screen:
- `Enter` — attach (re-execs into `tmux attach`)
- `k` — kill the highlighted session
- `r` — rename
- `space` — pin "keep awake" (the daemon holds a `caffeinate -s` while pinned)
- `?` — full keymap

### 3. Working from your phone (≈5 min, one-time setup)

Goal: your iPhone gets push notifications when Claude needs you, you tap, you're attached.

```bash
ccmux moshi-setup
```

The wizard installs `moshi-hook` via Homebrew, walks you through Moshi's pairing flow, and writes the Claude Code hook entries into `~/.claude/settings.json` so every Claude session on this host fires structured push notifications.

On your phone:
1. Install [Moshi](https://getmoshi.app/) from the App Store.
2. Tap "Add Host", scan the pairing QR code (or paste the token).
3. Moshi opens a persistent tmux session named `ccmux` and drops you into the TUI.

Now whenever Claude pauses for input on the Mac, your phone vibrates. Tap, the TUI's already on the right session, attach with Enter, answer, detach with Ctrl-b then L (returns you to the outer ccmux session, not the iOS app). Your laptop on a desk somewhere doesn't have to be open.

For a non-Moshi setup (plain Blink Shell), the BEL byte still produces a generic notification — you lose the category, that's it.

### 4. Customize the scaffold (≈2 min)

The defaults work, but ccmux is configurable end-to-end via `~/.config/ccmux/config.toml`. The knobs that matter:

```toml
[projects]
# Where ccmux looks for projects. Default: ~/Projects.
root = "/Users/you/code"

[scaffold]
# Directory layout `ccmux new` creates. Default below — just the docs/ vault,
# because the source-code shape is language-specific and Claude's `/init`
# handles it better. Want to enforce src/+tests/? Set them here.
dirs = ["docs/01_Specs", "docs/02_Architecture", "docs/03_Agent_Logs"]

# What `ccmux new` sends to Claude as the first message. {{name}} and
# {{description}} are substituted. Empty falls back to the built-in default,
# which asks Claude to run /init, scaffold CLAUDE.md, and create a GitHub repo.
initial_prompt = """
We're starting "{{name}}". {{description}}

Please: (1) run /init to scaffold CLAUDE.md. (2) Ask 2-3 questions about
stack and goals, then write docs/01_Specs/00_Initial_Concept.md.
"""

# Skip the auto-commit on `ccmux new`. Default true.
create_initial_commit = true

[daemon]
poll_interval_seconds = 2         # how often ccmuxd scrapes tmux
idle_seconds_for_needs_input = 3  # pane idle this long → needs_input
listen_tailnet = false            # set true on your Mac Mini
tailnet_port = 7474
```

After editing, the change is picked up on the next ccmux/ccmuxd start. Run `ccmux update` to reload the daemon with the new config.

### 5. Multi-machine: laptop + always-on Mac Mini (≈5 min)

The intended workflow for heavy users. Sessions live on the Mini; your laptop and phone are clients.

**On the Mini:**

```bash
# Edit ~/.config/ccmux/config.toml:
#   [daemon]
#   listen_tailnet = true
#   tailnet_port = 7474

ccmux daemon install   # registers ccmuxd under launchd so it survives reboot
```

**On the laptop:**

```bash
ccmux host add mini mini.tail-xxxxx.ts.net
ccmux                  # dashboard now shows local sessions AND mini sessions
```

Attach to a remote session and ccmux execs `mosh mini -- tmux attach -t <name>` — Mosh tolerates roaming and stalls, so you can close your laptop, walk to a coffee shop, open it, and your session resumes instantly.

### 6. Maintenance (≈1 min)

```bash
ccmux doctor          # non-interactive health check (tmux/mosh/tailscale/claude reachable?)
ccmux update          # git pull + rebuild + reinstall + restart daemon
ccmux uninstall       # clean removal — never touches your projects or ~/.claude
```

`ccmux update` auto-detects your git checkout (defaults to `~/Projects/ccmux`). Flags: `--dry-run` to preview, `--skip-pull` to just rebuild, `--no-restart` to leave ccmuxd untouched.

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
