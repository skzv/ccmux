# ccmuxd HTTP API

`ccmuxd` (the ccmux daemon) exposes a small HTTP + JSON API. It's how the
ccmux TUI talks to a remote machine over Tailscale, and it's the
integration surface for **mobile clients** (e.g. the [Moshi](https://getmoshi.app/)
app) that want to list, attach to, and act on agent sessions without
shelling in.

This document is the contract. It is generated from — and kept in sync
with — `cmd/ccmuxd/main.go` (routes), `cmd/ccmuxd/pairing.go`, and
`internal/daemon/protocol.go` (the request/response types). If you're
integrating against it and something here disagrees with the running
daemon, the daemon wins — please file an issue.

---

## Transport & reachability

The same routes are served over two transports:

| Transport | Address | Default | Notes |
|---|---|---|---|
| **Local Unix socket** | `~/.local/state/ccmux/ccmuxd.sock` (mode `0600`) | always on | Full surface, including the local-only pairing-token mint. |
| **Tailnet HTTP** | `http://<tailnet-ipv4>:<port>` (port default `7474`) | **off** | Enable with `daemon.listen_tailnet = true`; bind addr is the host's `tailscale ip -4`. |

To reach a daemon from another device you must, on the **daemon** host:

1. set `daemon.listen_tailnet = true` (optionally `daemon.tailnet_port`) in
   `~/.config/ccmux/config.toml`, and
2. restart ccmuxd (`ccmux daemon restart`, or it picks it up on next launch).

If Tailscale isn't running, the tailnet listener silently doesn't start
(logged, not fatal) and only the Unix socket is served.

- **All paths are under `/v1/`.** There is no `/v2`.
- Request and response bodies are `application/json` — **except** the SSE
  endpoint (`text/event-stream`) and the attach endpoint (a WebSocket
  upgrade).
- **No TLS.** Tailscale's WireGuard tunnel is the encryption + identity
  boundary; the HTTP listener is plain HTTP bound to the tailnet IP.
- Request bodies are capped at **64 KiB**; larger bodies fail to decode
  (`400`).

---

## Authentication & trust model — read this first

**There is no application-level authentication on the HTTP API.** No bearer
token, no API key, no per-request signature, no IP allowlist. The trust
boundary is **your Tailscale tailnet**: any host that can reach
`100.x.x.x:7474` can call every endpoint — list / create / kill / rename /
send-keys, **attach to a full interactive terminal**, and read
notes / conversations / usage.

> Secure this with **Tailscale ACLs**, not app-level auth. Treat anything
> that can route to the daemon's tailnet IP as fully trusted.

Two endpoints have per-request validation, and they exist **only to
bootstrap device trust + push** — not to protect the API:

- `POST /v1/pair` requires a valid, unexpired, single-use pairing token
  **plus** a parseable SSH public key. Redeeming it installs that key into
  `~/.ssh/authorized_keys` (so the device can `ssh`/`mosh` in).
- `POST /v1/pair-token` is **Unix-socket only** — it is never registered on
  the tailnet listener — and additionally returns `503` unless
  `listen_tailnet` is on. This is what stops a tailnet peer from minting
  itself a pairing token.

---

## Error shape

Errors are **plain text** (`Content-Type: text/plain; charset=utf-8`): a
single-line message via Go's `http.Error`, **not** a JSON envelope. So:

- check the **status code**, and
- read the body as a plain string for the human-readable message.

Successful JSON responses are `application/json`.

Status conventions across the API:

| Code | Meaning |
|---|---|
| `200` | OK (JSON body) |
| `204` | OK, no body (kill / send-keys / device register) |
| `400` | bad input / validation failure |
| `401` | invalid or expired pairing token (`/v1/pair` only) |
| `404` | not found — or **this daemon predates the endpoint** |
| `405` | wrong HTTP method |
| `500` | server / tmux / scaffold failure |
| `503` | feature unavailable (e.g. `/v1/pair-token` with `listen_tailnet` off) |

> **Forward/backward compatibility:** the API evolves by *adding* JSON
> fields (all optional fields use `omitempty`) and *adding* routes. Treat a
> `404` on a whole endpoint as "this daemon is older than that feature" and
> degrade gracefully (the official client does exactly this for
> `/v1/notes/search`).

---

## Endpoints

### Health & discovery

#### `GET /v1/health`
Liveness + identity probe. Used to decide whether a daemon is reachable and
to read its hostname / version / session count / sleep mode.
- **Request:** none.
- **Response `200`:** `HealthInfo`.

```json
{ "ok": true, "hostname": "mac-mini", "version": "v0.1.23", "sessions": 3, "sleep_mode": "safe" }
```
`sleep_mode` ∈ `off | safe | dangerous | very_dangerous`.

#### `GET /v1/peers`
Every tailnet peer plus whether each one runs ccmuxd. Powers an "add host"
picker without the client needing the `tailscale` CLI.
- **Request:** none.
- **Response `200`:** `[]PeerInfo`. Returns `[]` (not `500`) when Tailscale
  is absent. ccmuxd probes each peer's `/v1/health` in parallel with a ~1s
  deadline.

```json
[ { "hostname": "mac-mini", "addr": "100.75.64.20", "os": "macOS", "online": true, "runs_ccmuxd": true, "port": 7474 } ]
```

---

### Sessions

#### `GET /v1/sessions`
List every tmux session this daemon manages, with daemon-derived state.
- **Response `200`:** `[]SessionState` (see Types). `host` is always
  `"local"` from the daemon's own perspective; `state` ∈
  `active | idle | needs_input | error | unknown`.

#### `POST /v1/sessions`
Create-or-attach a **project-bound** agent session (idempotent on the tmux
session name). Persists the chosen agent to `<project>/.ccmux/agent`.
- **Request:** `NewSessionRequest` — `project` required.
- **Response `200`:** `SessionState` for the created/existing session.
- **Errors:** `400` missing `project` / bad name / decode error; `404`
  project path not found; `500` tmux failure.
- `path` defaults to `<projects_root>/<project>` **on the daemon host**.

#### `POST /v1/sessions/bare`
Create a **shell-only** tmux session not tied to any project (no scaffold).
- **Request:** `NewBareSessionRequest`.
- **Response `200`:** `NewBareSessionResponse`.
- `path` empty resolves to `sessions.default_dir` or `$HOME` **on the daemon
  host** (never the client's home). `agent` empty falls back to
  `sessions.default_agent` then `$SHELL`; `"shell"` means no agent.

#### `POST /v1/sessions/{name}/kill`
Kill a session by name. Emits a `killed` SSE event.
- **Request:** none. **Response:** `204`. **Errors:** `400` missing name;
  `500` tmux failure.

#### `POST /v1/sessions/{name}/rename`
Rename a session. `{name}` is the **current** name; the body carries the new
one.
- **Request:** `RenameRequest`. **Response `200`:** `SessionState`
  (`{name: <newName>, host: "local"}`).

#### `POST /v1/sessions/{name}/send-keys`
Send raw keystrokes/text into the session's active pane (e.g. type a reply +
Enter). Passed through to `tmux send-keys`.
- **Request:** `SendKeysRequest`. **Response:** `204`.

#### `GET /v1/sessions/{name}/preview`
Last N lines of the active pane as plain text (ANSI stripped). A lightweight
"peek" without opening the attach socket.
- **Query:** `?lines=N` (`1..200`, default `24`).
- **Response `200`:** `PreviewResponse`. `404` if the session doesn't exist.

#### `GET /v1/sessions/{name}/attach` — WebSocket
Upgrade to a WebSocket bridged to a real `tmux attach-session` in a PTY: a
true interactive terminal (live output, input, resize). This is how a mobile
client gives a full terminal **without** ssh/mosh.
- **Upgrade:** `GET` → `101 Switching Protocols`.
- **After upgrade** (uses `github.com/coder/websocket` framing):
  - **client → server, binary frame:** raw stdin bytes (keystrokes).
  - **client → server, text frame:** JSON `{"cols":N,"rows":N}` to resize.
  - **server → client, binary frame:** raw PTY output bytes.
- Initial PTY size is `80x24` until the first resize frame.
- The server pings every 25s with a 10s deadline; answer pongs or expect a
  teardown.
- **`InsecureSkipVerify` is set** (no `Origin` check) — again, the tailnet is
  the trust boundary and native clients send no `Origin`.
- Closing the socket only **detaches**; the tmux session keeps running.

> For an interactive terminal, prefer this over polling `/preview`.

---

### Projects

#### `GET /v1/projects`
Projects discovered under the daemon's configured projects root, tagged with
the daemon's hostname.
- **Response `200`:** `[]ProjectInfo`.

#### `POST /v1/projects`
Create a brand-new project (**directory only** — no `CLAUDE.md`/`docs/`/git)
under the projects root, and start an agent session inside it.
- **Request:** `NewProjectRequest` — `name` required.
- **Response `200`:** `NewProjectResponse`.
- **Errors:** `400` if `name` isn't a single non-hidden path segment (no
  `/`, `\`, no leading `.`) — a directory-escape guard for tailnet peers.

---

### Conversations, usage, notes

#### `GET /v1/conversations`
Past agent transcripts (Claude / Codex / Cursor / Antigravity) in the
daemon's home dir, most-recent first. Headless/SDK runs excluded.
- **Response `200`:** `[]Conversation`. `id` is the agent's own UUID (what
  you'd pass to its `--resume`).

#### `GET /v1/usage`
Per-agent token + cost activity over a rolling window.
- **Query:** `?window=<Go duration>` e.g. `2h`, `24h`, `30m` (default `5h`).
- **Response `200`:** `AgentUsage`. Best-effort per agent; `estimated_cost`
  is USD at published API rates.

#### `GET /v1/notes`
List a project's markdown vault, or (with `&file=`) read one file.
- **Query:** `?project=<name>` required; optional `&file=<project-relative
  path>` (must be a `.md` file, no `..`, not absolute).
- **Response `200`:** list mode → `[]NoteEntry`; file mode → `NoteContent`.

#### `GET /v1/notes/search`
Ripgrep-backed search across a project's markdown vault.
- **Query:** `?project=<name>&q=<query>` (both required).
- **Response `200`:** `[]SearchHit`. (Older daemons return `404` → treat as
  "search unavailable.")

---

### Models

#### `GET /v1/models`
Returns the Claude model catalog the daemon has discovered, merged with a
curated in-binary fallback list. Drives the model picker in the TUI and
`ccmux agents models` on the CLI; useful to integrators who want to surface
the same set without re-deriving it. Refreshes from Anthropic's
[Models API](https://platform.claude.com/docs/en/api/models-list) every 24h
in the background when `ANTHROPIC_API_KEY` is set on the daemon's environment;
without a key the response is the curated list (`source: "fallback"`).
- **Query:** `?refresh=true` forces a synchronous re-fetch before responding.
  Returns the cached catalog on refresh failure.
- **Response `200`:** `Catalog`.

```json
{
  "models": [
    {
      "id": "claude-opus-4-8",
      "display_name": "Claude Opus 4.8",
      "family": "opus",
      "max_input_tokens": 1000000,
      "max_tokens": 128000,
      "capabilities": {
        "vision": true,
        "thinking_adaptive": true,
        "structured_outputs": true,
        "effort_max": true
      },
      "source": "api"
    }
  ],
  "fetched_at": "2026-06-12T05:24:44Z",
  "source": "api"
}
```

`source` (per-model and on the envelope) ∈ `api | fallback`. Live entries
override curated ones on ID match; curated entries fill gaps for models the
caller's account can't list. `family` is derived from the ID (`opus | sonnet
| haiku`, or empty for unrecognised IDs) — purely for grouping in pickers.

---

### Live updates

#### `GET /v1/events` — Server-Sent Events
Stream of session lifecycle/state events; subscribe to live-update a view.
- **Response `200`:** `text/event-stream`. Each `data:` frame is a JSON
  `SessionEvent`; `kind` ∈ `created | killed | state_change | needs_input`.
- Heartbeats: `: connected` on open, `: ping` comment every 20s — comment
  lines (leading `:`) are ignorable.
- If the per-subscriber buffer (256) overflows you get an
  `event: drops` / `data: <n>` frame; that means you missed `n` events and
  should re-fetch `/v1/sessions` to resync.

---

### Pairing & push (mobile)

This is the optional flow for **native push notifications** (APNs on iOS,
FCM on Android) and for installing an SSH key so the device can `ssh`/`mosh`
attach. It is **not** required to use the read/act endpoints above over the
tailnet.

#### `POST /v1/pair-token` — Unix-socket only
Mint a one-time pairing token + a `ccmux://` deep link for the phone to
redeem. **Never registered on the tailnet listener.**
- **Request:** none (empty `POST`).
- **Response `200`:** `PairTokenResponse` — `token` (128-bit hex, single-use,
  5-min TTL) and `url` = `ccmux://pair?host=…&user=…&port=…&token=…`.
- **Errors:** `503` if `listen_tailnet` is off.

#### `POST /v1/pair`
Redeem a pairing token: install the device's SSH public key into
`~/.ssh/authorized_keys`, optionally registering a push token inline.
- **Request:** `PairRequest`.
- **Response `200`:** `PairResponse` (`hostname`, `version`).
- **Errors:** `400` unparseable/multi-line public key or pre-key options;
  `401` invalid/expired token; `500` failed to write `authorized_keys`.
- The key is validated **before** the token is consumed (a bad key doesn't
  burn the token) and canonicalised (comments/options stripped). This
  endpoint **is** reachable on the tailnet.

#### `POST /v1/devices`
Register/refresh a push token on an already-paired host (after the user
grants notifications, or the OS rotates the token).
- **Request:** `RegisterDeviceRequest`.
- **Response:** `204`.
- The device is identified by the SSH `public_key` it paired with (stored
  only as a SHA-256 hash). `provider` ∈ `apns` (default) | `fcm`; APNs
  requires `env` ∈ `development | production`, FCM requires empty `env`.

#### `POST /v1/devices/test`
Send a verification push to the device registered for a given SSH public key.
- **Request:** `{ "public_key": "ssh-ed25519 AAAA…" }`.
- **Response:** `204` (also `204` when APNs is disabled, so the UI can
  honestly say "sent"; the real status is in ccmuxd's log). `404` if no
  device is registered for that key.

**Push behavior:** pushes fire on two transitions — a session entering
`needs_input`, and `active → idle` ("agent finished"). The push's session id
is `local/<sessionName>`. Both APNs and FCM are **off by default** and need
server-side config (`[apns]` / `[fcm]` in `config.toml`). FCM routing exists
but real Android delivery isn't wired yet.

---

## Types

Copied from `internal/daemon/protocol.go` (Go structs with their JSON tags).

```go
// GET /v1/sessions item; POST /v1/sessions response.
type SessionState struct {
	Name        string    `json:"name"`
	Host        string    `json:"host"`         // "local" or a remote host name
	Project     string    `json:"project"`      // project basename if known
	Path        string    `json:"path"`         // session working directory
	State       string    `json:"state"`        // active|idle|needs_input|error|unknown
	Attached    bool      `json:"attached"`     // any tmux client attached
	Windows     int       `json:"windows"`
	Created     time.Time `json:"created"`
	LastChange  time.Time `json:"last_change"`  // pane content last changed
	PromptCount int       `json:"prompt_count"` // # needs-input transitions seen
	Agent       string    `json:"agent,omitempty"`
}

// GET /v1/health
type HealthInfo struct {
	OK        bool   `json:"ok"`
	Hostname  string `json:"hostname"`
	Version   string `json:"version"`
	Sessions  int    `json:"sessions"`
	SleepMode string `json:"sleep_mode"` // off|safe|dangerous|very_dangerous
}

// GET /v1/events frame
type SessionEvent struct {
	At      time.Time    `json:"at"`
	Kind    string       `json:"kind"` // state_change|created|killed|needs_input
	Session SessionState `json:"session"`
}

// POST /v1/sessions
type NewSessionRequest struct {
	Project  string `json:"project"`
	Path     string `json:"path"`     // defaults to <projects_root>/<project>
	Continue bool   `json:"continue"` // start the agent with --continue
	Name     string `json:"name,omitempty"`
	Agent    string `json:"agent,omitempty"`
}

// POST /v1/sessions/bare
type NewBareSessionRequest struct {
	Name  string `json:"name,omitempty"`
	Path  string `json:"path,omitempty"`
	Agent string `json:"agent,omitempty"`
}
type NewBareSessionResponse struct {
	Session string `json:"session"`
	Path    string `json:"path"`
	Host    string `json:"host"`
}

// POST /v1/projects
type NewProjectRequest struct {
	Name  string `json:"name"`
	Agent string `json:"agent,omitempty"`
}
type NewProjectResponse struct {
	Session string `json:"session"`
	Path    string `json:"path"`
	Host    string `json:"host"`
}

// GET /v1/projects item
type ProjectInfo struct {
	Name      string    `json:"name"`
	Host      string    `json:"host"`
	Path      string    `json:"path"` // absolute on the daemon host
	HasGit    bool      `json:"has_git"`
	HasCM     bool      `json:"has_cm"`
	HasAgents bool      `json:"has_agents,omitempty"`
	HasDocs   bool      `json:"has_docs"`
	Agent     string    `json:"agent,omitempty"`
	Modified  time.Time `json:"modified"`
}

// GET /v1/peers item
type PeerInfo struct {
	Hostname   string `json:"hostname"`
	Addr       string `json:"addr"` // tailnet IPv4
	OS         string `json:"os"`
	Online     bool   `json:"online"`
	RunsCCMuxd bool   `json:"runs_ccmuxd"`
	Port       *int   `json:"port,omitempty"` // ccmuxd HTTP port; nil if not probed
}

// GET /v1/conversations item
type Conversation struct {
	ID       string    `json:"id"` // agent UUID (its --resume id)
	Agent    string    `json:"agent"`
	Project  string    `json:"project,omitempty"`
	Path     string    `json:"path,omitempty"`
	Preview  string    `json:"preview,omitempty"` // first user message
	Modified time.Time `json:"modified"`
}

// GET /v1/usage
type AgentUsage struct {
	Claude      UsageSummary `json:"claude"`
	Codex       UsageSummary `json:"codex"`
	Antigravity UsageSummary `json:"antigravity"`
}
type UsageSummary struct {
	HasData       bool    `json:"has_data"`
	WindowSeconds int     `json:"window_seconds"`
	Prompts       int     `json:"prompts"`
	InputTokens   int     `json:"input_tokens"`
	OutputTokens  int     `json:"output_tokens"`
	EstimatedCost float64 `json:"estimated_cost"` // USD
}

// GET /v1/notes (list) / (&file= -> content) / /v1/notes/search
type NoteEntry struct {
	Rel      string    `json:"rel"`
	Dir      string    `json:"dir"`
	Display  string    `json:"display"`
	Modified time.Time `json:"modified"`
}
type NoteContent struct {
	Rel     string `json:"rel"`
	Content string `json:"content"`
}
type SearchHit struct {
	Rel     string `json:"rel"`
	LineNum int    `json:"line_num"`
	Snippet string `json:"snippet"`
}

// GET /v1/sessions/{name}/preview
type PreviewResponse struct {
	Lines   int    `json:"lines"`
	Content string `json:"content"`
}

// POST /v1/sessions/{name}/rename | /send-keys
type RenameRequest   struct{ Name string `json:"name"` }
type SendKeysRequest struct{ Keys string `json:"keys"` }

// Pairing & push
type PairRequest struct {
	Token       string `json:"token"`
	PublicKey   string `json:"public_key"`
	DeviceToken string `json:"device_token,omitempty"`
	APNsEnv     string `json:"apns_env,omitempty"` // development|production
}
type PairResponse      struct{ Hostname string `json:"hostname"`; Version string `json:"version"` }
type PairTokenResponse struct{ Token string `json:"token"`; URL string `json:"url"` } // ccmux://pair?...
type RegisterDeviceRequest struct {
	Token     string `json:"token"`
	Env       string `json:"env,omitempty"` // apns: development|production; fcm: empty
	PublicKey string `json:"public_key"`
	Provider  string `json:"provider,omitempty"` // apns (default) | fcm
}
// POST /v1/devices/test body is { "public_key": "..." }
```

---

## Validation rules to mirror client-side

The daemon enforces these (and `400`s on violation); validate before sending
for a better UX:

- **tmux session names** must not contain `/`, `\`, or `:` (a tmux
  target-spec injection guard).
- **project names** for `POST /v1/projects` must be a single non-hidden path
  segment — no `/`, `\`, no leading `.`.
- **notes file paths** must be project-relative, contain no `..`, and end in
  `.md`.
- request bodies are capped at **64 KiB**.

---

## Quick examples

```bash
# Health (over the tailnet)
curl -s http://100.75.64.20:7474/v1/health | jq

# List sessions
curl -s http://100.75.64.20:7474/v1/sessions | jq

# Type a reply into a session and press Enter
curl -s -X POST http://100.75.64.20:7474/v1/sessions/c-auth/send-keys \
  -H 'content-type: application/json' \
  -d '{"keys":"yes, ship it\n"}'

# Peek at the last 40 lines of a pane
curl -s 'http://100.75.64.20:7474/v1/sessions/c-auth/preview?lines=40' | jq -r .content

# Subscribe to live events
curl -sN http://100.75.64.20:7474/v1/events
```

The Go reference client in `internal/daemon/client.go` is the canonical
consumer and a useful map of method → endpoint.
