## ADDED Requirements

### Requirement: Daemon serves note search over the tailnet

The daemon SHALL expose `GET /v1/notes/search?project=<name>&q=<query>` that returns search hits from the named project's notes vault, mirroring the result of `notes.Vault.Search`. The endpoint SHALL validate the `project` parameter against the daemon's known projects and reject requests for unknown projects.

#### Scenario: Search a remote project's notes

- **WHEN** a client issues `GET /v1/notes/search?project=ccmux&q=tailscale` against a reachable daemon
- **THEN** the daemon responds with a JSON array of search hits (each with relative path, line number, and snippet) drawn from that project's markdown tree

#### Scenario: Search rejects unknown project

- **WHEN** a client searches with a `project` that the daemon does not know
- **THEN** the daemon responds with a 404 status and does not read the filesystem

### Requirement: Client can read notes from any reachable device

The daemon `Client` SHALL provide `Notes(ctx, project)`, `NoteContent(ctx, project, rel)`, and `SearchNotes(ctx, project, query)` methods that target the client's configured endpoint (local Unix socket or a remote tailnet address). These methods SHALL work against both the local daemon and a remote daemon without code changes at the call site.

#### Scenario: List notes on a remote device

- **WHEN** code calls `RemoteClient(addr).Notes(ctx, "ccmux")` for a reachable remote daemon
- **THEN** the call returns the list of note entries from that device's `ccmux` project vault

#### Scenario: Read a note body from a remote device

- **WHEN** code calls `RemoteClient(addr).NoteContent(ctx, "ccmux", "docs/Design.md")`
- **THEN** the call returns the markdown content of that file on the remote device

#### Scenario: Path traversal is rejected

- **WHEN** a client requests note content with a relative path that escapes the project root (e.g. `../../etc/passwd`)
- **THEN** the daemon rejects the request and returns no file content

### Requirement: TUI Notes screen has a device toggle

The TUI Notes screen SHALL provide a keybinding that cycles the active device among the set of reachable devices: the local machine, each configured host, and each discovered tailnet peer. The set of devices SHALL be populated from the same `hostStatus` data the Sessions and Projects screens use. The active device SHALL be indicated in the Notes screen header.

#### Scenario: Cycle to the next device

- **WHEN** the user presses the device-toggle key on the Notes screen with more than one reachable device
- **THEN** the active device advances to the next device and the header updates to name it

#### Scenario: Toggle is a no-op with one device

- **WHEN** only the local device is reachable and the user presses the device-toggle key
- **THEN** the active device remains local and the screen does not error

#### Scenario: Unreachable device is skipped or surfaced

- **WHEN** the active device is a remote device that cannot be reached
- **THEN** the Notes screen shows an error/empty state for that device rather than falling back silently to local notes

### Requirement: Notes screen scopes content to the active device

When the active device is local, the Notes screen SHALL load the project list, note list, preview, and search results from the local vault. When the active device is remote, it SHALL load all four from that device's daemon via the client methods. Switching the active device SHALL reload the project list and note list for the newly-selected device.

#### Scenario: Switching device reloads notes

- **WHEN** the user toggles from the local device to a remote device
- **THEN** the project picker and note list repopulate with the remote device's projects and notes, and the preview clears until a remote note is selected

#### Scenario: Preview and search follow the active device

- **WHEN** the active device is remote and the user opens a note or runs a search
- **THEN** the preview content and search results come from the remote device's daemon, not the local vault

### Requirement: CLI exposes cross-device note access

The CLI SHALL provide a `ccmux notes` command with `list`, `read`, and `search` subcommands. Each SHALL accept a `--host <name>` flag selecting a configured host; when omitted, the command operates on the local device. Output SHALL be plain text suitable for scripting.

#### Scenario: List notes on a host

- **WHEN** the user runs `ccmux notes list ccmux --host laptop`
- **THEN** the command prints the note entries from the `ccmux` project on the `laptop` host

#### Scenario: Read a note on a host

- **WHEN** the user runs `ccmux notes read ccmux docs/Design.md --host laptop`
- **THEN** the command prints the markdown body of that file from the `laptop` host

#### Scenario: Default to local device

- **WHEN** the user runs `ccmux notes list ccmux` with no `--host`
- **THEN** the command reads notes from the local daemon
