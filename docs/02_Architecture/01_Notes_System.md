# Notes System

## Design Stance

Markdown files on disk are the source of truth. ccmux is the primary interface to them — terminal-first, accessible over the same SSH/Mosh/Tailscale path that already runs everything else. **No required external app, no required sync service, no paid subscription.**

Obsidian is an optional desktop-only convenience: if you happen to have it installed on your Mac, "Open in Obsidian" works for graph view and plugin chains. The mobile workflow does not depend on it.

## Why Not Obsidian-First

| Reason | Detail |
|---|---|
| Each project is technically its own vault | Obsidian's vault model fights a many-projects workflow. Switching vaults on iOS is awkward. |
| Sync is the user's problem | Obsidian Sync is $8/mo. iCloud only works on Apple devices. Git plugin on iOS is unreliable. LiveSync is good but adds CouchDB to operate. |
| The phone already SSHes into the Mac | The files are already reachable. Adding a sync layer is duplicative. |
| Glamour renders markdown beautifully in a terminal | The terminal UI we're already building is a fine notes reader. |

## Directory Layout (unchanged)

```
~/Projects/foo/
├── CLAUDE.md
├── docs/                          ← plain markdown, nothing Obsidian-specific
│   ├── 01_Specs/
│   │   ├── 00_Initial_Concept.md
│   │   └── 01_Auth_Flow.md
│   ├── 02_Architecture/
│   │   ├── 00_System_Design.md
│   │   └── 01_Database_Choice.md
│   └── 03_Agent_Logs/
│       ├── 2026-05-09.md
│       └── 2026-05-10.md
├── src/
└── tests/
```

The structure (`01_Specs`, `02_Architecture`, `03_Agent_Logs`) is *convention*, not Obsidian. ccmux honors it; Obsidian, if installed, also honors it because it's just files on disk.

## Primary: ccmux Notes Tab

A "Notes" tab in the TUI, scoped to the currently focused project. Two-pane layout:

```
┌───── docs/ ──────────────┐  ┌──── 01_Specs/00_Initial_Concept.md ─────┐
│ ▼ 01_Specs              │  │                                          │
│   • 00_Initial_Concept  │  │  # Initial Concept                       │
│   • 01_Auth_Flow        │  │                                          │
│ ▼ 02_Architecture       │  │  ## Problem                              │
│   • 00_System_Design    │  │                                          │
│   • 01_Database_Choice  │  │  Building this because…                  │
│ ▼ 03_Agent_Logs         │  │                                          │
│   • 2026-05-09          │  │  ## Approach                             │
│   • 2026-05-10  ←       │  │                                          │
└──────────────────────────┘  └──────────────────────────────────────────┘
  ↑↓: navigate  n: new  e: edit in $EDITOR  /: search  o: open in Obsidian*
                                                       * if installed on host
```

Rendering: Glamour with the active theme. Wikilinks (`[[foo]]`) and markdown links resolve within the project's `docs/` tree. The renderer caches output keyed by `(path, mtime)` so re-opening a note is instant.

## Quick-Actions

| Action | Result |
|---|---|
| `n` then `a` | New Agent Log → `docs/03_Agent_Logs/YYYY-MM-DD.md` (today's, created if missing) with templated header. Opens in `$EDITOR`. |
| `n` then `s` | New Spec → prompts for title; creates `docs/01_Specs/NN_<title>.md` (NN auto-incremented). |
| `n` then `d` | New ADR → same flow for `docs/02_Architecture/`. |
| `e` | Edit selected file in `$EDITOR` (nvim, vim, helix, code). |
| `/` | Full-text search across the project's `docs/`. Ripgrep when available. |
| `o` | Open in Obsidian via `obsidian://` URI (macOS only; hidden if Obsidian not installed). |

## Templated Frontmatter

**Agent Log (`docs/03_Agent_Logs/YYYY-MM-DD.md`):**

```markdown
---
date: 2026-05-10
project: foo
sessions: []
---

# Agent Log — 2026-05-10
```

**Spec (`docs/01_Specs/NN_<title>.md`):**

```markdown
---
id: NN
title: <title>
status: draft
created: 2026-05-10
owner: skz
---

# <title>

## Problem

## Approach

## Open Questions
```

**ADR (`docs/02_Architecture/NN_<title>.md`):** standard `Status / Context / Decision / Consequences` block.

## Auto-Logged Session Starts

When you start a Claude session via ccmux, ccmux appends a single line to today's Agent Log:

```markdown
## 21:14 — Started session `c-foo`
**Initial prompt:** "Refactor the auth middleware to use the new token format"
```

Daily journal builds itself. Disabled per-project via `.ccmux.toml`:

```toml
[notes]
auto_log_sessions = false
```

## Optional: Tailnet Web Viewer (P2)

ccmuxd exposes a small HTTP server bound to the Tailscale interface (`100.x.x.x:7474` by default). Serves rendered markdown via Goldmark + a minimal HTML layout. From any device on your tailnet, open Safari/Firefox to `http://mini.tail-xxxxx.ts.net:7474` and browse notes.

Why this exists:
- Read on an iPad with no terminal app.
- Send a link to a collaborator on your tailnet ("look at this spec").
- One-handed reading on the phone when the keyboard isn't worth pulling out.

Bound only to the tailnet IP (`tailscale ip -4`), never `0.0.0.0`. No auth — your tailnet is the authentication boundary.

Eventually: edit-in-browser via simple `<textarea>` + autosave. (P3.)

## Optional: Obsidian on Desktop

If `/Applications/Obsidian.app` exists, the "Open in Obsidian" action is enabled. It builds an `obsidian://open?vault=<vault-name>&file=<path>` URI and execs `open`. Vault name comes from `.obsidian/app.json` if present, else project basename.

The user adds the project's `docs/` as an Obsidian vault once (drag-and-drop in Obsidian onboarding); after that, "Open in Obsidian" jumps to the right file every time. ccmux does not manage Obsidian's vault registration.

## Optional: Obsidian on Mobile (Power User)

For users who really want graph view and Obsidian-style note-taking on iOS, the recommended path is **LiveSync** (community plugin) backed by a self-hosted **CouchDB** on the Mac Mini. It's free, real-time, and works over Tailscale.

This is documented in `docs/04_Guides/Obsidian_LiveSync_Setup.md` (P2) but explicitly **not** required. The default workflow does not assume Obsidian is installed anywhere.

## Why Not [Other Tool]

| Tool | Verdict for this workflow |
|---|---|
| **Logseq** | Block-based outliner. Different mental model. Adds a dependency for marginal benefit over plain MD + TUI. |
| **Joplin** | Solid, but the UX is heavier than we need and its database format (not plain files by default) fights "files on disk = source of truth." |
| **Bear** | Apple-only, paid sync. Same problem as Obsidian, smaller community. |
| **VS Code / Cursor** | Excellent on desktop. No mobile story. Could be the desktop `$EDITOR` fallback. |
| **GitHub web/mobile** | Works! Free fallback for read-on-the-go: just push to git, view in the iOS GitHub app. Will mention in docs as a complementary path. |

## Decisions Locked In

1. Plain markdown in `docs/` is the source of truth.
2. ccmux TUI is the primary notes UI on every device.
3. Obsidian is desktop-only and optional.
4. Tailnet web viewer (P2) is the recommended way to read notes outside the terminal.
5. No required sync service. The files are on the Mac; the Mac is the truth.
