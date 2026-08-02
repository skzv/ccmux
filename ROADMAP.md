# Roadmap

ccmux ships ambitious from day one. This is a phased plan — see [`docs/01_Specs/01_Feature_Catalog.md`](docs/01_Specs/01_Feature_Catalog.md) for the full feature catalog with per-feature notes.

## v0.1 — "the ambitious MVP"

The headline release. Everything required to use ccmux as a daily-driver replacement for the existing `cc` / `mkproj` / `upgrade-proj` zsh functions, plus the mobile workflow on top.

### Must-ship
- TUI shell (Bubble Tea + Lipgloss + Bubbles)
  - Dashboard, Sessions, Projects, Notes, Setup, Settings screens
  - Catppuccin Mocha theme
  - Status bar with hostname / tailnet status / session count / clock
  - Contextual help overlay (`?`)
  - Narrow-terminal layout under 80 cols
- Session management
  - List with status (active / idle / needs-input)
  - Attach / kill / rename / new
  - Live preview pane
  - "Keep awake" pin per session
- Project management
  - Discover projects under `~/Projects` (configurable)
  - New project flow (replaces `mkproj`)
  - Upgrade existing project (replaces `upgrade-proj`)
- Notes tab
  - Tree view of `docs/`
  - Glamour-rendered preview
  - Quick-actions: new Agent Log, new Spec, new ADR
  - Auto-log session starts to today's Agent Log
  - `o` opens in Obsidian if installed (macOS host only)
- ccmuxd daemon
  - Polls tmux state every 2s
  - Detects "needs input" via pane content heuristic
  - Rings terminal bell in pane (iOS push trigger)
  - Holds `caffeinate -s` lock while sessions active (sleep prevention)
  - SQLite metrics
  - Unix socket + tailnet HTTP listeners
  - launchd / systemd unit files
- Local / server / mixed modes
  - `ccmux host add/list/remove`
  - Dashboard merges local + remote sessions, color-coded
  - Attach to remote = exec `mosh host -- tmux attach`
- CLI subcommands
  - `attach`, `new`, `upgrade`, `list`, `kill`, `setup`, `doctor`, `daemon start/stop/status`, `host …`
- Setup wizard (Huh forms)
  - Dep check + brew install offer
  - SSH key gen + ANSI QR code for phone
  - Tailscale status check
  - Blink Shell host config copy-paste
- `ccmux doctor` health check
- Homebrew tap (`skzv/tap/ccmux`) via Goreleaser
- README + docs + VHS-recorded demo GIF

### Nice-to-have if time permits in v0.1
- Cost tracking (parsing Claude Code's JSONL transcripts)
- Theme switcher (multiple themes available, picker in settings)
- `Ctrl-P` global command palette

## v0.2

- Tailnet web viewer for notes (HTTP server on `100.x.x.x:7474`, Goldmark-rendered)
- Session snapshots (save tmux scrollback + Claude transcript, restore into fresh window)
- Project templates (Python uv, Go, Next.js, Rust)
- Cost tracking dashboard
- Theme switcher in settings UI
- Per-host config (default user, mosh path, tmux socket)
- Self-update from GitHub releases
- Cross-project notes search
- Per-project `.ccmux.toml` overrides

## v0.3

- Multi-select session operations (broadcast `/cost`, kill many, snapshot many)
- Session graveyard (transcript browser for killed sessions)
- Activity heatmap (Lipgloss block-character GitHub-style)
- Daily journal rollup (aggregates Agent Logs across all projects)
- mDNS auto-discovery of other ccmuxd instances on the tailnet
- Backlinks / wikilink jumping in the Notes tab preview
- Browser-editable notes (textarea + autosave) in the web viewer
- Per-session mute for bell notifications
- ntfy.sh forward as backup notification channel

## Long-term

- **Richer mobile clients on the ccmuxd HTTP API** (see [`docs/02_Architecture/05_HTTP_API.md`](docs/02_Architecture/05_HTTP_API.md))
  - The API already exposes list / attach (WebSocket PTY) / send-keys / preview / events / notes / usage, plus a pairing + push (APNs/FCM) flow
  - The [Moshi](https://getmoshi.app/) app is the mobile path today; the same API is open for any other client to integrate
- End-to-end remote TUI streaming (no need to mosh+attach; render frames over Tailscale)
- Session pairing (tmux built-in pair feature)
- Team mode (multi-user host, ACLs, per-user session lists)
- Web UI for desktop (full-featured, not just notes viewer)

## Non-goals

- Telemetry of any kind. Never.
- Cloud-required auth / accounts. ccmux is a personal tool that runs entirely on machines you own.
- Replacing tmux or Obsidian — ccmux is a client of tmux and (optionally) a peer of Obsidian, never a replacement.
- A plugin/extension system. Charm-stack-style configurability via Go is enough.
