## Context

ccmux's pitch is "Obsidian-backed project context that follows you." Today it does not: the TUI Notes screen (`internal/tui/notes.go`) and any CLI path read only the local `notes.Vault`. The daemon, however, already serves notes over the tailnet — `GET /v1/notes?project=<name>` (list) and `GET /v1/notes?project=<name>&file=<rel>` (read) are implemented in `cmd/ccmuxd/main.go:handleNotes`, with `daemon.NoteEntry`/`daemon.NoteContent` defined in `protocol.go`. What is missing is (1) client-side methods to call those endpoints, (2) a search endpoint (search is local-only today), and (3) a TUI affordance to choose which device's notes you are looking at.

The multi-host plumbing this needs already exists. Sessions and Projects fan out across `cfg.Hosts` plus discovered tailnet peers and tag results by origin (`internal/tui/app.go` ~1840–2149), driven by the `hostStatus` list (`internal/tui/messages.go`). `project.Project` already carries a `Host` field. The daemon `Client` already memoizes a `LocalClient()` singleton and per-address `RemoteClient(addr)` instances (`internal/daemon/client.go`), with a `getJSON` helper. This change is mostly wiring an existing API into the Notes UI and CLI, plus one new search endpoint.

## Goals / Non-Goals

**Goals:**
- Read and list any reachable device's project notes from the TUI and CLI.
- Search notes on a remote device (parity with local search).
- A TUI device toggle that re-scopes the entire Notes screen (project picker, list, preview, search) to a chosen device, with the active device shown in the header.
- Reuse the existing `hostStatus` / `RemoteClient` / `getJSON` infrastructure — no new transport, discovery, or auth.

**Non-Goals:**
- Writing/creating/editing notes on a remote device. Remote access stays read-only (matches the current `/v1/notes` contract); note creation remains local-only.
- Syncing or caching notes for offline use. Each view is a live fetch.
- Conflict resolution or a unified "all devices merged" notes view. The toggle shows one device at a time.
- Changing the on-disk notes model or the local `notes.Vault` API.

## Decisions

### Decision: Add client methods over the existing endpoints rather than a new abstraction layer
Add `Client.Notes(ctx, project) ([]NoteEntry, error)`, `Client.NoteContent(ctx, project, rel) (NoteContent, error)`, and `Client.SearchNotes(ctx, project, q) ([]SearchHit, error)` in `daemon/client.go`, each built on the existing `getJSON` helper with a query-string path. `getJSON` already takes a path string, so query params append cleanly (`/v1/notes?project=...`), with proper URL-escaping of `project`/`file`/`q`.
- *Rationale:* The endpoints and protocol structs already exist; the call sites in Sessions/Projects show this is the established pattern. A single set of methods works for both local and remote because the `Client` already encapsulates the endpoint.
- *Alternative considered:* A higher-level `notes`-package "remote vault" type implementing the same interface as `notes.Vault`. Rejected for v1 — it adds an abstraction the TUI doesn't need yet; the TUI can branch on local-vs-remote at the load site.

### Decision: Add a `/v1/notes/search` endpoint backed by `notes.Vault.Search`
The daemon gains `GET /v1/notes/search?project=<name>&q=<query>`, validating `project` against known projects (same validation `handleNotes` uses) and returning `[]daemon.SearchHit` from `vault.Search`.
- *Rationale:* Without it, the device toggle would silently disable search on remote devices — a confusing partial feature. Search is a first-class Notes affordance (`/` key).
- *Alternative considered:* Fetch all note bodies to the client and search client-side. Rejected — wasteful over the network and duplicates the ripgrep logic the daemon already has.

### Decision: Device selection is screen-level state, toggled by a keybinding, sourced from `hostStatus`
`notesModel` gains an `activeDevice` (index/identifier into a `[]hostStatus`) and a `SetHosts([]hostStatus)` method, populated from `app.go` on `sessionsLoadedMsg` exactly like the Sessions/Projects models. A new keybinding (proposed `H`, "host") cycles `activeDevice`. The active device name renders in the screen header. Loaders (`loadEntriesCmd`, `refreshPreview`, search command) branch: local → `notes.Vault`; remote → `RemoteClient(addr).Notes/NoteContent/SearchNotes`.
- *Rationale:* Mirrors the device picker already used by the Sessions new-session form; users get a consistent mental model. Keeping it screen-level (not global app state) means toggling devices on Notes doesn't disturb other screens.
- *Alternative considered:* Derive the device purely from the selected project's `Host`. Rejected as the *primary* mechanism — the user explicitly asked to *toggle* the viewing device independently. (We still honor `project.Host` when a remote project is opened, but the toggle is the user-facing control.)

### Decision: Render remote `NoteEntry`/`NoteContent` through the same view code as local entries
Add a tiny adapter (in `internal/notes` or local to the Notes model) converting `daemon.NoteEntry` → the fields the list renderer needs and `daemon.NoteContent` → preview bytes, so the Glamour render path and list rows are identical regardless of source.
- *Rationale:* One rendering path; remote notes look exactly like local ones. Avoids duplicating the list/preview UI.

### Decision: CLI `ccmux notes` mirrors the TUI capability for scripting and the feature-surface policy
New `cmd/ccmux/cmd/notes.go` with `list`/`read`/`search` subcommands and a `--host` flag resolving to a configured `config.Host` (reusing the same address resolution `ccmux attach`/`list` use). No `--host` → `LocalClient()`.
- *Rationale:* CLAUDE.md requires every feature be reachable from both TUI and CLI. The CLI also gives a test seam that doesn't need a running Bubble Tea program.

## Risks / Trade-offs

- **[Remote device unreachable mid-session]** → The loader returns an error; the Notes screen renders an explicit error/empty state for that device (per spec) rather than silently falling back to local, which would mislead the user about whose notes they see.
- **[Latency on remote list/preview/search]** → Loads already run off the UI thread via `tea.Cmd`; show a loading indicator. Network fetches are inherently slower than disk; acceptable for an interactive browse.
- **[Project-name collisions across devices]** → A project named `ccmux` may exist on several devices with different content. Mitigated because the toggle scopes to exactly one device at a time and the header names it; no merged view to confuse origins.
- **[Search endpoint exposes filesystem]** → Reuse `handleNotes`' existing project validation and path discipline; search only operates within a validated project's vault, and the query never becomes a path.
- **[Read-only asymmetry]** → Users may expect to create a note on the active remote device. Out of scope; the new-note action stays disabled/hidden when the active device is remote, with a hint, to avoid a confusing failure.

## Migration Plan

Purely additive; no data migration. The daemon must be updated on a device before that device's notes can be searched remotely (list/read already work on current daemons). Rollout: ship the daemon `/v1/notes/search` handler and client methods together; older daemons simply 404 on the search path, which the client surfaces as "search unavailable on this device." No rollback concerns — disabling the feature is removing the toggle binding; nothing persists.

## Open Questions

- Final keybinding for the device toggle (`H` proposed; confirm no conflict with existing Notes bindings).
- Whether to also honor `project.Host` automatically when a remote project is opened from the Projects screen, or require an explicit toggle in all cases. (Leaning: honor it, with the toggle able to override.)
