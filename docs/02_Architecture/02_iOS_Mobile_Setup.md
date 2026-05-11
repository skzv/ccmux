# iOS Mobile Setup

How the phone fits into the ccmux workflow, and what `ccmux setup` walks you through for the first connection.

## Goal

From your iPhone — on cellular, on a coffee-shop wifi, or at home — open one app, get a live, persistent Claude Code session on your Mac Mini, with push notifications when Claude needs input.

## Stack

| Layer | Purpose | Where it runs |
|---|---|---|
| **Tailscale** | Private mesh network. Gives the Mac Mini a stable hostname (`mini.tail-xxxxx.ts.net`) and end-to-end encrypted tunnel. | iOS app + macOS host |
| **Mosh** | Roaming-resilient SSH. Survives network changes (cell ↔ wifi) and sleep/wake without dropping the session. | macOS server (`mosh-server`) + iOS terminal client |
| **Blink Shell** *(recommended)* / Termius / "Moshi" | The iOS terminal app. Hosts the Mosh client and the bell-to-notification bridge. | iPhone |
| **tmux** | Session persistence on the server. Outlives any single ssh/mosh connection. | macOS host |
| **ccmux** | TUI that runs *inside* the mosh+tmux session. | macOS host (rendered remotely) |

## Why Blink Shell

- First-class Mosh support, maintained.
- Bell-to-push-notification works out of the box. When Claude rings the terminal bell, iOS shows a push notification — even if Blink is backgrounded or the phone is locked.
- Supports iCloud-synced settings and keys (your config follows you between iPad and iPhone).
- Open source and pays maintainers.

Termius works too (bell support exists but is less reliable on background). "Moshi" the iOS app works if you already have it; same bell flow.

## What `ccmux setup` Does for You

The setup wizard runs through:

1. **Dependency check on the Mac.** Installs missing items via `brew`:
   - `mosh` (the server side)
   - `tmux`
   - `tailscale` (if not already installed via official installer)
   - `claude` (Claude Code CLI)
   - `ripgrep`, `glow` (optional accelerators)

2. **Tailscale status.** Confirms you're signed in. Shows your tailnet hostname. Tests reachability from another tailnet device if available.

3. **SSH key.** Generates `~/.ssh/id_ed25519` if missing. Adds the public key to `~/.ssh/authorized_keys`.

4. **Public key handoff to the phone.** Three ways, presented as cards:
   - **QR code (recommended).** ccmux renders an ANSI QR of the public key. Scan from Blink Shell's "Add Key" screen.
   - **iCloud Keychain.** Copy the key to clipboard; paste into Blink on a Mac signed into the same iCloud; sync to the phone.
   - **Paste manually.** Display key + step-by-step Blink screen names.

5. **Mosh server check.** Tests `mosh-server` is on the PATH. Opens the UDP port range Mosh uses (60000-61000 by default) via macOS firewall API if needed.

6. **Blink Shell host config text.** Prints a copy-pasteable host configuration:

   ```
   Host: ccmux-mini
   HostName: mini.tail-xxxxx.ts.net   (auto-filled with your tailnet name)
   User: skz                          (auto-filled with current user)
   Port: 22
   Key: ccmux-key
   Mosh: enabled
   Mosh server: mosh-server new -s -- /opt/homebrew/bin/tmux new-session -A -s ccmux
   ```

   That `-A -s ccmux` makes the connection attach to (or create) a tmux session called "ccmux," which is where the launcher will be running.

7. **Push notification check.** Asks the phone (manually, via instructions) to send a test bell. ccmux waits, detects the bell, prints success.

8. **First-connect smoke test.** "Open Blink. Run `mosh ccmux-mini`. You should see the ccmux TUI."

## The Persistent Session Convention

For mobile use, ccmux uses one **always-running outer tmux session** called `ccmux` that hosts the TUI itself. Each project's Claude session runs inside its own inner tmux session (`c-<project>`). When the phone connects:

```
mosh ccmux-mini
  → tmux new-session -A -s ccmux ccmux   # outer: hosts the TUI
      → user navigates inside ccmux
      → presses Enter on a project
      → ccmux tmux switch-client to inner c-<project> session
```

This means: closing Blink doesn't kill anything. Opening Blink hours later puts you right back in the TUI. Choosing a project switches to that project's persistent Claude session. Detaching from the inner session (`Ctrl-b d`) drops back to the TUI.

The outer session uses tmux's `set-option -g destroy-unattached off` so it never dies even with no client.

## Failure Modes on Mobile

| Symptom | Likely cause | Fix |
|---|---|---|
| Mosh hangs on connect | Tailscale tunnel not up on phone | Open Tailscale iOS app, ensure VPN is on. |
| "Connection refused" | mosh-server not on PATH for ssh's environment | Set `PathRemoteCommand` in Blink host config; or symlink mosh-server into `/usr/local/bin`. |
| Push notification doesn't fire | Bell muted in Blink settings | Blink → Settings → Bell → On (visual+sound+push). |
| Slow scrolling | Mosh prediction off | Blink → Connection → Mosh → Local Echo: on. |
| Garbled colors | Terminal type mismatch | Blink: set `TERM=xterm-256color`. |
| Session goes dead after long sleep | Mosh keepalive disabled | Blink → Mosh → Keepalive: on (30s). |

## Long-Term: Native iOS App

Blink + Mosh is a great 90% solution. The 10% that's awkward:

- The terminal UI is small for thumbs. Hitting `j` precisely while walking is annoying.
- Push notifications via terminal bell are coarse. Native APNs gives a payload (which session? which project?).
- No background fetch of session state. The TUI only updates when you're looking at it.

The roadmap "native iOS app" item (in `01_Feature_Catalog.md`) builds a SwiftUI client that talks to ccmuxd over Tailscale (the daemon's existing Unix socket would be wrapped in a small HTTP/gRPC server bound to the tailnet interface). Native push, native list UI, native attach. Conversation view optimized for thumb input. Apple Watch glance for the "you have a session waiting" case.

But that's v2+ work. v0.1 ships with Blink + Mosh + bell-to-push, which is already a step-change over manual ssh.
