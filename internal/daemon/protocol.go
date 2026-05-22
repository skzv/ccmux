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
	KeepAwake   bool      `json:"keep_awake"`   // per-session "always keep awake" pin
	// Agent is the AI agent driving this session, sourced from the
	// project's .ccmux/agent sidecar. One of "claude" / "codex" /
	// "antigravity" (or the legacy alias "gemini"). Empty for sessions
	// whose project we couldn't resolve (which the client should treat
	// as claude for back-compat).
	Agent string `json:"agent,omitempty"`
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
	Project    string `json:"project"`     // project name (basename of path)
	Path       string `json:"path"`        // working directory; defaults to ~/Projects/<project>
	Continue   bool   `json:"continue"`    // start Claude with --continue
	KeepAwake  bool   `json:"keep_awake"`  // pin this session immediately
	FirstInput string `json:"first_input"` // initial prompt to feed Claude
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
	Name     string    `json:"name"`
	Host     string    `json:"host"`
	Path     string    `json:"path"` // absolute path on the daemon's host
	HasGit   bool      `json:"has_git"`
	HasCM    bool      `json:"has_cm"`
	HasDocs  bool      `json:"has_docs"`
	Agent    string    `json:"agent,omitempty"`
	Modified time.Time `json:"modified"`
}

// PairRequest is the body of POST /v1/pair (mobile → daemon).
type PairRequest struct {
	Token     string `json:"token"`
	PublicKey string `json:"public_key"`
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

// KeepAwakeRequest is the body of POST /v1/sessions/{name}/keep-awake.
type KeepAwakeRequest struct {
	Enabled bool `json:"enabled"`
}

// SendKeysRequest is the body of POST /v1/sessions/{name}/send-keys.
type SendKeysRequest struct {
	Keys string `json:"keys"`
}
