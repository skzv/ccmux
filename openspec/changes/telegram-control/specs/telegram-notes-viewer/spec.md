## ADDED Requirements

### Requirement: List and send markdown as a native-rendered document

`/notes [project]` SHALL list a project's markdown vault using the daemon's `Notes` read, and selecting a file SHALL send it as a `.md` document. Because the June-2026 Telegram in-app browser renders `.md` natively, this requires no web server and no Mini App — the document opens rendered. This is the base, always-available viewing path.

#### Scenario: List a vault
- **WHEN** an allowlisted user sends `/notes ccmux`
- **THEN** the bridge returns the project's markdown files (grouped/labeled as the daemon lists them) as selectable choices

#### Scenario: Open a file rendered
- **WHEN** the user selects a listed `.md` file
- **THEN** the bridge sends that file as a document, which the Telegram in-app browser renders as formatted markdown

### Requirement: Search notes

`/notes <project> <query>` SHALL run the daemon's `SearchNotes` and return ranked hits (file, line, snippet) as selectable results that lead to the containing file.

#### Scenario: Search returns hits
- **WHEN** an allowlisted user sends `/notes ccmux tailnet`
- **THEN** the bridge returns matching note locations and lets the user open the file containing a hit

### Requirement: Path safety

File access SHALL be confined to known project vaults. The bridge MUST reject any requested path that escapes the project's vault root (path traversal) or names a file outside a discovered project, returning nothing rather than reading an arbitrary file.

#### Scenario: Traversal is rejected
- **WHEN** a request references a path like `../../etc/passwd` or an absolute path outside any vault
- **THEN** the bridge refuses and reads no file

### Requirement: Optional tailnet web viewer

When `[telegram].web_viewer = true`, ccmuxd SHALL expose a markdown browser **bound to the host's tailnet address** (the same `100.x` address the daemon's API binds to) and the bot SHALL offer a URL button to open it for whole-vault browsing with working inter-note links. This path is **off by default**, is tailnet-only by virtue of binding to the tailnet interface (it MUST NOT be exposed publicly — no `tailscale funnel`), and requires the viewing device to be on the tailnet. (HTTPS via `tailscale serve` is an optional manual upgrade layered on top; the built-in listener is tailnet-scoped HTTP, which the in-app browser renders.)

#### Scenario: Web viewer disabled by default
- **WHEN** `web_viewer` is unset or false
- **THEN** no viewer listener is started and `/notes` uses only the send-document path

#### Scenario: Web viewer link is tailnet-scoped
- **WHEN** `web_viewer = true` and the user opens the offered link from a device on the tailnet
- **THEN** the browser loads the viewer at the host's tailnet address; the same address is not reachable from the public internet (never funnel)

#### Scenario: Web viewer is confined to project vaults
- **WHEN** the web viewer is enabled
- **THEN** it serves only the same project vaults the bridge exposes, rejects path traversal and non-`.md` paths, and does not become an unauthenticated file server for the host
