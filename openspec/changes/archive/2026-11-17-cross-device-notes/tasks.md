## 1. Daemon: notes search endpoint

- [x] 1.1 Add `GET /v1/notes/search` route in `cmd/ccmuxd/main.go` and a `handleNotesSearch` handler that validates `project` against known projects (reuse the validation from `handleNotes`)
- [x] 1.2 Back the handler with `notes.Vault.Search(ctx, q, limit)` and return JSON `[]daemon.SearchHit`
- [x] 1.3 Add `daemon.SearchHit` to `internal/daemon/protocol.go` (rel path, line number, snippet) if not already present
- [x] 1.4 White-box daemon test: search returns hits for a known project and 404s for an unknown project

## 2. Daemon client methods

- [x] 2.1 Add `Client.Notes(ctx, project) ([]NoteEntry, error)` in `internal/daemon/client.go` using `getJSON` with a URL-escaped `?project=` query
- [x] 2.2 Add `Client.NoteContent(ctx, project, rel) (NoteContent, error)` with escaped `project` + `file` params
- [x] 2.3 Add `Client.SearchNotes(ctx, project, q) ([]SearchHit, error)` hitting `/v1/notes/search`
- [x] 2.4 Confirm `getJSON` handles query strings cleanly; adjust if it assumes a bare path
- [x] 2.5 Fake-daemon test exercising all three methods against an httptest server (local + remote shapes)

## 3. Notes view adapter

- [x] 3.1 Add an adapter converting `daemon.NoteEntry` → the fields the Notes list renderer needs and `daemon.NoteContent` → preview bytes, so local and remote render identically
- [x] 3.2 Unit test the adapter round-trips entry/content fields

## 4. TUI Notes screen: device toggle

- [x] 4.1 Add `activeDevice` state and `hosts []hostStatus` to `notesModel` plus a `SetHosts([]hostStatus)` method (mirror Sessions/Projects)
- [x] 4.2 Add a device-toggle keybinding (`H`) that cycles `activeDevice` across reachable devices; no-op with a single device
- [x] 4.3 Render the active device name in the Notes screen header
- [x] 4.4 Branch `loadEntriesCmd` / project picker population: local vault when active device is local, `RemoteClient(addr).Notes` when remote
- [x] 4.5 Branch `refreshPreview` to use `RemoteClient(addr).NoteContent` when the active device is remote
- [x] 4.6 Branch the search command to use `RemoteClient(addr).SearchNotes` when remote
- [x] 4.7 On device switch, reload project list + note list and clear the preview
- [x] 4.8 Show an explicit error/empty state when the active remote device is unreachable (no silent fallback to local)
- [x] 4.9 Disable/hide the new-note action with a hint when the active device is remote (read-only remote)
- [x] 4.10 Table-driven keymap test for the new toggle binding; teatest golden covering header indicator + device switch

## 5. App plumbing

- [x] 5.1 Pass the `hostStatus` list into `notesM.SetHosts(...)` on `sessionsLoadedMsg` in `internal/tui/app.go` (same place Sessions/Projects get theirs)
- [x] 5.2 When a remote project is opened from the Projects screen, set the Notes active device from `project.Host` (toggle can still override)

## 6. CLI: `ccmux notes`

- [x] 6.1 Create `cmd/ccmux/cmd/notes.go` with parent `notes` command and `--host <name>` flag resolving to a configured `config.Host` (reuse existing host/address resolution)
- [x] 6.2 Implement `notes list <project> [--host]` printing entries via local or remote client
- [x] 6.3 Implement `notes read <project> <rel> [--host]` printing markdown body
- [x] 6.4 Implement `notes search <project> <query> [--host]` printing hits
- [x] 6.5 Register the command in `cmd/ccmux/cmd/root.go`
- [x] 6.6 CLI test: `--host` resolves to the right client; default targets local

## 7. Docs

- [x] 7.1 Update `docs/02_Architecture/01_Notes_System.md` with the cross-device access model and `/v1/notes/search`
- [x] 7.2 Update README + website notes MDX to document the device toggle and `ccmux notes` CLI

## 8. Verification

- [x] 8.1 `make lint` (gofmt + vet + staticcheck), including the TUI styles lint
- [x] 8.2 `go test ./...` green; cross-compile sanity (`GOOS=windows`, `GOOS=linux`) since daemon/client touch OS paths
- [x] 8.3 Manual e2e: two daemons on the tailnet (or two temp HOMEs), toggle device in TUI, confirm list/preview/search read the remote vault; run the `ccmux notes` CLI against both
