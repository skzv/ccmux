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
	// "gemini". Empty for sessions whose project we couldn't resolve
	// (which the client should treat as claude for back-compat).
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

// NewProjectRequest is the body of POST /v1/projects. Asks the daemon
// to scaffold a brand-new project under its configured Projects.Root
// and start an agent session inside it.
//
// Used by the Projects screen's "n" flow when the user picks a remote
// host: the local TUI calls this over the tailnet, the remote daemon
// does the scaffold + tmux dance natively (no SSH round-trips for
// `git init` etc), and the TUI then attaches over ssh.
type NewProjectRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"` // becomes the first prompt to the agent
	// Agent picks which AI agent the remote daemon launches inside
	// the new session. One of "claude" / "codex" / "gemini"; empty
	// (omitted by older clients) defaults to claude on the daemon
	// side for back-compat.
	Agent string `json:"agent,omitempty"`
}

// NewProjectResponse is what POST /v1/projects returns once the daemon
// finished scaffolding + starting the session. Session is the tmux
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
	Modified time.Time `json:"modified"`
}
