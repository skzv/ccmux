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

A "Notes" tab in the TUI, scoped to the currently focused project. The
list surfaces *every* markdown file in the project — `README.md`,
`CLAUDE.md`, `openspec/`, anything — grouped by the folder it lives in,
not just the `docs/` subtree. Version-control, dependency, and
build-output directories (`.git`, `node_modules`, `vendor`, `dist`,
`build`, `target`) are pruned. New notes created from the tab still
land under `docs/` (the canonical home).

### Collapsible folder tree

The list is a **collapsible tree**, not a flat dump. Projects with a
deep notes tree (many folders, hundreds of files) would otherwise scroll
far past the pane, so **folders open collapsed**: when you enter a
project you see only the root-level files plus the top-level folder
headers. You expand the branches you care about and leave the rest
folded. Navigation (`↑`/`↓`) walks only the *visible* rows, so a
collapsed folder's contents are skipped entirely.

- `→` / `l` — expand the folder under the cursor; on an already-expanded
  folder, step into its first child; on a file row, move focus to the
  preview pane.
- `←` / `h` — collapse the folder under the cursor; on a file or a
  collapsed folder, jump out to the enclosing parent folder header. From
  the preview pane, `←` returns focus to the list.

Collapsing a folder while one of its descendants is selected moves the
cursor up to that folder's header, so the selection never lands on a
hidden row. Folder headers carry a fold glyph: `▸` collapsed, `▾`
expanded. The default is collapsed; pass `ccmux --expand-notes` (or set
`[notes] expand_folders = true` in `config.toml`) to open the tree fully
expanded instead. A search (`/`) shows a flat result list independent of
fold state; clearing it restores the tree.

Two-pane layout (folders collapsed on open):

```
┌───── ccmux ◀ ────────────┐  ┌──── README.md ──────────────────────────┐
│ ▌ README.md             │  │                                          │
│   CLAUDE.md             │  │  # ccmux                                 │
│   ▸ docs/               │  │                                          │
│   ▸ openspec/           │  │  A TUI for Claude Code session…          │
│                         │  │                                          │
└──────────────────────────┘  └──────────────────────────────────────────┘
  ↑↓: navigate  →/←: expand/collapse  n: new  e: edit  /: search  H: device
```

Rendering: Glamour with the active theme. Wikilinks (`[[foo]]`) and markdown links resolve within the project tree. The renderer caches output keyed by `(path, mtime)` so re-opening a note is instant.

## Actions

| Action | Result |
|---|---|
| `→` / `l` | Expand the folder under the cursor (or drill into an expanded one); on a file, focus the preview. |
| `←` / `h` | Collapse the folder under the cursor (or jump out to its parent header); from the preview, return to the list. |
| `e` | Edit the selected file in `$EDITOR` (nvim, vim, helix, code). Local notes only. |
| `/` | Full-text search across every markdown file in the project. Ripgrep when available; routed to the active device. |
| `H` | Toggle which **device** you're viewing notes from (local → each reachable tailnet peer → back). |
| `o` | Open in Obsidian via `obsidian://` URI (macOS only; hidden if Obsidian not installed). |

ccmux **browses, renders, and searches** notes — it does not create or
template them. Earlier versions had `n`-then-`a/s/d` quick-actions that
scaffolded `docs/01_Specs/`, `docs/02_Architecture/`, and
`docs/03_Agent_Logs/` files; those were removed along with project
scaffolding. Writing notes — whatever shape and convention you want —
is the user's (or their agent's) job. The `docs/` convention is still
honored by the listing if you use it; it's just no longer imposed.

## Cross-Device Access

Notes live on whichever machine holds the project, but you can read any
device's notes from anywhere on your tailnet — that's the "context that
follows you" promise. The Notes screen's device toggle (`H`) cycles the
active device through the local machine and every reachable ccmuxd peer
(configured hosts + auto-discovered tailnet peers). Switching device
re-scopes the project picker, the note list, the preview, and search to
that device's daemon. The active device is shown in the screen header
(`● <device>`); an unreachable peer surfaces an explicit error rather
than silently falling back to local notes.

Remote access is **read-only**: the preview renders remote markdown, but
`e` (edit) and `n` (new note) are disabled for a remote device because
the file lives on another machine's disk. Create/edit happens on the
device that owns the project.

### Daemon API

ccmuxd serves the notes vault over its tailnet-bound HTTP API (the same
`100.x.x.x:7474` listener used elsewhere). The client (`internal/daemon.Client`)
exposes `Notes`, `NoteContent`, and `SearchNotes`, each working against
the local Unix socket or a remote peer transparently:

| Endpoint | Purpose |
|---|---|
| `GET /v1/notes?project=<name>` | List the project's markdown files. |
| `GET /v1/notes?project=<name>&file=<rel>` | Read one file's body (path-traversal validated). |
| `GET /v1/notes/search?project=<name>&q=<query>` | Search the project's vault (ripgrep-backed). |

`project` is resolved against the daemon's configured projects root, so a
caller can only ever reference projects ccmux already lists.

### CLI

The same access is scriptable via `ccmux notes`:

```bash
ccmux notes list  <project> [--host <name>]     # list a vault's files
ccmux notes read  <project> <file> [--host <name>]   # print one note
ccmux notes search <project> <query> [--host <name>] # search the vault
```

Without `--host` the command targets the local device; `--host <name>`
selects a configured host (a typo'd name is an error, never a silent
fallback to local).

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
