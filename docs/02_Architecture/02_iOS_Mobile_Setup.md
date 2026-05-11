# iOS Mobile Setup — Moshi + ccmux

The mobile workflow is built around **[Moshi](https://getmoshi.app)** — an iOS/Android terminal app purpose-built for AI coding agents over Mosh + tmux. Moshi handles push notifications via its `moshi-hook` daemon, which plugs directly into Claude Code's hooks system on the host. No terminal-bell tricks, no custom push pipeline.

## What you get

From the phone:

1. Tap a saved Moshi connection.
2. Mosh + Tailscale brings you up on the Mac Mini, attached to the persistent `ccmux` outer tmux session running the TUI.
3. You see every Claude session across every project. Tap one to attach. Detach (`Ctrl-b d`) drops you back in the TUI.
4. When any session needs your attention — Claude is awaiting approval, a long task finishes, an error fires — `moshi-hook` sends a categorized push notification through Moshi. Tap, you're attached to exactly that session.

## Why Moshi (over Blink Shell, Termius, etc.)

| Capability | Moshi | Blink | Termius |
|---|---|---|---|
| Mosh protocol | ✓ | ✓ | partial |
| Native tmux UI affordances | ✓ | manual | manual |
| **Structured push notifications** (categorized: approval / task-done / session-started) | ✓ via `moshi-hook` | bell only | bell only |
| Claude Code hooks integration | ✓ built-in | — | — |
| Voice-to-terminal | ✓ | — | — |
| SSH keys in iOS Keychain + biometric unlock | ✓ | ✓ | ✓ |
| Tailscale routing | OS layer | OS layer | OS layer |

Moshi is the only one of the three with first-class agent integration. The others would work, but you'd be back to writing `\a` into panes and hoping the terminal raises a notification. ccmux supports both paths — `ccmuxd` will fall back to bell injection if `moshi-hook` isn't detected — but Moshi is the recommended setup.

## Stack

| Layer | Purpose | Where |
|---|---|---|
| **Tailscale** | Private mesh; stable hostname for the Mac Mini from anywhere. | iOS app + Mac Mini |
| **Mosh** | Roaming-resilient SSH replacement. | `mosh-server` on Mac Mini, Moshi-internal client on phone |
| **moshi-hook** | Daemon on the Mac Mini; bridges Claude Code hooks → Moshi WebSocket → phone push. | Mac Mini (background launchd / systemd service) |
| **Moshi** | The iOS/Android terminal app. Where the TUI is rendered on mobile. | Phone |
| **tmux** | Session persistence on the Mac Mini. Outlives any ssh/mosh connection. | Mac Mini |
| **ccmux + ccmuxd** | Session/project management TUI. Runs on the Mac Mini; rendered remotely via Moshi. | Mac Mini |

## One-time host setup

ccmux automates all of this via `ccmux moshi-setup`. The manual version:

```bash
# 1. Core deps (skip what's already installed)
brew install tmux mosh ripgrep

# 2. Tailscale on the Mac Mini (if not already installed via the official package)
brew install tailscale && sudo tailscaled install-system-daemon
tailscale up

# 3. Claude Code itself
curl -fsSL https://docs.claude.com/install.sh | sh   # or whichever path Anthropic ships

# 4. moshi-hook — the agent-hook bridge
brew tap rjyo/moshi
brew install moshi-hook

# 5. Pair with the Moshi app
#    In Moshi: Settings → Get pairing token → copy
moshi-hook pair --token $MOSHI_PAIRING_TOKEN

# 6. Wire moshi-hook into Claude Code (writes ~/.claude/settings.json hooks block)
moshi-hook install

# 7. Run it as a service so it survives reboots
brew services start moshi-hook
```

Verify the daemon is connected:

```bash
moshi-hook status   # should report paired + connected
ccmux doctor        # ccmux's own health check now includes moshi-hook
```

## One-time phone setup

1. **Install [Tailscale](https://tailscale.com/download) on iOS.** Sign into the same tailnet as the Mac Mini.
2. **Install [Moshi](https://getmoshi.app/) on iOS.** Open the app.
3. **Pair Moshi with the host:** Settings → generate pairing token → paste into the `moshi-hook pair --token` command on the host (step 5 above). Moshi will show "Connected."
4. **Add the host as a connection in Moshi:**
   - Host: `<your-tailnet-hostname>` (e.g. `mini.tail-xxxxx.ts.net` or just `mini`)
   - User: your Mac username
   - Connection type: **Auto** (Moshi picks mosh, falls back to ssh)
   - Mosh: on
   - Optional: Authentication via SSH key — Moshi can generate one stored in iOS Keychain with biometric unlock, then upload the public key to `~/.ssh/authorized_keys` on the host.
   - **Command to run on connect:** `tmux new-session -A -s ccmux ccmux`
     - `-A -s ccmux` attaches to (or creates) a tmux session named "ccmux."
     - `ccmux` (the binary) runs inside it — Moshi lands you in the TUI.

That's it. Tap the connection. Land in the ccmux TUI. Browse sessions. Attach. Work. Phone goes black. Tomorrow, tap the same connection — you're right back where you were.

## The persistent-session convention

ccmux uses **one always-running outer tmux session called `ccmux`** that hosts the TUI itself. Each project's Claude session runs inside its own *inner* tmux session (`c-<project>`). When the phone connects, Moshi attaches you to the outer session. Pressing Enter on a project in the TUI shells you out to that project's inner session via `tmux switch-client` or `tmux attach`. Detach drops back to the TUI.

```
mosh sasha@mini.tail-xxxxx.ts.net
  → tmux new-session -A -s ccmux ccmux           ← outer: hosts the TUI
      → user picks project "foo" in TUI
      → tmux switch-client -t c-foo              ← inner: Claude session
         (or attach if running solo)
      → user works, detaches with Ctrl-b d
      → back in outer "ccmux" session, TUI re-rendered
```

Set `set-option -g destroy-unattached off` in your `~/.tmux.conf` so the outer session never dies even with no client. (ccmux's setup wizard will add this if missing.)

## What ccmuxd does when moshi-hook is present

When `moshi-hook` is detected (its socket exists or `~/.claude/settings.json` contains the hook entries), ccmuxd:

- **Does not ring the terminal bell on needs-input transitions** — `moshi-hook` already sends a structured `approval_required` notification through Moshi. Sending the bell too would produce a duplicate.
- **Still tracks state for the dashboard** (idle / active / needs-input) — this is for the TUI display, not for notifications.
- **Still manages sleep prevention** (`caffeinate -s` Mode 1, optional Mode 2) — unrelated to notifications.

When `moshi-hook` is absent (Blink Shell or any other terminal), ccmuxd falls back to ringing the BEL byte in the pane on needs-input transitions, which most iOS terminals turn into a push notification. Less rich (no category, no message), but it works.

## Failure modes

| Symptom | Likely cause | Fix |
|---|---|---|
| Mosh hangs on connect | Tailscale not running on phone | Open Tailscale iOS app, toggle VPN on. |
| "Connection refused" | mosh-server not on the SSH PATH | Add `/opt/homebrew/bin` to `PathRemoteCommand`, or symlink `mosh-server` to `/usr/local/bin`. |
| No push notifications | `moshi-hook` not running / not paired | `moshi-hook status`; if "not connected", `moshi-hook serve` to see live errors. |
| Push notifications duplicate | ccmuxd bell + moshi-hook both firing | `ccmux config set daemon.send_bell false`, or upgrade — ccmux auto-detects moshi-hook and suppresses the bell. |
| Push notifications miss `approval_required` | `moshi-hook install` not run, hooks block missing from `~/.claude/settings.json` | Re-run `moshi-hook install`, restart `brew services restart moshi-hook`. |
| Session goes dead after long sleep | Mosh keepalive disabled | Moshi → Connection → Mosh → Keepalive: 30s. |
| Garbled colors | Terminal type mismatch | Moshi → Terminal → TERM: `xterm-256color`. |

## Apple Watch (Moshi Pro)

Moshi Pro includes an Apple Watch companion that surfaces the active session count and the most recent notification. ccmux doesn't drive the watch directly — it goes through `moshi-hook`. If you have Moshi Pro, no extra setup beyond pairing the watch in the iOS app.

## Long-term: native ccmux iOS app

Even with Moshi, the long-tail goal in the roadmap is a native ccmux iOS app that talks directly to `ccmuxd` over Tailscale — a touch-optimized session list, an attach view built for thumb input, an Apple Watch glance. Moshi covers 90% of that need today, so this is genuinely a v2+ conversation rather than a near-term priority.
