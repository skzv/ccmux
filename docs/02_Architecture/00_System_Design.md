# ccmux — System Design

## Deployment Modes

ccmux is **symmetric** — every machine installs the same `ccmux`+`ccmuxd` pair. A machine can simultaneously be:

1. **Local** — manages its own tmux/Claude sessions via Unix-socket IPC.
2. **Server** — exposes its sessions over Tailscale via a small HTTP API for remote ccmux clients.
3. **Client** — connects to one or more remote ccmuxd HTTP endpoints and shows their sessions alongside any local ones.

The three are not exclusive. A laptop is typically `local + client`. A Mac Mini is typically `local + server`. A workstation could be all three.

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

When the user presses Enter on a remote session row, the local ccmux execs `mosh <host> -- tmux attach -t <session>`. On detach (`Ctrl-b d`), the user is back in the local TUI.

The HTTP API is the same schema as the Unix socket protocol — just transported over `net/http` instead of `net.Conn`. One server implementation, two listeners.

## Sleep Prevention (local mode)

ccmuxd holds a sleep-blocker subprocess while any "active" Claude session exists. The blocker is released when all sessions are idle for ≥ N minutes (configurable, default 10), or by explicit TUI action. Per-session "always keep awake" pins (`k` keybind) override the idle threshold.

There are three modes, escalating in risk:

### Mode 1: Safe (default)

- **macOS:** `caffeinate -s` — prevents system sleep only while on AC power. If unplugged, normal sleep behavior resumes.
- **Linux:** `systemd-inhibit --what=sleep:idle --who=ccmuxd --why="Claude session active" cat`.

This is the default and covers the canonical use case (Mac Mini on a desk; MacBook plugged in with lid closed).

### Mode 2: Dangerous — keep awake on battery too

Opt-in via TUI setting or `~/.config/ccmux/config.toml`:

```toml
[sleep]
dangerous_keep_awake_on_battery = true
low_battery_cutoff = 20   # percent; auto-release the lock when AC is gone and battery drops below this
```

When enabled:

- **macOS:** ccmuxd uses `caffeinate -d -i -m -u` (prevent display, idle, disk sleep, and assert user activity). This works on battery, but **the lid-close-forces-sleep behavior is enforced by hardware/firmware and `caffeinate` cannot override it.** Lid-closed-on-battery still sleeps.
- ccmuxd polls battery state every 30s via `pmset -g batt`. If unplugged and battery < `low_battery_cutoff`, the lock is auto-released and a notification is shown in the TUI.
- The TUI shows a **red persistent banner**: `⚠ DANGEROUS MODE — battery sleep prevention active`.

This mode is documented as "use for short bursts when you can't plug in and you really need Claude to keep working." Not for unattended overnight use on battery.

### Mode 3: Very Dangerous — override lid-close (sudo required)

For users who genuinely want the laptop to stay awake with the lid closed on battery (e.g. running headless without an external display), the only path is to disable lid-close-sleep at the system level:

```bash
sudo pmset -b disablesleep 1   # battery only
```

ccmuxd will detect this state (via `pmset -g`) and surface a setting in the TUI: **"Mode 3 detected — battery + lid-close sleep disabled system-wide."** ccmux does **not** call sudo on the user's behalf; the user opts in by running `pmset` themselves, and ccmux just respects/displays the resulting state.

The TUI shows: `⚠⚠ MODE 3 ACTIVE — system-wide sleep override. Remember to re-enable.`

When the user toggles "Mode 3" off in the TUI, ccmux prints the un-do command (`sudo pmset -b disablesleep 0`) for the user to run.

### Summary of risk levels

| Mode | Plugged | Battery (lid open) | Battery (lid closed) | Requires sudo |
|---|---|---|---|---|
| 1 (Safe, default) | Stays awake | Sleeps normally | Sleeps normally | No |
| 2 (Dangerous) | Stays awake | Stays awake until low battery cutoff | Sleeps (HW-enforced) | No |
| 3 (Very Dangerous) | Stays awake | Stays awake | Stays awake | Yes (user-triggered, once) |

## Components

```
┌─────────────────────────────────────────────────────────────────────┐
│                          MAC MINI HOST                              │
│                                                                     │
│   ┌──────────────────────┐         ┌──────────────────────────┐    │
│   │      ccmux (TUI)     │  unix   │      ccmuxd (daemon)     │    │
│   │   Bubble Tea app     │ ◄────►  │   - polls tmux every 2s  │    │
│   │   user terminal      │ socket  │   - detects idle Claude  │    │
│   └──────────┬───────────┘         │   - rings bell in pane   │    │
│              │                     │   - writes to SQLite     │    │
│              │ exec                └────────────┬─────────────┘    │
│              ▼                                  │                  │
│   ┌──────────────────────┐                      │ exec             │
│   │       tmux server    │ ◄────────────────────┘                  │
│   │   c-proj-foo  (claude)                                         │
│   │   c-proj-bar  (claude)                                         │
│   │   c-proj-baz  (claude)                                         │
│   └──────────────────────┘                                         │
│                                                                     │
│   ┌──────────────────────┐         ┌──────────────────────────┐    │
│   │   ~/Projects/foo/    │         │   ~/.claude/projects/    │    │
│   │     ├── CLAUDE.md    │         │     <encoded-path>/      │    │
│   │     ├── docs/        │         │     sessions/*.jsonl     │    │
│   │     │   ├── 01_…     │  read   │                          │    │
│   │     │   ├── 02_…     │ ◄───────┤  (Claude Code's          │    │
│   │     │   └── 03_…     │         │   transcript store)      │    │
│   │     └── src/         │         │                          │    │
│   └──────────────────────┘         └──────────────────────────┘    │
└─────────────────────────────────────────────────────────────────────┘
                                  ▲
                                  │ Mosh over Tailscale
                                  │ (TCP/UDP, NAT-traversed)
                                  │
              ┌───────────────────┴────────────────────┐
              │              iPhone                    │
              │   Blink Shell  ──►  mosh user@host     │
              │                     ccmux              │
              │   ("Moshi" / Termius work the same)    │
              │                                        │
              │   Obsidian iOS (via Sync) ◄── docs/    │
              └────────────────────────────────────────┘
```

## Process Model

- **ccmux (TUI)** runs in the user's terminal. One per logged-in session. Stateless beyond `~/.config/ccmux/`.
- **ccmuxd (daemon)** runs once per host as a user-mode launchd service (`~/Library/LaunchAgents/dev.ccmux.daemon.plist`). On Linux: systemd user service. Crashes are recovered by launchd within seconds.
- **tmux server** is the user's existing tmux. ccmux does not start its own server; it uses `$TMUX_TMPDIR` / default socket.

## IPC Protocol

ccmuxd serves the same JSON-over-HTTP protocol on two listeners:

- **Local (always on):** Unix socket at `~/.local/state/ccmux/ccmuxd.sock`, file mode 0600.
- **Tailnet (opt-in via config):** HTTP on the Tailscale interface, default `100.x.x.x:7474`. Bound only to the tailnet IP (looked up via `tailscale ip -4`), never `0.0.0.0`.

Endpoints (the full reference — request/response types, errors, the trust
model — lives in [`05_HTTP_API.md`](05_HTTP_API.md); that's the contract for
external integrators such as the Moshi app):

```
GET    /v1/health                         → HealthInfo
GET    /v1/peers                          → []PeerInfo
GET    /v1/sessions                       → []SessionState
POST   /v1/sessions                       → SessionState   (create-or-attach)
POST   /v1/sessions/bare                  → NewBareSessionResponse
POST   /v1/sessions/{name}/kill           → 204
POST   /v1/sessions/{name}/rename         → SessionState
POST   /v1/sessions/{name}/send-keys      → 204
GET    /v1/sessions/{name}/preview        → PreviewResponse
GET    /v1/sessions/{name}/attach         → WebSocket (interactive PTY)
GET    /v1/projects                       → []ProjectInfo
POST   /v1/projects                       → NewProjectResponse
GET    /v1/conversations                  → []Conversation
GET    /v1/usage?window=…                  → AgentUsage
GET    /v1/notes?project=…[&file=…]        → []NoteEntry | NoteContent
GET    /v1/notes/search?project=…&q=…      → []SearchHit
GET    /v1/events                         → SSE stream of SessionEvent
POST   /v1/pair-token (unix socket only)  → PairTokenResponse
POST   /v1/pair                           → PairResponse
POST   /v1/devices                        → 204
POST   /v1/devices/test                   → 204
```

There is **no application-level auth** — the tailnet is the trust boundary
(see the API reference). Same schema on both transports. The client uses the
local socket when possible (faster, no encryption overhead) and HTTP for
configured remote hosts.

Why the daemon at all (vs. TUI shelling out to tmux every render)?

1. The poll loop runs once per host, not once per TUI render. Cheaper.
2. State includes daemon-only derived fields (idle duration, last-bell time, prompt count).
3. The sleep-prevention `caffeinate` lock needs a long-lived process to hold it.
4. Server mode needs a long-lived HTTP listener.

If the daemon is not running, the TUI falls back to direct `tmux` calls and degrades gracefully — daemon-derived fields don't render; sleep prevention and remote-host listing are unavailable.

## Host Registry (client side)

Remote hosts the local ccmux can connect to are listed in `~/.config/ccmux/hosts.toml`:

```toml
[[host]]
name    = "mini"
address = "mini.tail-xxxxx.ts.net"
user    = "skz"
mosh    = true

[[host]]
name    = "lambda-a100"
address = "100.64.0.42"
user    = "ubuntu"
mosh    = true
```

The dashboard pings `GET /v1/health` on each host every 10s. Unreachable hosts grey out but remain in the list (so reconnects don't lose your bookmark).

## Session State Machine

Each tracked session is in exactly one of:

```
                    ┌─────────────┐
              ┌────►│   ACTIVE    │◄────┐
              │     │ (typing in) │     │
   user types │     └──────┬──────┘     │ output
              │            │ idle ≥30s  │ appears
              │            ▼            │
              │     ┌─────────────┐     │
              │     │    IDLE     │─────┘
              │     │  (waiting)  │
              │     └──────┬──────┘
              │            │ Claude prints prompt + idle ≥3s
              │            ▼
              │     ┌─────────────┐
              └─────┤ NEEDS_INPUT │
                    │  🔔 bell    │
                    └─────────────┘
```

"Needs input" detection (heuristic): the bottom line of `tmux capture-pane -p -t <pane>` matches Claude Code's prompt pattern (`╭─` box-drawing prefix + idle cursor), AND no new output has appeared in the last 3 seconds. The daemon transitions and writes a single `\a` to the pane via `tmux send-keys -t <pane> ''` (or by writing the BEL byte directly to the pty). Subsequent bells suppressed until state leaves NEEDS_INPUT.

## Persistence

| Path | Purpose |
|---|---|
| `~/.config/ccmux/config.toml` | User config: projects dir, theme, keybindings, idle thresholds. |
| `~/.local/share/ccmux/ccmux.db` | SQLite: session history, prompt counts, snapshots index. |
| `~/.local/share/ccmux/snapshots/<id>/` | Snapshot archives: tmux scrollback + Claude transcript copy. |
| `~/.local/state/ccmux/ccmuxd.sock` | Daemon Unix socket. |
| `~/.local/state/ccmux/ccmuxd.log` | Daemon log (rotated by lumberjack). |
| `~/.local/state/ccmux/ccmuxd.pid` | Daemon PID file. |
| `<project>/.ccmux/agent` | Per-project sidecar recording which AI agent (claude / codex / antigravity / cursor) the project runs. Written by scaffold and the Projects-screen `a` switcher. |

The XDG-ish split (`config` for user-editable, `share` for app data, `state` for runtime) is intentional. On macOS the canonical place would be `~/Library`, but we'd lose Linux portability. XDG paths work on both.

## Project discovery

`project.Discover(root)` walks the projects root one level deep and surfaces every non-hidden directory as a project — no marker file required. The `HasGit` / `HasCM` / `HasDocs` flags on `Project` still record which of `.git/`, `CLAUDE.md`, and `docs/` are present so the TUI can render them as visual tags ("git · CLAUDE · docs/") — useful for the eye to tell "real software project" from "scratch directory."

The rule used to require one of those markers, which left worktrees without `CLAUDE.md`, freshly-cloned repos, and scratch dirs invisible to ccmux with no in-app fix. The simpler "every directory shows up" rule matches what users actually mean by "everything in my projects folder," and the visual tags carry the marker information for sorting/filtering purposes without acting as a gate.

## tmux Naming Convention

Compatibility with the existing `cc()` zsh function: session name is `c-<basename-with-dots-as-underscores>`. ccmux honors this and creates sessions with the same prefix so the old aliases continue to work during transition.

Future: drop the prefix and use full path as session name (`/Users/skz/Projects/foo` → `c..Users.skz.Projects.foo`)? Decision deferred to v0.2 after user feedback.

## Concurrency

- The TUI is single-threaded Bubble Tea. Long operations (`tmux capture-pane` on huge scrollback, Glamour rendering large docs) are dispatched as `tea.Cmd` goroutines that send a result message back.
- The daemon's poll loop runs in one goroutine. Each session check is a sub-goroutine with a 1s timeout so a hung tmux call can't stall the loop.
- SQLite writes use a single writer goroutine with a buffered channel. Reads from the TUI go through a read-only WAL connection.

## Failure Modes

| Failure | Behavior |
|---|---|
| tmux server not running | TUI shows "no sessions" and a "start a session" CTA. Subcommands exit cleanly with a message. |
| Daemon down | TUI falls back to direct tmux calls. Status bar shows "⚠ daemon offline." |
| Claude binary missing | `ccmux doctor` flags it. New-session flow refuses with a setup link. |
| Tailscale down | Notification: "you're working locally only." Sessions still function. |
| SQLite corruption | Daemon detects on startup, renames the file to `.corrupt`, starts fresh. Metrics history lost but app works. |
| Socket file stale (daemon crashed) | First TUI connection unlinks and retries. |

## Security Model

- ccmux is a single-user tool. No multi-user file locking or permission checks.
- All sockets are 0600 in user's home. No network listeners (Tailscale exposure is via SSH/Mosh, not ccmux itself).
- ccmuxd never reads `~/.claude/credentials.*`. Token usage data, when added, is parsed from public JSONL files only.
- The TUI never echoes the contents of `.env`, `.git/config`, or any file matching a configured ignore pattern when rendering previews.
