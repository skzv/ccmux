// Package daemon contains the IPC protocol between ccmux (client) and
// ccmuxd (server), plus the client implementation. The server lives in
// cmd/ccmuxd. Both the local Unix socket and the optional tailnet HTTP
// listener speak the same JSON schema.
package daemon

import "time"

// SessionState is one row in the dashboard. Includes both tmux-derived
// data (windows, attached, path) and daemon-derived data (state, idle,
// metrics).
type SessionState struct {
	Name        string    `json:"name"`
	Host        string    `json:"host"`         // "local" or a configured remote host name
	Project     string    `json:"project"`      // project basename if known
	Path        string    `json:"path"`         // session's working directory
	State       string    `json:"state"`        // "active" | "idle" | "needs_input" | "error" | "unknown"
	Attached    bool      `json:"attached"`     // any tmux client attached
	Windows     int       `json:"windows"`      // tmux window count
	Created     time.Time `json:"created"`      // session creation time
	LastChange  time.Time `json:"last_change"`  // pane content last changed
	PromptCount int       `json:"prompt_count"` // # of times we've seen a needs-input transition
	// Agent is the AI agent driving this session, sourced from the
	// project's .ccmux/agent sidecar. One of "claude" / "codex" /
	// "antigravity" (or the legacy alias "gemini"). Empty for sessions
	// whose project we couldn't resolve (which the client should treat
	// as claude for back-compat).
	Agent string `json:"agent,omitempty"`
	// Seen reports whether the user has looked at this session since
	// the last state change. False means "the agent did something the
	// user hasn't yet reviewed" — e.g. it transitioned to needs_input
	// or active→idle while nobody was attached. Attaching to the
	// session marks it seen. Drives the dashboard's attention-priority
	// rollup (needs_input > unseen-idle > working > seen-idle > unknown)
	// and the bell/push suppression for the currently-attached session.
	// Defaults to true (a session you've never had output from is by
	// definition reviewed-empty, not unreviewed).
	Seen bool `json:"seen"`
}

// HealthInfo is returned by GET /v1/health. Used by clients to ping
// remote ccmuxd instances and decide if they're alive.
type HealthInfo struct {
	OK       bool   `json:"ok"`
	Hostname string `json:"hostname"`
	Version  string `json:"version"`
	Sessions int    `json:"sessions"`
	// SleepMode is "off" | "safe" | "dangerous" | "very_dangerous".
	SleepMode string `json:"sleep_mode"`
}

// AggregatedMetrics is what GET /v1/metrics returns. Stretch goal for v0.1.
type AggregatedMetrics struct {
	Since      time.Time             `json:"since"`
	PerProject map[string]ProjectAgg `json:"per_project"`
}

// ProjectAgg is one row in the metrics view: how much activity a project
// has seen in the requested window.
type ProjectAgg struct {
	SessionStarts int           `json:"session_starts"`
	PromptCount   int           `json:"prompt_count"`
	ActiveTime    time.Duration `json:"active_time"`
}

// SessionEvent is one frame of the SSE stream from WATCH /v1/events. The
// TUI subscribes and updates its model when frames arrive.
type SessionEvent struct {
	At      time.Time    `json:"at"`
	Kind    string       `json:"kind"` // "state_change" | "created" | "killed" | "needs_input"
	Session SessionState `json:"session"`
}

// NewSessionRequest is the body of POST /v1/sessions.
type NewSessionRequest struct {
	Project  string `json:"project"`  // project name (basename of path)
	Path     string `json:"path"`     // working directory; defaults to ~/Projects/<project>
	Continue bool   `json:"continue"` // start Claude with --continue
	// Name overrides the tmux session name. Empty falls back to the
	// derived c-<project> from tmux.SessionNameForPath.
	Name string `json:"name,omitempty"`
	// Agent picks which AI agent to launch. When set, the daemon writes
	// it to the project's .ccmux/agent sidecar before launching so
	// subsequent attaches pick the same agent. One of "claude" /
	// "codex" / "antigravity" (or the legacy alias "gemini").
	Agent string `json:"agent,omitempty"`
}

// NewBareSessionRequest is the body of POST /v1/sessions/bare. A
// "bare" session is one not tied to a project — no scaffold, no
// description — just a tmux session running the picked agent (or
// $SHELL when Agent is empty) at Path. The Sessions tab's "new
// session" form posts this to either the local daemon (for a
// local-host session) or to a tailnet peer's daemon (cross-device
// session on the Mac mini, say).
//
// Why a separate endpoint from NewSessionRequest: extending the
// existing one would mean an "if Project == ” && !Bare" branch
// everywhere; cleaner to have a small dedicated handler that
// only does the bare case.
type NewBareSessionRequest struct {
	// Name is the bare-tmux session name. Empty → server picks
	// something like `c-shell-<runid>`. The server is the source
	// of truth for naming so concurrent clients on the same daemon
	// don't collide.
	Name string `json:"name,omitempty"`
	// Path is the working directory the session opens in. Empty →
	// resolves to $HOME on the daemon host. We deliberately don't
	// resolve client-side; the home directory of the *remote*
	// machine is what matters when "any device" is the point.
	Path string `json:"path,omitempty"`
	// Agent picks which AI agent the new session launches. One of
	// "claude" / "codex" / "antigravity" (or the legacy alias
	// "gemini"), or the explicit "shell" for no agent. Empty falls
	// back to the daemon's configured sessions.default_agent; if
	// that's also empty / "shell" the daemon spawns $SHELL.
	Agent string `json:"agent,omitempty"`
}

// NewBareSessionResponse is what POST /v1/sessions/bare returns.
// Mirrors NewProjectResponse for symmetry: the client uses Session
// to ssh-attach.
type NewBareSessionResponse struct {
	Session string `json:"session"`
	Path    string `json:"path"`
	Host    string `json:"host"`
}

// NewProjectRequest is the body of POST /v1/projects. Asks the daemon
// to scaffold a brand-new project under its configured Projects.Root
// and start an agent session inside it.
//
// Used by the Projects screen's "n" flow when the user picks a remote
// host: the local TUI calls this over the tailnet, the remote daemon
// creates the directory + starts the session natively, and the TUI
// then attaches over ssh. The daemon creates only the directory — no
// CLAUDE.md, no docs/ tree, no git init.
type NewProjectRequest struct {
	Name string `json:"name"`
	// Agent picks which AI agent the remote daemon launches inside
	// the new session. One of "claude" / "codex" / "antigravity" (the
	// legacy alias "gemini" is also accepted); empty (omitted by older
	// clients) defaults to claude on the daemon side for back-compat.
	Agent string `json:"agent,omitempty"`
}

// NewProjectResponse is what POST /v1/projects returns once the daemon
// created the directory + started the session. Session is the tmux
// session name to attach to; Path is the absolute directory on the
// daemon's host; Host is the daemon's hostname (so the client can show
// "created on <host>" feedback).
type NewProjectResponse struct {
	Session string `json:"session"`
	Path    string `json:"path"`
	Host    string `json:"host"`
}

// ProjectInfo is one entry from GET /v1/projects. The host name is
// filled in by the daemon out of HealthInfo.Hostname so a client
// merging projects from multiple ccmuxds can tag each row with its
// origin. The remaining fields mirror internal/project.Project.
type ProjectInfo struct {
	Name      string    `json:"name"`
	Host      string    `json:"host"`
	Path      string    `json:"path"` // absolute path on the daemon's host
	HasGit    bool      `json:"has_git"`
	HasCM     bool      `json:"has_cm"`
	HasAgents bool      `json:"has_agents,omitempty"`
	HasDocs   bool      `json:"has_docs"`
	Agent     string    `json:"agent,omitempty"`
	Modified  time.Time `json:"modified"`
}

// PeerInfo is one row in the GET /v1/peers response. Maps the daemon's
// internal tailnet scan output to a shape clients (iOS, TUI) can use to
// render "other ccmuxd hosts on your tailnet" pickers without each
// needing tailscale-CLI access. Online peers that didn't respond to a
// ccmuxd probe still show up with RunsCCMuxd=false so the UI can offer
// an "install ccmux there" hint.
type PeerInfo struct {
	Hostname   string `json:"hostname"` // pretty name (Tailscale HostName or MagicDNS short form)
	Addr       string `json:"addr"`     // tailnet IPv4 (e.g. "100.75.64.20")
	OS         string `json:"os"`       // "macOS" | "Linux" | "iOS" | …
	Online     bool   `json:"online"`
	RunsCCMuxd bool   `json:"runs_ccmuxd"`    // ccmuxd /v1/health probe succeeded
	Port       *int   `json:"port,omitempty"` // ccmuxd HTTP port if probed; nil otherwise
}

// NoteEntry is one markdown file in the project's notes vault.
// Returned by GET /v1/notes?project=<name>.
type NoteEntry struct {
	Rel      string    `json:"rel"`      // slash-separated path from the project root
	Dir      string    `json:"dir"`      // "" for a root-level file
	Display  string    `json:"display"`  // human-readable label
	Modified time.Time `json:"modified"` // file mtime
}

// NoteContent is the body of one markdown file.
// Returned by GET /v1/notes?project=<name>&file=<rel>.
type NoteContent struct {
	Rel     string `json:"rel"`
	Content string `json:"content"`
}

// SearchHit is one match from GET /v1/notes/search?project=<name>&q=<query>.
// Mirrors internal/notes.SearchHit minus the absolute on-disk Path, which
// is meaningless to a remote caller.
type SearchHit struct {
	Rel     string `json:"rel"`      // slash-separated path from the project root
	LineNum int    `json:"line_num"` // 1-based matching line number
	Snippet string `json:"snippet"`  // the matching line, trimmed
}

// PreviewResponse is returned by GET /v1/sessions/{name}/preview. The
// Content field is the raw capture-pane output (ANSI sequences stripped
// by tmux) — clients can render it monospaced as-is. Lines is the
// number of trailing lines the daemon was asked to capture, echoed so
// callers can match against ?lines=N.
type PreviewResponse struct {
	Lines   int    `json:"lines"`
	Content string `json:"content"`
}

// UsageSummary is per-agent token + cost activity over a rolling
// window, returned by GET /v1/usage. Drives a mobile client's dashboard
// usage card and any future "what am I spending" surface.
type UsageSummary struct {
	HasData       bool    `json:"has_data"` // false → no transcripts found
	WindowSeconds int     `json:"window_seconds"`
	Prompts       int     `json:"prompts"`
	InputTokens   int     `json:"input_tokens"`
	OutputTokens  int     `json:"output_tokens"`
	EstimatedCost float64 `json:"estimated_cost"` // USD at published API rates
}

// AgentUsage groups the per-agent token summaries plus the OpenRouter
// account spend into one response so a client can render a unified
// "today's activity" card in a single round trip.
type AgentUsage struct {
	Claude      UsageSummary    `json:"claude"`
	Codex       UsageSummary    `json:"codex"`
	Antigravity UsageSummary    `json:"antigravity"`
	OpenRouter  OpenRouterSpend `json:"openrouter"`
	// Others carries per-agent summaries for the second-wave agents
	// (OpenCode, Kimi, …) read via the generic JSONL walker. Only
	// agents with actual usage in the window appear — the list is empty
	// for a user who only runs Claude/Codex. Additive: existing clients
	// that ignore this field keep working.
	Others []OtherUsage `json:"others,omitempty"`
}

// OtherUsage is one second-wave agent's usage row.
type OtherUsage struct {
	Agent string       `json:"agent"`
	Usage UsageSummary `json:"usage"`
}

// OpenRouterSpend is the OpenRouter account spend, returned in
// AgentUsage when the daemon has an OpenRouter key configured. It's a
// different shape from UsageSummary — OpenRouter reports dollars spent
// against the key (not a per-window token count), so a client renders
// it as a "spend / limit" line rather than a token row.
type OpenRouterSpend struct {
	// Enabled is false when no OpenRouter key is configured; clients
	// skip the row entirely in that case. ErrMsg is non-empty when a
	// key IS configured but the fetch failed (bad key, network) so the
	// dashboard can show why instead of a silent blank.
	Enabled bool   `json:"enabled"`
	ErrMsg  string `json:"err_msg,omitempty"`
	// Usage is total USD spent on the key; Limit is the key's credit
	// cap (0 = uncapped). Remaining is Limit-Usage, or -1 when uncapped.
	Usage      float64 `json:"usage"`
	Limit      float64 `json:"limit"`
	Remaining  float64 `json:"remaining"`
	IsFreeTier bool    `json:"is_free_tier"`
}

// Conversation is one past agent transcript on disk. Returned by
// GET /v1/conversations so clients can show a unified history across
// Claude / Codex / Antigravity sessions without each needing to know
// the on-disk layouts.
type Conversation struct {
	ID       string    `json:"id"`                // agent's own UUID; passed to its --resume flag
	Agent    string    `json:"agent"`             // "claude" | "codex" | "antigravity"
	Project  string    `json:"project,omitempty"` // best-effort project label
	Path     string    `json:"path,omitempty"`    // session's working directory if known
	Preview  string    `json:"preview,omitempty"` // first user message (empty for antigravity)
	Modified time.Time `json:"modified"`          // when the transcript was last written
}

// PairRequest is the body of POST /v1/pair (mobile → daemon).
type PairRequest struct {
	Token     string `json:"token"`
	PublicKey string `json:"public_key"`
	// Optional APNs registration carried inline so push works from
	// first pair without a second round trip. Omitted means the
	// client doesn't (yet) have a push token to register.
	DeviceToken string `json:"device_token,omitempty"`
	APNsEnv     string `json:"apns_env,omitempty"` // "development" | "production"
}

// RegisterDeviceRequest is the body of POST /v1/devices. Used to
// update a mobile device's push token on an already-paired host
// (e.g. when the user granted notifications after the initial pair,
// or when iOS / FCM rolls the token). Identified by the same SSH
// public key that was recorded at pair time, so no separate
// device-id concept is needed.
//
// Provider is "apns" or "fcm". An empty Provider is treated as
// "apns" so existing iOS clients that predate the multi-gateway
// device store keep working without a client change.
type RegisterDeviceRequest struct {
	Token     string `json:"token"`
	Env       string `json:"env,omitempty"` // required for apns; empty for fcm
	PublicKey string `json:"public_key"`
	Provider  string `json:"provider,omitempty"` // "apns" (default) | "fcm"
}

// PairResponse is what POST /v1/pair returns on success.
type PairResponse struct {
	Hostname string `json:"hostname"`
	Version  string `json:"version"`
}

// PairTokenResponse is what POST /v1/pair-token returns (unix-socket only).
type PairTokenResponse struct {
	Token string `json:"token"`
	URL   string `json:"url"` // ccmux://pair?host=…&user=…&port=…&token=…
}

// RenameRequest is the body of POST /v1/sessions/{name}/rename.
type RenameRequest struct {
	Name string `json:"name"`
}

// SendKeysRequest is the body of POST /v1/sessions/{name}/send-keys.
type SendKeysRequest struct {
	Keys string `json:"keys"`
}
