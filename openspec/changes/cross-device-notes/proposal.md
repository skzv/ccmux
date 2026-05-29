## Why

Notes live on whichever machine holds the project, so a note written on the desktop is invisible from the laptop or phone — exactly the "context that follows you" promise ccmux makes. The daemon already exposes a read-only `/v1/notes` HTTP endpoint over the tailnet, but nothing on the client consumes it: the TUI Notes screen and the CLI both read only the local vault. This change closes that gap so you can browse and read any device's notes from anywhere on your tailnet, and switch between devices without leaving the Notes screen.

## What Changes

- Add daemon `Client` methods `Notes(ctx, project)` and `NoteContent(ctx, project, rel)` that call the existing `/v1/notes` endpoint, plus a new `/v1/notes/search` endpoint + `SearchNotes(ctx, project, query)` client method so search works against remote vaults too.
- The TUI Notes screen gains a **device toggle**: a keybinding that cycles through the set of reachable devices (local + configured hosts + discovered tailnet peers). The currently-selected device scopes the project picker, the note list, the preview, and search to that device's daemon. The active device is shown in the screen header.
- The Notes screen loads entries/preview/search from the local vault when the selected device is local, and from the remote daemon (via the new client methods) when it is remote — reusing the same multi-host fan-out and `hostStatus` plumbing the Sessions and Projects screens already use.
- Add a `ccmux notes` CLI subcommand (`list`, `read`, `search`) with a `--host <name>` flag so the same cross-device access is scriptable.
- Docs: update the README + website notes docs and the architecture notes doc to describe cross-device access and the device toggle.

## Capabilities

### New Capabilities
- `cross-device-notes`: Reading, listing, and searching a project's markdown notes on any reachable ccmux device — the daemon API surface, the TUI device toggle that re-scopes the Notes screen to a chosen device, and the `ccmux notes` CLI.

### Modified Capabilities
<!-- No existing capability's spec-level requirements change; this is additive. -->

## Impact

- **internal/daemon**: new `Client.Notes`, `Client.NoteContent`, `Client.SearchNotes` methods (`client.go`); query-string handling for `getJSON`.
- **cmd/ccmuxd**: new `/v1/notes/search` handler reusing `notes.Vault.Search` (`main.go`).
- **internal/tui/notes.go**: device-toggle state, `SetHosts`, keybinding, remote-aware loaders for list/preview/search, header indicator.
- **internal/tui/app.go**: plumb the `hostStatus` list into the Notes model on session-load, same as Sessions/Projects.
- **cmd/ccmux/cmd**: new `notes.go` Cobra subcommand.
- **internal/notes**: small adapter to render `daemon.NoteEntry`/`NoteContent` through the same code path as local `notes.Entry` (no change to the on-disk model).
- Docs: README, website notes MDX, `docs/02_Architecture/01_Notes_System.md`.
- No breaking changes; remote access is read-only and additive.
