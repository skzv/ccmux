# Telegram setup

Control ccmux from Telegram — get pinged when an agent needs you and answer right there: approve/deny, send the agent its own slash-commands, read project notes. From your phone or your watch. The bot reaches out to Telegram over long polling, so it needs no open port and works behind NAT.

## 1. Make a bot

1. Open Telegram, message [@BotFather](https://t.me/BotFather).
2. Send `/newbot`, pick a name and a username.
3. Copy the token it gives you (looks like `123456789:AAE...`).

Optional but recommended for the autocomplete: in BotFather, `/setinline` on your bot enables inline queries (typing `@yourbot ` to search commands). Everything else works without it.

## 2. Register the token

```bash
ccmux telegram register --token 123456789:AAE...
# or keep it out of shell history:
CCMUX_TELEGRAM_BOT_TOKEN=123456789:AAE... ccmux telegram register
```

This writes the token to `~/.config/ccmux/config.toml`, enables the bridge, and **restarts the daemon for you** so it takes effect right away (the bridge reads config at daemon startup). If no daemon is running yet, it starts with the token next time.

(Or just run `ccmux setup` — the wizard has a Telegram step that does the same, restart included.)

## 3. Pair your chat

```bash
ccmux telegram pair
```

It prints a one-time code. Open your bot and send it:

```
/start ABC123
```

That enrolls your chat. **Only chats you pair can drive the bot** — the token alone can't do anything. Codes are single-use and expire after 10 minutes. Pair more devices/people by running `ccmux telegram pair` again.

Check it end to end:

```bash
ccmux telegram status   # enabled, paired count, tiers (token redacted)
ccmux telegram test     # sends a test message to every paired chat
ccmux doctor            # includes a Telegram health check
```

## What you can do

- **Approve / deny.** When an agent hits a permission prompt, the bot messages you with the pane tail and Approve / Deny buttons. Tap one — or reply `y` / `n` / `approve` / `deny` (handy on a watch, which can't always tap inline buttons).
- **Drive the agent.** `/agent [host:session]` lists the agent's own commands as buttons; tap one to send it. Or just type a prompt and it goes to the session you last touched. `/say <host:session> <text>` targets explicitly.
- **Manage the fleet.** `/sessions`, `/preview <host:session> [lines]`, `/projects`, `/usage`, `/new <project> [agent]`, `/kill <host:session>` (asks to confirm), `/send <host:session> <text>`.
- **Read notes.** `/notes <project> [search]` lists the vault; tap a file and Telegram renders the markdown inline.

`/help` lists everything the bot can do.

## Permission tiers

The chat-ID allowlist is the real authorization. On top of that:

| Tier | What | Default |
|------|------|---------|
| Read | `/sessions`, `/preview`, `/projects`, `/usage`, `/notes` | always on |
| Control | approve/deny, `/agent`, `/say`, `/new`, `/kill`, `/send` | allowlisted chats |
| Exec | `/run <host:session> <raw>` — arbitrary keys/shell | **off** (`allow_exec`) |

Driving the agent (its slash-commands and prompts) is *control*, not *exec* — it's the agent's own curated surface, so you don't need to open arbitrary shell to use it. Turn the exec tier on only if you want it:

```bash
ccmux telegram register --token <token> --allow-exec
```

## The whole tailnet, one bot

Run the bot on your always-on machine (the Mac mini). It addresses sessions as `host:session` and reaches every peer's daemon over the same `:7474` tailnet API the dashboard uses, so `/sessions` shows local *and* remote, and `/preview mini:build` previews the pane on `mini`. One bot token, one daemon — Telegram only allows one poller per token, so don't run the bridge on two machines with the same token.

## Optional: notes web viewer

`/notes` sends files as documents (rendered inline). For browsing a whole vault with working links, turn on the web viewer:

```bash
ccmux telegram serve on
ccmux daemon restart
```

It binds to your machine's tailnet address — reachable from your phone *only* while it's on the tailnet, never from the public internet (no `tailscale funnel`). `/notes` then offers an "Open vault in browser" button.

## A note on privacy

Pane tails and note contents you fetch pass through Telegram's servers (like any Telegram message). It's all opt-in and off by default; the pane tail is line-capped before it leaves the machine (`pane_tail_lines`). Don't point it at panes with secrets you wouldn't paste into a chat. The web viewer keeps full note content on your tailnet.

## Config reference

```toml
[telegram]
enabled = true
bot_token = "123456789:AAE..."
allowed_chat_ids = [123456789]
allow_exec = false       # /run arbitrary input — off by default
web_viewer = false       # tailnet-only markdown browser
mute_alerts = false      # silence needs-input alerts, keep commands working
pane_tail_lines = 24     # cap on pane content shipped in alerts
```
