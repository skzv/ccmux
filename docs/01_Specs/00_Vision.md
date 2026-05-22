# ccmux — Vision

> One tool to start, resume, and supervise Claude Code sessions from anywhere.

## The Problem

The current workflow stitches together five pieces by hand:

- **tmux** keeps Claude Code alive across disconnects.
- **Tailscale** gives the host (a Mac Mini M4 Pro) a stable private address.
- **Mosh** keeps the connection from a phone alive across cell/wifi drops.
- **Markdown files in `docs/`** hold the project's specs, ADRs, and agent logs.
- **Claude Code** is what's actually doing the work inside the tmux pane.

A few zsh functions (`cc`, `mkproj`, `upgrade-proj`) glue them together. That's enough for one person who already knows the stack, but:

- It's invisible to a newcomer. There's no UI showing what sessions are running, which are idle, which need input.
- Mobile UX is "ssh in and remember the tmux session name."
- Bootstrapping a new machine — Mosh, Tailscale, Blink Shell on the phone — is a manual checklist with no validation.
- The `docs/` directory exists by convention; nothing populates the agent log automatically and there's no first-class way to browse notes from the phone without bolting on a sync service.

## The Vision

**ccmux is the front door to your AI development environment.** From any device on your tailnet, you launch the TUI and see — at a glance — every Claude Code session you have running across every project. You see which ones are idle, which are mid-conversation, which need your input. You attach, detach, spawn new sessions, and browse project notes without leaving the terminal.

On your phone, the experience is the same TUI rendered into a Mosh-backed Blink Shell pane. When Claude needs input on a session you're not watching, the daemon rings the terminal bell — the iOS terminal client fires a push notification — and you tap it to jump straight to that session.

When you start a new project, ccmux creates the directory and starts the agent session — and stops there. It does not scaffold: no CLAUDE.md, no docs/ tree, no git init. Bootstrapping is the agent's job, run inside the session (`/init`, `openspec`); ccmux just opens the door.

It's the workflow you have today, made *legible*. And then it grows: cost tracking, session snapshots, multi-machine view, a native iOS client.

## The User Story (end to end)

> It's 11pm. I'm in bed. I open Blink Shell on my phone, which auto-connects via Mosh to my Mac Mini over Tailscale. I type `ccmux`.
>
> A clean Charm-styled TUI loads. The dashboard shows three active Claude sessions across three projects. One has a yellow "waiting for input" marker — the bell I heard fifteen minutes ago was that session asking me a question. I press `Enter`, attached. I answer Claude, hit detach (`Ctrl-b d`), back to the dashboard.
>
> I navigate to the Notes tab. ccmux shows every markdown file in the project I was just in, grouped by folder, with content rendered inline. I open today's agent log, press `e` to edit it in `nvim`, jot two lines, close the editor. The files are on the Mac; my iPad picks them up over the tailnet when I open the ccmux web viewer in Safari tomorrow.
>
> I quit ccmux and lock the phone.
>
> Tomorrow morning, on my MacBook on the train, I tether through Tailscale, SSH into the Mini, run `ccmux`. Same view. Same sessions. The agent log I wrote last night shows up under "Recent notes."

## Local and Server Modes

ccmux is **symmetric**. There is no special "host" binary or "client" binary — every machine installs the same pair (`ccmux` + `ccmuxd`) and can play either role.

- **Local mode.** On your laptop, ccmuxd manages local tmux sessions and prevents system sleep (`caffeinate -s` lock) while any Claude session is active. Close the lid (on AC power), the session keeps running. Open the lid, it's exactly where you left it.
- **Server mode.** On your Mac Mini, ccmuxd binds an HTTP API to the Tailscale interface. From your laptop or any tailnet device, your local ccmux TUI lists the Mini's sessions alongside any local ones. One Enter attaches via Mosh.
- **Mixed.** The dashboard shows both, color-coded by origin. From your laptop on a flight you see only local sessions; on the train you see local + Mini; on your phone you see whichever host you're connected to.

This means you don't have to pick a "headquarters." The desk-bound Mini handles your big jobs and your laptop picks up local quick edits — but every Claude session is reachable from every device, every time.

## Design Principles

1. **Terminal-first, not terminal-only.** Everything must work in a Mosh pane on an iPhone. No fancy mouse interactions, no graphics outside text. Every action is keyboard-driven and discoverable via `?`.
2. **One source of truth: tmux.** ccmux never persists session content. tmux is the database. ccmux is a view over tmux state. If ccmux dies, sessions live; if a session dies, tmux holds its scrollback.
3. **Plain markdown on disk is the source of truth.** Notes live with the project they belong to. ccmux is the primary interface (renders, creates, browses). Obsidian, if installed on your Mac, is an *optional* desktop add-on — never a hard dependency. No required sync service, no required cloud account.
4. **Notifications by terminal bell.** No custom push pipeline. The daemon writes `\a` into the tmux pane when Claude needs input. iOS terminal clients turn the bell into a notification. This is the right level of abstraction.
5. **Setup is a flow, not a README.** First-run wizard. Detects what's missing. Installs what it can. For things it can't (Blink Shell, Tailscale account), it shows the screen, the command, the link.
6. **Defaults are opinionated; everything is overridable.** The default projects directory is `~/Projects`. The default theme is Catppuccin Mocha. The default Obsidian root is `docs/`. All configurable in `~/.config/ccmux/config.toml`.
7. **No required cloud.** No accounts, no telemetry, no auth server, no notes-sync subscription. ccmux runs on your Mac. Tailscale is the only network layer. The files are already on the Mac; the Mac is the truth.

## Non-Goals (for now)

- A web UI. Tailscale + a TUI in a terminal is enough. Web UI joins the roadmap if there's demand.
- Multi-tenancy or team features. ccmux is a personal tool. Team mode is a v2+ conversation.
- Replacing tmux. ccmux is a *client* of tmux, not a fork or wrapper that hides it.
- Replacing Obsidian. Markdown in `docs/` is the contract; the user can use any editor.

## The Long-Term Bet

The most-used path is *phone → tailnet → ccmux on the Mini → Claude session*. The bottleneck on that path is the terminal client on the phone. Blink Shell is great; it's not yours. The natural endgame is a native iOS app that speaks directly to ccmuxd over Tailscale: native push notifications, a touch-optimized session list, a Claude conversation UI built for one-handed thumb input. That's a v2 conversation, but the architecture (daemon + IPC) is designed to make it possible.
