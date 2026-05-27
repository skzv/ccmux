# ccmux — Feature Catalog

Every feature, scoped to a release phase. Phase 1 is "v0.1 — the ambitious MVP." Phases 2–4 are roadmap.

Legend: `[P1]` Phase 1, `[P2]` Phase 2, `[P3]` Phase 3, `[L]` Long-term / native iOS app.

---

## Session Management

| Feature                                                                          | Phase | Notes                                                     |
| -------------------------------------------------------------------------------- | ----- | --------------------------------------------------------- |
| List active Claude/tmux sessions with status (active / idle / waiting-for-input) | P1    | Status derived from pane content + idle time.             |
| Attach to a session by Enter on its row                                          | P1    | Default tmux attach. `d` to suspend ccmux first.          |
| Create a new session by picking a project from a fuzzy-filtered list             | P1    | Bubbles list + textinput.                                 |
| Kill a session with confirmation                                                 | P1    | `x` keybind, `y/n` confirm modal.                         |
| Rename a session inline                                                          | P1    | `r` keybind.                                              |
| Session metadata: project path, start time, last activity, prompt count          | P1    | Shown in detail pane on the right.                        |
| Live preview pane: tails the selected session's pane content                     | P2    | tmux `capture-pane -p`, refresh every 500ms when focused. |
| Session snapshots: save Claude transcript + tmux scrollback to a labeled archive | P3    | Stored under `~/.local/share/ccmux/snapshots/`.           |
| Restore session from snapshot into a fresh tmux window                           | P3    |                                                           |
| Broadcast a slash command (e.g. `/cost`) to multiple selected sessions at once   | P3    | Multi-select with `<space>`, then `b` to broadcast.       |
| Session graveyard: review transcripts of recently killed sessions                | P3    | Read from `~/.claude/projects/` JSONL transcripts.        |
| Pair / share a session with a teammate via tmux built-in pair feature            | L     | Requires a separate user account on the host.             |

## Project Management

| Feature                                                                                                                                       | Phase | Notes                                                                                        |
| --------------------------------------------------------------------------------------------------------------------------------------------- | ----- | -------------------------------------------------------------------------------------------- |
| Discover projects under `~/Projects` (configurable)                                                                                           | P1    | Any dir with `CLAUDE.md` or `.git`.                                                          |
| New project: name + host + agent. Creates the directory, starts the agent session — nothing else (no `CLAUDE.md`, no `docs/`, no `git init`). | P1    | Bootstrapping (`/init`, `openspec`) is the user's job, run inside the session.               |
| Recently opened projects pinned to top of project list                                                                                        | P2    | Last-opened time in config.                                                                  |
| `/` on Projects screen filters the visible list by name (case-insensitive substring)                                                          | P1    | Type-to-filter via bubbles `textinput`. Esc clears, enter attaches to the highlighted match. |
| Quick-jump: `Ctrl-P` opens fuzzy finder over all projects                                                                                     | P1    | Global hotkey form of the above, scoped to "from any screen". Not yet shipped.               |
| Per-project `.ccmux.toml` overrides (auto-attach behavior, default branch)                                                                    | P2    | Optional, merged over user config.                                                           |

## Notes System (plain markdown on disk)

| Feature                                                                           | Phase | Notes                                                                                 |
| --------------------------------------------------------------------------------- | ----- | ------------------------------------------------------------------------------------- |
| Notes tab per project: every `.md` file, grouped by folder                        | P1    | Whole project tree, not just `docs/`; VCS/dependency dirs pruned.                     |
| Inline markdown preview rendered with Glamour                                     | P1    | Right-pane viewport.                                                                  |
| Edit a note in `$EDITOR` (`e`)                                                    | P1    | ccmux browses + searches notes; creating/writing them is the user's (or agent's) job. |
| Per-project notes search (ripgrep-backed)                                         | P1    | `/` opens fuzzy text search across the whole project.                                 |
| Recent notes panel on dashboard: notes edited in last 24h across all projects     | P2    | Reads mtimes.                                                                         |
| Tailnet web viewer: ccmuxd serves rendered markdown on `tailscale_ip:7474`        | P2    | Bound to tailnet only, no auth (tailnet _is_ auth).                                   |
| Cross-project notes search                                                        | P2    |                                                                                       |
| Browser-editable notes (textarea + autosave) on the web viewer                    | P3    |                                                                                       |
| Backlinks display when viewing a note in the TUI                                  | P3    | Parse `[[wikilinks]]`.                                                                |
| Daily journal aggregation: roll up Agent Logs from all projects into a daily view | P3    |                                                                                       |

## Optional Obsidian Integration (desktop only)

| Feature                                                                | Phase | Notes                                                                                                        |
| ---------------------------------------------------------------------- | ----- | ------------------------------------------------------------------------------------------------------------ |
| Detect `/Applications/Obsidian.app` at startup                         | P1    | Hides "open in Obsidian" action if missing.                                                                  |
| "Open in Obsidian" action: builds `obsidian://open?vault=…&file=…` URI | P1    | macOS host only.                                                                                             |
| LiveSync setup guide for users who want Obsidian on iOS                | P2    | `docs/04_Guides/Obsidian_LiveSync_Setup.md`. Self-hosted CouchDB on the Mini. Documented but never required. |

## Local / Server / Mixed Mode

| Feature                                                                                 | Phase | Notes                                                                                                                                                                      |
| --------------------------------------------------------------------------------------- | ----- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Local-only mode: ccmux manages tmux/Claude sessions on the machine it runs on           | P1    | Default behavior. Unix socket IPC to local ccmuxd.                                                                                                                         |
| Sleep prevention Mode 1 (Safe, default): `caffeinate -s` while sessions active          | P1    | macOS only. Linux: `systemd-inhibit`. Released when all sessions idle ≥ N minutes or killed. AC power only.                                                                |
| Sleep prevention Mode 2 (Dangerous, opt-in): keep awake on battery too                  | P1    | `caffeinate -d -i -m -u`. Red persistent banner in TUI. Auto-release below configurable low-battery cutoff (default 20%). Lid-close on battery still sleeps (HW-enforced). |
| Sleep prevention Mode 3 (Very Dangerous, sudo-gated): override lid-close on battery     | P2    | ccmux detects user-set `sudo pmset -b disablesleep 1` state. Does not invoke sudo itself. Shows ⚠⚠ banner. Provides un-do command on toggle off.                           |
| Per-session "always keep awake" pin                                                     | P1    | Toggled with `k` on the session row. Survives daemon restart.                                                                                                              |
| Server mode: ccmuxd binds HTTP API to tailnet IP (`100.x.x.x:7474`)                     | P1    | Same JSON schema as Unix-socket protocol. TLS optional (tailnet _is_ the boundary).                                                                                        |
| `ccmux host add <name> <hostname>` registers a remote ccmuxd                            | P1    | Stored in `~/.config/ccmux/hosts.toml`.                                                                                                                                    |
| `ccmux host list/remove`                                                                | P1    |                                                                                                                                                                            |
| Dashboard shows local + remote sessions, color-coded by host origin                     | P1    | "local" (cyan), "mini" (magenta), etc. Color hash of host name.                                                                                                            |
| Attach to remote session = exec `mosh <host> -- tmux attach -t <session>`               | P1    | Detach returns to local TUI.                                                                                                                                               |
| New project / new session on a remote host (TUI → POST /sessions on remote)             | P2    |                                                                                                                                                                            |
| Health pings remote hosts every 10s; greys out unreachable ones                         | P1    |                                                                                                                                                                            |
| Per-host config: default user, mosh server path, tmux socket                            | P2    |                                                                                                                                                                            |
| ccmuxd autodiscovers other ccmuxd instances on the tailnet via mDNS                     | P3    | Zero-config add for tailnet peers.                                                                                                                                         |
| End-to-end remote TUI streaming (no need to mosh+attach — render frames over Tailscale) | L     | Big project, native iOS app would also use this.                                                                                                                           |

## Claude Code Configuration Management

A "Claude" screen in the TUI that surfaces and edits the settings Claude Code itself reads from `~/.claude/`. Today these live in `settings.json`, `CLAUDE.md`, `commands/`, `skills/`, and the `ANTHROPIC_MODEL` env var; ccmux gives them a UI.

| Feature                                                                     | Phase | Notes                                                                                         |
| --------------------------------------------------------------------------- | ----- | --------------------------------------------------------------------------------------------- |
| Detect & show effective default model (env var > settings.json > built-in)  | P1    | Reads `$ANTHROPIC_MODEL`, `~/.claude/settings.json:model`.                                    |
| Switch default model: Opus 4.7 / Sonnet 4.6 / Haiku 4.5 / opusplan / custom | P1    | Writes to `~/.claude/settings.json`. Re-exec hint for ANTHROPIC_MODEL override.               |
| Per-project model override via `.claude/settings.json`                      | P1    | "Use Sonnet for this project" toggle.                                                         |
| View & edit `~/.claude/CLAUDE.md` (global instructions)                     | P1    | Opens in `$EDITOR`. Shows current content inline.                                             |
| View & edit per-project `CLAUDE.md`                                         | P1    | Linked from Project detail.                                                                   |
| Browse slash-command aliases under `~/.claude/commands/`                    | P1    | Lists `.md` files, shows trigger + content preview.                                           |
| Create new slash-command alias from a template                              | P1    | `n` in the Commands subsection.                                                               |
| Manage Skills under `~/.claude/skills/`                                     | P2    | List, view metadata (name, description, when-to-use).                                         |
| Manage MCP servers in `settings.json:mcpServers`                            | P2    | Add / remove / toggle. Pre-built entries for common servers (Playwright, Figma, Gmail, etc.). |
| Manage tool permission allowlists (`permissions.allow` / `deny`)            | P2    | Inspired by `/permissions` slash command.                                                     |
| Manage hooks (pre/post tool, on Stop, on UserPromptSubmit)                  | P3    | Power-user feature. Diff preview before applying.                                             |
| Show effective config: merged view of global + project + local + env        | P1    | Useful for "why is Claude doing X" debugging.                                                 |
| Backup ~/.claude before every write                                         | P1    | Roll-back action in the TUI.                                                                  |

## Setup & Onboarding

| Feature                                                             | Phase | Notes                                                                   |
| ------------------------------------------------------------------- | ----- | ----------------------------------------------------------------------- |
| `ccmux setup` first-run wizard (Huh forms)                          | P1    |                                                                         |
| Dependency check: tmux, mosh, tailscale, claude, obsidian, ripgrep  | P1    | Reports missing; offers `brew install` for installable ones.            |
| Tailscale status check + reachable hostname display                 | P1    | `tailscale status --json`.                                              |
| SSH key generation (ed25519) if missing                             | P1    |                                                                         |
| Public-key display with copy-to-clipboard + QR code for phone setup | P1    | QR code in TUI via `go-qrcode` + ANSI.                                  |
| iOS Blink Shell walkthrough screen with copy-paste config           | P1    | Static screen, version-controlled.                                      |
| iOS Termius alternative walkthrough                                 | P2    |                                                                         |
| Obsidian vault path detection and config                            | P1    | Default: project `docs/` dir.                                           |
| `ccmux doctor` health-check command                                 | P1    | Runs all the above checks non-interactively. Exit code = problem count. |
| Self-update: `ccmux update` pulls latest release from GitHub        | P2    |                                                                         |

## Notifications

| Feature                                                                 | Phase | Notes                                                                                                 |
| ----------------------------------------------------------------------- | ----- | ----------------------------------------------------------------------------------------------------- |
| Terminal bell injection by daemon when Claude needs input               | P1    | `tmux send-keys -t <pane> ''` then write `\a`. Better: append a no-op bell line via `tmux pipe-pane`. |
| Configurable idle threshold for "needs input" detection (default 30s)   | P1    |                                                                                                       |
| Per-session mute toggle                                                 | P2    |                                                                                                       |
| ntfy.sh forward (optional): also push to your phone as a backup channel | P3    |                                                                                                       |
| Email digest of session activity (optional)                             | L     |                                                                                                       |
| Native iOS push via custom app                                          | L     |                                                                                                       |

## TUI Quality of Life

| Feature                                                                                              | Phase | Notes                                                                                                                                                                                                                                                                                       |
| ---------------------------------------------------------------------------------------------------- | ----- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Catppuccin Mocha as default theme                                                                    | P1    | Default palette exposed as `styles.DefaultPalette` (renamed from `CatppuccinMocha` during the design-system pass so a future palette swap doesn't churn call sites).                                                                                                                        |
| Design-system foundation (tokens + shared components + lint test)                                    | P1    | Tokens (`Spacing`, `Radius`, `Typography`, `Semantic`) live in `internal/tui/styles/`; shared chrome (`Header`, `HelpBar`, `List`) in `internal/tui/components/`; `TestNoInlineStyleLiteralsInScreens` blocks regressions. Full contract in `docs/02_Architecture/04_TUI_Design_System.md`. |
| Priority-driven HelpBar replaces hardcoded footer                                                    | P1    | Each screen exposes `HelpBarProps`; lowest-priority entries drop first at narrow widths.                                                                                                                                                                                                    |
| Dashboard Usage panel: Claude / Cost / Tokens / per-agent breakdown with consistent indent hierarchy | P1    | `u` opens the full breakdown (top projects, cache hit rate, cost-per-prompt) as a modal overlay.                                                                                                                                                                                            |
| Theme switcher: Catppuccin (Mocha/Latte), Dracula, Nord, Gruvbox, Tokyo Night                        | P2    | All Lipgloss color tables, no runtime cost. Tokens layer is already structured to make this swap mechanical.                                                                                                                                                                                |
| `?` opens contextual key help overlay (Bubbles `help`)                                               | P1    |                                                                                                                                                                                                                                                                                             |
| Vim-style navigation (`h/j/k/l`) and arrow keys both work                                            | P1    |                                                                                                                                                                                                                                                                                             |
| Status bar: hostname, tailnet status, session count, current time                                    | P1    |                                                                                                                                                                                                                                                                                             |
| Toast notifications for in-app events (session killed, note created)                                 | P1    |                                                                                                                                                                                                                                                                                             |
| Loading spinners for any operation > 200ms                                                           | P1    | Bubbles `spinner`. _(Pending: swap the current `(loading transcripts…)` muted placeholders to real spinner widgets — small follow-up to the design-system change.)_                                                                                                                         |
| `Ctrl-P` global fuzzy command palette                                                                | P2    | All actions reachable from one place.                                                                                                                                                                                                                                                       |
| Mouse support (optional, on by default)                                                              | P1    | Bubble Tea has it for free.                                                                                                                                                                                                                                                                 |
| Mobile-friendly narrow-terminal layout (auto-switches under 120 cols; curates reference detail away) | P1    |                                                                                                                                                                                                                                                                                             |

## Observability / Metrics

| Feature                                                                                    | Phase | Notes                                                                                                                                                                                                                                                                                                                                   |
| ------------------------------------------------------------------------------------------ | ----- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Daemon tracks per-session: uptime, message count, last-activity timestamp                  | P1    | SQLite.                                                                                                                                                                                                                                                                                                                                 |
| Per-project activity heatmap (GitHub-style)                                                | P2    | Rendered with Lipgloss block characters.                                                                                                                                                                                                                                                                                                |
| Cost tracking: parse Claude Code `~/.claude/projects/<…>/sessions/*.jsonl` for token usage | P2    | Stretch; depends on JSONL stability. Cost numbers come from `ccusage` (Claude-only).                                                                                                                                                                                                                                                    |
| Cursor usage metrics: read `~/.cursor/ai-tracking/ai-code-tracking.db` SQLite              | P2    | Cursor's `~/.cursor/chats/<hash>/<uuid>/store.db` (per-conversation) and `ai-code-tracking.db` (aggregate, with `ai_code_hashes`, `scored_commits`, `conversation_summaries` tables) give us measurable usage without API access. Would replace the dashboard's `Cursor · recent · no conversations yet` placeholder with real numbers. |
| OpenAI/Codex cost estimator (rate table per model)                                         | P2    | Codex transcripts give us tokens; cost requires shipping an OpenAI per-token rate table (no ccusage-equivalent for OpenAI). Without it, the dashboard renders `Codex · recent · N prompts · X in · Y out · no cost estimate`.                                                                                                           |
| Weekly summary email/file: time per project, prompts sent, files changed                   | P3    |                                                                                                                                                                                                                                                                                                                                         |
| Anonymous opt-in usage telemetry                                                           | —     | Never. Explicit non-goal.                                                                                                                                                                                                                                                                                                               |

## CLI Surface (scripting)

| Command                           | Phase | Notes                                                                  |
| --------------------------------- | ----- | ---------------------------------------------------------------------- |
| `ccmux`                           | P1    | Launch TUI.                                                            |
| `ccmux attach [project]`          | P1    | Attach to session for project (default: cwd). Direct shim for `cc`.    |
| `ccmux new <name> [--agent <id>]` | P1    | Create the project directory + start an agent session. No scaffolding. |
| `ccmux list [--json]`             | P1    | List sessions. JSON for scripting.                                     |
| `ccmux kill <project>`            | P1    | Kill a session.                                                        |
| `ccmux setup`                     | P1    | First-run wizard.                                                      |
| `ccmux doctor`                    | P1    | Health check.                                                          |
| `ccmuxd` (daemon binary)          | P1    | Usually managed by launchd; can be run manually.                       |
| `ccmux daemon start/stop/status`  | P1    | Convenience wrappers around launchctl.                                 |
| `ccmux note <type> [title]`       | P2    | Create an Obsidian note from anywhere.                                 |
| `ccmux snapshot <session>`        | P3    | Snapshot a session.                                                    |

## Distribution

| Feature                                                                          | Phase | Notes                                |
| -------------------------------------------------------------------------------- | ----- | ------------------------------------ |
| Homebrew tap: `brew install skzv/tap/ccmux`                                      | P1    | Goreleaser-managed.                  |
| GitHub Releases with prebuilt darwin-arm64 / darwin-amd64 / linux-amd64 binaries | P1    |                                      |
| Install one-liner: `curl -fsSL ccmux.dev/install.sh \| sh`                       | P2    | Cute, but Homebrew is enough for P1. |
| Auto-update notice on launch when newer version on GitHub                        | P2    |                                      |
| VHS-recorded demo GIF in README                                                  | P1    | Critical for traction.               |
| Animated splash on first launch (subtle)                                         | P1    |                                      |

## Long-Term (native iOS app)

| Feature                                                       | Phase | Notes                                                  |
| ------------------------------------------------------------- | ----- | ------------------------------------------------------ |
| SwiftUI app that talks to ccmuxd over Tailscale               | L     | gRPC or JSON-over-HTTP; designed alongside ccmuxd IPC. |
| Native iOS push via APNs                                      | L     |                                                        |
| Touch-optimized session list (swipe-to-attach, swipe-to-kill) | L     |                                                        |
| Claude conversation view built for thumb input                | L     |                                                        |
| Obsidian-Sync-aware vault viewer in the same app              | L     |                                                        |
| Apple Watch glance: "1 session waiting for input"             | L     |                                                        |

---

## Phase 1 (v0.1) Definition of Done

- ccmux binary builds, launches a TUI, lists sessions, attaches/kills/renames, creates new projects.
- ccmuxd daemon runs in the background (launchd plist provided), polls tmux, rings the bell.
- Vault tab works: tree view, glamour preview, three quick-actions.
- `ccmux setup` walks a fresh machine to working state.
- `ccmux doctor` reports problems.
- README is good enough to merit a Hacker News post.
- Demo GIF in README.
- Homebrew install one-liner.
