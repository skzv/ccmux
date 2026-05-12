<div align="center">

# ccmux

**One TUI for every Claude Code session — on your Mac, on your phone, anywhere.**

[![Go Version](https://img.shields.io/badge/go-1.22+-00ADD8.svg)](https://go.dev/)
[![License: FSL-1.1-MIT](https://img.shields.io/badge/license-FSL--1.1--MIT-blue.svg)](LICENSE)
[![Status: alpha](https://img.shields.io/badge/status-alpha-orange.svg)](#status)
[![Made with Charm](https://img.shields.io/badge/made_with-Charm-FF66CC.svg)](https://charm.sh/)

<!-- DEMO_GIF -->

</div>

---

## Why

Three things, mostly:

🔁 **Seamlessly switch devices.** Start a Claude Code session on your laptop, get a push on your iPhone when it needs you, attach from the phone, answer, detach. The session keeps going. Pick it up on your laptop in the morning, exactly where you left it.

🎛️ **One dashboard for every Claude session.** Live view of every running session across every project — *active*, *idle*, **waiting for your input** — color-coded, one key to attach. No more remembering tmux session names.

☕ **Your laptop won't sleep while Claude is working.** A small background daemon holds a `caffeinate` lock while sessions are active and releases it when they go quiet. Close the lid; Claude keeps thinking.

Built on `tmux` (durability), `Mosh` + `Tailscale` (mobile-friendly connectivity), and [Claude Code](https://claude.ai/code) (the workload). ccmux is the TUI that ties them together.

> **TUI-first, CLI when you want it.** Everything in this README — new projects, attaching sessions, switching hosts, editing config, running the tour — works inside the TUI with discoverable keys and a `?` help overlay. No commands to memorize. The CLI subcommands (`ccmux new`, `ccmux list`, `ccmux update`, …) are there for scripts, muscle memory, and pipelines, but they're optional.
>
> **No `ccmux host add` needed.** ccmux scans your Tailscale network on every refresh, probes each peer for a `ccmuxd /v1/health`, and adds the responders to your dashboard automatically. Install ccmux on a new device, start its daemon (`ccmux daemon install`, `listen_tailnet = true`), and it shows up on every other device on the tailnet within seconds. The `ccmux host add` command still exists for hosts outside Tailscale or for pinning a specific port — pure convenience.

## 🚀 60-second start

ccmux looks for projects under `~/Projects` by default (configurable via `projects.root` in `~/.config/ccmux/config.toml`, or a one-shot path like `ccmux ~/code`). Cloning ccmux itself into that directory keeps things tidy and means `ccmux update` finds the checkout without extra flags:

```bash
mkdir -p ~/Projects && cd ~/Projects
git clone https://github.com/skzv/ccmux.git
cd ccmux && make bootstrap
```

🛠️ `make bootstrap` is the friendliest path on a fresh machine: it checks the **build chain** (Go / git / make / Homebrew on macOS) and offers to install whatever's missing, then chains into `make setup`. ✨ `make setup` then compiles ccmux, installs to `~/.local/bin/`, and runs the interactive wizard that checks `tmux` / `mosh` / `tailscale` / `claude` / `gh` and offers to brew-install whatever's missing. Both are **idempotent** — re-run any time.

> 🤖 **Built with ccmux.** ccmux was developed using ccmux. Almost every commit in this repo was produced by a Claude Code session managed through the very TUI you're about to install — sessions kept alive across laptop lid-closes via the daemon, attached from iOS over Moshi when away from the desk, supervised from the dashboard. It's the kind of tool you only really validate by living inside it; that's what we did.

Then:

```bash
ccmux                # launch the TUI (first-run tour included)
ccmux ~/code         # one-shot: scope this session to ~/code instead of ~/Projects
ccmux new my-app     # scaffold a project + start its Claude session
ccmux list           # what's running, everywhere
ccmux update         # pull latest, rebuild, reload daemon
```

## 👀 What it looks like

```
┌────────────────────────────────────────────────────────────────┐
│  ccmux                          local ✓     mini ✓     5h: 47% │
├────────────────────────────────────────────────────────────────┤
│  NAME              STATE          PATH                          │
│  c-auth-redesign   ● needs_input  ~/Projects/auth-redesign     │
│  c-checkout-bug    ● active       ~/Projects/checkout-bug      │
│  c-ml-experiments  ○ idle         mini:~/Projects/ml-exp       │
│  c-blog            ○ idle         ~/Projects/my-plain-blog     │
└────────────────────────────────────────────────────────────────┘
```

⌨️ `1`-`7` jump between screens (Dashboard, Sessions, Projects, Notes, Claude, Settings, Network), `Enter` attaches, `?` opens contextual help, `T` re-runs the first-run tour.

## 📱 Mobile

```bash
ccmux moshi-setup
```

Installs [moshi-hook](https://getmoshi.app/) on the Mac, runs **Easy Pair** (a QR code appears in your terminal — scan it with the Moshi iOS app, done), and wires the Claude Code hooks that turn `needs_input` events into **categorized** push notifications (approval_required vs task_complete). Tap the notification, the Moshi app shows your live tmux session list, pick one, attach, answer, detach. No tokens to copy.

Plain BEL fallback works in any iOS terminal client (Blink Shell, Termius) — you lose the categories, that's it. For headless / scripted setups: `ccmux moshi-setup --token <token>` bypasses the QR flow.

## 🛰️ Remote (always-on Mac Mini, auto-discovered)

```bash
# On the Mini:
ccmux daemon install                       # ccmuxd survives reboot
# edit ~/.config/ccmux/config.toml: listen_tailnet = true

# On the laptop — nothing to do:
ccmux                                      # dashboard already lists the Mini
```

Every refresh, ccmux runs `tailscale status --json`, probes each online non-mobile peer for a `ccmuxd /v1/health`, and merges the responders into the host list. New device on the tailnet running ccmux? It just appears.

The Devices panel on the Dashboard shows every device on your tailnet:

- 🟢 **peers running ccmuxd** — with their reported version + an "update available" tag whenever they lag this build
- ⚪ **peers NOT running ccmuxd** (Macs/Linux boxes you haven't installed on yet) — with a one-line "ccmux not installed" hint so you remember to bring them online with `make bootstrap`
- 📱 **phones / iPads** — with a "connect via Moshi app" hint, since the iOS Moshi app is their picker (and they don't run ccmux directly)

Attaching to an auto-discovered peer execs `ssh -t <host> -- tmux attach -t <name>` (with a cross-platform PATH prepend so Homebrew/Snap/Linuxbrew tmux is found). If you've pinned a host with `ccmux host add --mosh <name> …`, ccmux uses `mosh` instead — which tolerates roaming and stalls (close the lid, walk somewhere, open it back up, session is still attached). Your phone gets pushes from the Mini either way.

> Manually pinning a host with `ccmux host add` still works — useful for non-Tailscale hosts, or to force a specific port. Discovered hosts and pinned hosts coexist on the dashboard without duplicates.

---

## ✨ Features

### 🎛️ Session management
- Live dashboard of every Claude session across every project, with state (active / idle / **needs_input**)
- One-key attach, kill, rename — applies a styled tmux status bar so you always know where you are
- Per-session "keep awake" pin — the daemon holds a sleep-prevention lock while any pinned or active session is alive, and releases it when they all go idle
- **Three sleep-prevention modes** — `safe` (AC only — the macOS default; auto on Linux), `dangerous` (works on battery too, opt-in, auto-releases below a configurable low-battery threshold), `very_dangerous` (system-wide override that survives lid-close; sudo-gated and reverted on daemon exit)

### 🏗️ Project bootstrapping
- `ccmux new <name>` — scaffolds a project, creates the `docs/` notes vault, runs `git init`, opens a Claude session with your description as the first prompt
- `ccmux upgrade` — retrofits the same structure into an existing directory
- Local-only by default — push to GitHub when you're ready with `gh repo create`

### 🤖 Claude Code config management
- Dedicated "Claude" screen for everything in `~/.claude/`
- Model picker (Opus 4.7 / Sonnet 4.6 / Haiku 4.5 / opusplan / custom) — global or per-project
- Browse + create slash-command aliases, manage MCP servers, hooks, permission allowlists
- View & edit global and per-project `CLAUDE.md` from the TUI

### 📝 Notes, terminal-native
- Per-project Notes tab — tree view of `docs/` with inline markdown rendered by [Glamour](https://github.com/charmbracelet/glamour)
- Quick-actions: new Agent Log (today's, auto-templated), new Spec, new ADR
- Ripgrep-backed search; plain markdown on disk is the source of truth (no required cloud)

### 📲 Mobile workflow (Moshi / iOS / Android)
- **Easy Pair via QR code** — `ccmux moshi-setup` runs `moshi-hook host setup`; scan the QR code with the Moshi app, you're paired. No token paste.
- **Categorized push notifications** via `moshi-hook` plugging into Claude Code's hooks system (approval_required, task_complete, …)
- **In-app session picker** — the Moshi app lists every tmux session on the paired host; no need to memorize names
- **Auto-detection** — ccmuxd suppresses its own BEL trigger when moshi-hook is paired so you don't get duplicate notifications

### 🌐 Local & remote modes
- **Local** — manages tmux sessions on this machine; prevents sleep while sessions are active
- **Server** — daemon binds an HTTP API to your Tailscale interface so other devices can list/attach
- **Mixed** — dashboard shows local + remote sessions, color-coded by origin

### 🩺 Setup, doctor, update
- `ccmux setup` — interactive wizard, checks every dep, offers `brew install` for missing pieces
- `ccmux doctor` — non-interactive health check (great for scripting)
- `ccmux update` — pulls the git checkout, rebuilds, reloads ccmuxd
- `ccmux uninstall` — clean removal, never touches your projects or `~/.claude/`

### 🎨 Quality of life
- Catppuccin Mocha by default; Dracula / Nord / Gruvbox / Tokyo Night planned
- `?` opens contextual key help on every screen
- Vim-style (`h/j/k/l`) and arrow keys both work
- Auto-switches to a narrow-terminal layout under 80 cols (phone mode)
- Mouse support on by default
- **No telemetry. Ever.**

---

## 📚 Tutorials

Six hands-on walkthroughs. Each is self-contained — pick whichever maps to what you're trying to do.

### 1. Your first project, end-to-end (≈3 min)

The core loop: scaffold → talk to Claude → take notes → commit.

```bash
ccmux new auth-redesign -d "rebuild the login flow with passkeys"
```

That single command:
1. Creates `~/Projects/auth-redesign/` with `docs/01_Specs/`, `docs/02_Architecture/`, `docs/03_Agent_Logs/` — just the documentation vault. The source-code layout (cmd+internal? src? a Python package dir?) is chosen by Claude during `/init` based on the language you pick.
2. Writes a starter `README.md` + `.gitignore`, runs `git init`, makes the first commit.
3. Opens a tmux session named `c-auth-redesign`, starts Claude inside it.
4. After Claude boots, types your description as the first prompt — Claude reads it, asks 2-3 clarifying questions, and writes `docs/01_Specs/00_Initial_Concept.md` from your answers.

Everything stays local. When you're ready to push to GitHub:

```bash
cd ~/Projects/auth-redesign
gh repo create --private --source=. --remote=origin --push
```

To check on the session without joining the conversation: `ccmux list`. To attach: `ccmux attach auth-redesign`.

The session keeps running after you detach. Your laptop's lid can close (on AC power) and it'll still be there tomorrow.

### 2. Juggling multiple Claude sessions (≈2 min)

You have three projects moving in parallel. Open the TUI:

```bash
ccmux
```

The Dashboard shows all sessions, color-coded by state:

- **active** — Claude is generating output right now.
- **idle** — Claude finished, no prompt visible.
- **needs_input** — Claude is showing its input box and the pane has been quiet for ≥ 3 seconds. **This is the one to watch.**

When any session transitions to `needs_input`, ccmuxd injects a terminal BEL. Any iOS terminal client that does BEL→notification raises a push.

Useful keys on the Sessions screen:
- `Enter` — attach
- `x` — kill the highlighted session
- `R` — rename
- `k` — pin keep-awake (the daemon holds a `caffeinate -s` while pinned)
- `?` — full keymap

### 3. Working from your phone (≈3 min, one-time setup)

Goal: your iPhone gets push notifications when Claude needs you, you tap, you're attached.

```bash
ccmux moshi-setup
```

That installs `moshi-hook`, then runs `moshi-hook host setup` — **a QR code appears in your terminal**. Open the Moshi iOS app, tap **Add Host → Scan QR**, point your phone at the terminal, done. The wizard also wires Claude Code's hook entries so notifications fire automatically.

After pairing, the Moshi app lists every tmux session on the Mac. Whenever Claude pauses on the host, your phone vibrates with a categorized push (approval_required / task_complete). Tap it, pick the session in the Moshi picker, attach, answer, swipe away.

For a non-Moshi setup, the BEL fallback still produces a generic notification — categories disappear, everything else works.

### 4. Customize the scaffold (≈2 min)

`~/.config/ccmux/config.toml` — the knobs that matter:

```toml
[projects]
root = "~/Projects"                  # where ccmux looks for projects

[scaffold]
# Default below — just the docs vault, because the source-code shape is
# language-specific and /init handles it better. Want to enforce src/+tests/?
# Set them here.
dirs = ["docs/01_Specs", "docs/02_Architecture", "docs/03_Agent_Logs"]

# What `ccmux new` sends to Claude as the first message. {{name}} and
# {{description}} are substituted. Empty falls back to the built-in default.
initial_prompt = """
We're starting "{{name}}". {{description}}
…
"""

create_initial_commit = true         # auto-commit on scaffold

[daemon]
poll_interval_seconds = 2
idle_seconds_for_needs_input = 3
listen_tailnet = false               # set true on your server-mode host
tailnet_port = 7474

[sleep]
mode = "safe"                        # "safe" | "dangerous" | "very_dangerous"
idle_release_minutes = 10
low_battery_cutoff = 20              # dangerous mode auto-downgrades below this
```

> Sleep-mode notes:
> - `safe` — `caffeinate -s` on macOS (Apple's policy keeps it AC-only; safe to leave on). `systemd-inhibit --what=sleep:idle` on Linux.
> - `dangerous` — `caffeinate -d -i -m -s` on macOS, so the lock works on battery too. The daemon polls battery once a minute and downgrades to `safe` when below `low_battery_cutoff` (so a forgotten laptop doesn't run flat).
> - `very_dangerous` — adds `sudo -n pmset -a disablesleep 1` (macOS) / `sudo -n systemctl mask sleep.target …` (Linux) so lid-close no longer puts the system to sleep. Requires passwordless sudo for the specific command (add a line to `/etc/sudoers.d/ccmux` — example below); silently degrades to `dangerous` if sudo asks for a password. Always reverted when ccmuxd exits cleanly.
>
> Example sudoers entry (run `sudo visudo -f /etc/sudoers.d/ccmux`):
> ```
> # macOS
> %admin ALL=(ALL) NOPASSWD: /usr/bin/pmset -a disablesleep *
> # Linux
> %sudo ALL=(ALL) NOPASSWD: /bin/systemctl mask *, /bin/systemctl unmask *
> ```

`projects.root`, `scaffold.dirs`, and `subscription.tier` are also editable inline from the Settings screen — `↑/↓` to move, `Enter` to edit, `e` to open `$EDITOR` for the prose-heavy fields.

After editing, run `ccmux update` to reload the daemon with the new config.

### 5. Multi-machine: laptop + always-on Mac Mini (≈5 min)

The intended workflow for heavy users. Sessions live on the Mini; your laptop and phone are clients. **No manual host configuration** — ccmux auto-discovers every ccmuxd on your tailnet.

**On the Mini:**

```toml
# ~/.config/ccmux/config.toml
[daemon]
listen_tailnet = true
tailnet_port   = 7474
```

```bash
ccmux daemon install   # registers ccmuxd under launchd so it survives reboot
```

**On the laptop:**

```bash
ccmux                  # the Mini already appears on the dashboard, tagged "discovered"
```

The Devices panel shows each peer's ccmuxd version. If the Mini is behind your laptop's build, it gets an "update available" tag — run `ccmux update` on the Mini (or SSH in and do it) to bring them in sync.

Attach to a discovered remote session and ccmux execs `ssh -t mini -- tmux attach -t <name>` (use `ccmux host add --mosh mini …` if you'd rather use Mosh for that pinned host).

### 6. Maintenance (≈1 min)

```bash
ccmux doctor          # one-shot health check
ccmux update          # git pull + rebuild + reinstall + restart daemon
ccmux uninstall       # clean removal
```

`ccmux update` auto-detects your git checkout (defaults to `~/Projects/ccmux`). Flags: `--dry-run`, `--skip-pull`, `--no-restart`.

---

## Install

**From source (Homebrew tap coming with v0.1 release):**

```bash
git clone https://github.com/skzv/ccmux.git
cd ccmux
make setup
```

`make setup` builds, installs `ccmux` + `ccmuxd` into `~/.local/bin/`, then runs the wizard. Idempotent — re-run any time.

Requirements:
- Go 1.22+ (build only)
- macOS or Linux
- `~/.local/bin` on your PATH

```bash
ccmux          # launch the TUI
ccmux setup    # re-run the wizard
ccmux doctor   # health check
```

## Uninstall

```bash
ccmux uninstall            # interactive: shows what it'll do, asks y/N
ccmux uninstall --yes      # skip the prompt
ccmux uninstall --dry-run  # preview only
```

What gets removed:
- Running `ccmuxd` (SIGTERM)
- `~/.local/bin/ccmux` and `~/.local/bin/ccmuxd`
- `~/.local/state/ccmux/` (socket, logs)
- `~/.local/share/ccmux/` (snapshots, daemon db)
- `~/.config/ccmux/` (unless `--keep-config`)
- The ccmux-styled tmux status bar on every `c-*` session (unless `--keep-chrome`)

What is **never** touched:
- Your project directories
- Notes under `<project>/docs/`
- `~/.claude/` (Claude Code state + moshi-hook entries)

To also remove `moshi-hook`: `brew services stop moshi-hook && brew uninstall moshi-hook && brew untap rjyo/moshi`.

## 🏛️ Architecture

```
        LAPTOP (client + local)                MINI (local + server)
   ┌─────────────────────────────┐         ┌──────────────────────────────┐
   │  ccmux TUI                  │ ──http──►  ccmuxd HTTP                 │
   │   ├─ local sessions ◄─unix──┤ tailnet │   ├─ sessions (mini-foo)     │
   │   │   • laptop-bar          │ ────────►   │   • mini-foo (active)    │
   │   │   • laptop-baz          │         │   │   • mini-cas (waiting 🔔)│
   │   └─ remote: mini           │         │   └─ caffeinate -s while active
   │      • mini-foo             │         │                              │
   │                             │         │ ccmuxd Unix socket           │
   │                             │         │  (for local TUI on mini)     │
   └─────────────────────────────┘         └──────────────────────────────┘
                                                       ▲
                                                       │ Mosh
                                                       │ (when phone connects)
                                              ┌────────┴──────────┐
                                              │  iPhone (Moshi /  │
                                              │  Blink / Termius) │
                                              └───────────────────┘
```

Full design: [`docs/02_Architecture/00_System_Design.md`](docs/02_Architecture/00_System_Design.md).

## 🗺️ Roadmap

Phasing in [`ROADMAP.md`](ROADMAP.md). Headline:

- **v0.1** — TUI, sessions, notes, setup wizard, daemon, local + server + mixed modes, terminal-bell notifications, Homebrew tap
- **v0.2** — Snapshots, themes, command palette, tailnet web viewer for notes, cost tracking from Claude transcripts
- **v0.3** — Multi-select session ops, activity heatmap, daily-journal rollups, mDNS host discovery
- **Long term** — Native SwiftUI iOS app talking directly to ccmuxd over Tailscale

## Design principles

1. **Terminal-first, not terminal-only.** Must work in a Mosh pane on an iPhone.
2. **One source of truth: tmux.** ccmux is a view; tmux is the database.
3. **Plain markdown on disk** beats vendor lock-in. No required cloud, no required sync.
4. **Notifications by terminal bell.** Reuses what every iOS terminal client already supports.
5. **Setup is a flow, not a README.** First-run wizard installs what it can, instructs for what it can't.
6. **No telemetry. Ever.**

## Status

**Alpha.** Core flows (attach, new, kill, notes, daemon, Moshi) work end-to-end. Expect rough edges; file issues. Wait for v0.1 if you want it for real work.

## Built with

Standing on the shoulders of [Charm](https://charm.sh/):

- [Bubble Tea](https://github.com/charmbracelet/bubbletea) — the TUI framework
- [Lipgloss](https://github.com/charmbracelet/lipgloss) — styling
- [Bubbles](https://github.com/charmbracelet/bubbles) — list, viewport, textinput, spinner, help
- [Huh](https://github.com/charmbracelet/huh) — forms for the setup wizard
- [Glamour](https://github.com/charmbracelet/glamour) — markdown rendering

Plus [Cobra](https://cobra.dev/) for the CLI surface and [SQLite](https://gitlab.com/cznic/sqlite) for daemon state.

## Contributing

Issues and PRs welcome. See `CONTRIBUTING.md` (TBD). The short version:

- Code style: `gofmt`, `go vet`, `staticcheck`
- Bug reports: include `ccmux doctor` output
- Feature requests: read `docs/01_Specs/01_Feature_Catalog.md` first

## License

[FSL-1.1-MIT](LICENSE) — Functional Source License with MIT future grant.

In plain English: you can use, modify, and redistribute ccmux freely for any purpose **except** offering it (or a substantially-similar feature set) as a competing commercial product or managed service. Two years after each release, that version automatically relicenses to plain MIT.

If you want to commercialize a derivative work sooner, get in touch.

## Acknowledgements

The workflow this tool wraps was developed in public by the AI-first software engineering community over 2024–2026. Particular thanks to:

- Charm for the best TUI stack in any language
- The Tailscale and Mosh teams for the connectivity layers
- Anthropic for shipping Claude Code
- The Blink Shell and Moshi maintainers for making mobile terminals actually good
