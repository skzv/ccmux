package tui

import (
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/claudeusage"
	"github.com/skzv/ccmux/internal/conversations"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/notes"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/usage"
)

// sessionsLoadedMsg is delivered when a fresh dashboard refresh completes.
// Carries sessions from every reachable host (local + configured remotes).
type sessionsLoadedMsg struct {
	Sessions []daemon.SessionState
	Hosts    []hostStatus
	Err      error
	At       time.Time
}

// projectsLoadedMsg carries discovered projects under the configured root.
type projectsLoadedMsg struct {
	Projects []project.Project
	Err      error
}

// hostStatus is one row in the host-health pings table.
type hostStatus struct {
	Name string
	// Local marks the row representing this machine. The Devices
	// panel renders it with a small "(this device)" tag so the user
	// can tell at a glance which row is which.
	Local     bool
	Address   string
	OK        bool
	Sessions  int
	SleepMode string
	Err       error
	// Discovered is true when this row came from tailnet auto-discovery
	// rather than the user's explicit `ccmux host add`. The dashboard
	// uses it to render a subtle "discovered" tag so the user knows
	// they didn't have to configure it.
	Discovered bool
	// DialHost is the bare hostname (no port) the attach path should
	// hand to ssh/mosh — typically a MagicDNS short name for discovered
	// peers or the configured address for explicit hosts.
	DialHost string
	// User is the ssh/mosh login user for this host. Empty means the
	// client's own username. Populated for explicit cfg.Hosts entries
	// that have a `user` field set; always empty for discovered peers.
	User string
	// Mosh signals that the user prefers mosh over ssh for this host.
	// Only set for explicit cfg.Hosts entries with `mosh = true`.
	Mosh bool
	// Version is the remote ccmuxd's reported version string from
	// /v1/health. Empty for hosts we couldn't reach. The dashboard
	// compares against the local build to flag "update available".
	Version string
	// NeedsInstall flags a tailnet peer that didn't respond to the
	// /v1/health probe — typically another Mac or Linux box on the
	// tailnet where the user hasn't installed ccmux yet. The Devices
	// panel renders these with a "ccmux not installed / running" hint
	// instead of session counts.
	NeedsInstall bool
	// Mobile flags a phone / iPad / Android peer on the tailnet. We
	// surface these in the Devices panel so the user sees that the
	// device is reachable, but with a "connect via Moshi app" hint
	// instead of session counts (the Moshi app handles the picker).
	Mobile bool
	// OS is what Tailscale reports for this peer ("macOS", "Linux",
	// "iOS", "iPadOS", "Android", …). Populated for NeedsInstall and
	// Mobile rows so the hint can be platform-aware.
	OS string
}

// tickMsg is the periodic dashboard refresh trigger.
type tickMsg struct{ At time.Time }

// toastMsg displays a one-line transient notification in the status bar.
type toastMsg struct {
	Text  string
	Kind  toastKind
	Until time.Time
}

// toastEntry is a frozen snapshot of a past toast, kept in a small ring
// in the App so the help overlay can replay recent activity.
type toastEntry struct {
	At   time.Time
	Kind toastKind
	Text string
}

type toastKind int

const (
	toastInfo toastKind = iota
	toastSuccess
	toastWarning
	toastError
)

// New-project flow messages.

// newProjectSubmitMsg is emitted by the modal form when the user confirms.
// Host/DialHost/Address are zero/empty for the local case; for a remote
// pick, the form fills them from the App's hostStatus slice and the
// dispatcher routes via daemon.Client.NewProject().
type newProjectSubmitMsg struct {
	Name        string
	Description string

	// Host is the display name of the target device ("local",
	// "mac-mini", …). Empty / "local" means scaffold on this machine.
	Host string

	// Address is the http "host:port" of the remote ccmuxd (used to
	// build a daemon.RemoteClient). Empty for local.
	Address string

	// DialHost is the bare hostname for the ssh-attach step after the
	// remote scaffolds. Typically the MagicDNS short name so
	// known_hosts matches. Empty for local.
	DialHost string

	// Agent is the AI agent the user picked in the form's agent row
	// (claude / codex / antigravity). Empty defaults to claude downstream.
	// Carried through daemon.NewProjectRequest so remote scaffolds
	// honor the choice.
	Agent agent.ID
}

// projectAgentSwitchedMsg fires after a successful "a" press on the
// Projects screen — the in-memory project list needs a refresh so the
// detail pane reflects the new agent without waiting for the next
// poll tick. Index is the projectsModel cursor at the time of switch.
type projectAgentSwitchedMsg struct {
	Path  string
	Agent agent.ID
}

// newProjectCancelMsg is emitted by the modal form when the user hits Esc.
type newProjectCancelMsg struct{}

// projectSessionExistsMsg is emitted by attachOrCreateLocal when the
// project already has a running tmux session. App responds by opening
// the session picker modal so the user can choose between rejoining
// the existing session or spawning a new one with a distinct name.
type projectSessionExistsMsg struct {
	Existing    string // name of the already-running tmux session
	Project     string // project display name (for the label)
	ProjectPath string // working directory for "start new"
}

// projectSessionPickMsg is emitted by projectSessionPickerModel on Enter.
// Action is "rejoin" or "new".
type projectSessionPickMsg struct {
	Action      string // "rejoin" | "new"
	Existing    string // original session name (rejoin target)
	NewName     string // name for the new session (only used when Action == "new")
	Project     string
	ProjectPath string
}

// projectSessionPickCancelMsg is emitted by the picker on Esc.
type projectSessionPickCancelMsg struct{}

// projectSessionReadyMsg is emitted after scaffold + StartSession finishes;
// triggers the actual tmux-attach via tea.ExecProcess. Project is the
// human-readable label passed to tmuxchrome.Apply so the attached
// status bar reads "ccmux | <project>" rather than the raw session name.
type projectSessionReadyMsg struct {
	Session string
	Project string
}

// remoteSessionStartedMsg fires after `ccmuxd` on a remote host
// returns success from POST /v1/sessions. SessionName is what tmux
// labeled the session on the remote (c-<basename>); DialHost is the
// ssh-target string we should use to attach. The App responds by
// suspending Bubble Tea and exec'ing into ssh.
type remoteSessionStartedMsg struct {
	SessionName string
	DialHost    string
	User        string // login user; empty → client's own username
	Mosh        bool   // prefer mosh over ssh
}

// New-bare-session flow (Sessions tab `n` key). Mirrors the new-
// project flow but doesn't carry a project name or description —
// just a session in this dir on this device, running the picked
// agent (or $SHELL when Agent is "").

// newBareSessionSubmitMsg is emitted by the Sessions tab's form
// when the user confirms. Host/Address/DialHost are zero for the
// local case; for a remote pick, the form fills them from the
// App's hostStatus slice.
type newBareSessionSubmitMsg struct {
	Name string // tmux session name; empty → server picks
	Path string // working directory; empty → daemon's default_dir → $HOME

	Host     string // "local" or peer display name
	Address  string // ccmuxd http "host:port" for remote daemon
	DialHost string // bare hostname/IP for ssh/mosh attach
	User     string // login user; empty → client's own username
	Mosh     bool   // prefer mosh over ssh for this host

	// Agent is the AI agent the form's picker selected. Empty means
	// "shell — no agent", the picker's explicit no-agent row.
	Agent agent.ID
}

// newBareSessionCancelMsg is emitted by the form on Esc.
type newBareSessionCancelMsg struct{}

// bareSessionReadyMsg is the local-flow completion message. Carries
// the new tmux session name so the App can route attach via
// localAttachCmd.
type bareSessionReadyMsg struct {
	Session string
}

// sessionKilledMsg signals that a Sessions-screen `x` kill completed; the
// app responds with an immediate refresh.
type sessionKilledMsg struct {
	Name string
	Err  error
}

// Rename flow (Sessions screen `R` key).

// renameSessionSubmitMsg is emitted by renameFormModel on Enter.
type renameSessionSubmitMsg struct {
	OldName string
	NewName string
}

// renameSessionCancelMsg is emitted by renameFormModel on Esc.
type renameSessionCancelMsg struct{}

// sessionRenamedMsg is emitted after tmux.Rename completes.
type sessionRenamedMsg struct {
	OldName string
	NewName string
	Err     error
}

// Notes screen messages.

// openEditorMsg asks the app to suspend the TUI and run $EDITOR on `Path`.
// Emitted by the Notes screen (after creating a new file) and by the
// Settings screen (for multi-line config values). After tea.ExecProcess
// returns, the App routes a follow-up reload message based on `Source`
// so the right screen refreshes its state.
type openEditorMsg struct {
	Editor string
	Path   string
	// Source identifies which screen wants the reload. "notes" by
	// default (back-compat); "settings" triggers configReloadMsg.
	Source string
}

// notesReloadMsg asks the Notes screen to re-list and re-render after a
// file was created/edited externally.
type notesReloadMsg struct{}

// configReloadMsg asks the App to re-read ~/.config/ccmux/config.toml
// and push the new shape into every screen that holds a cached copy.
// Emitted after the Settings screen's "edit in $EDITOR" flow returns.
type configReloadMsg struct{}

// notesSearchResultMsg carries the result set from a Vault.Search
// invocation back to the Notes screen. Query echoes the user's
// input so the rendering can re-print it as a header.
type notesSearchResultMsg struct {
	Query string
	Hits  []notes.SearchHit
}

// usageTickMsg fires periodically to refresh the dashboard's usage panel.
// Slower cadence than the session tick because walking the transcript
// tree is more expensive.
type usageTickMsg struct{ At time.Time }

// usageLoadedMsg carries the result of a per-agent usage refresh.
// `Agg` is the rich Claude aggregate that drives the dashboard's
// main usage panel (cache breakdown, 5h quota bar, top projects).
// `Codex` and `Antigravity` are the cross-agent summaries; today
// they're always zero-valued (stub walkers) until those agents grow
// real transcript parsers.
type usageLoadedMsg struct {
	Agg         *claudeusage.Aggregate
	Codex       usage.AgentSummary
	Antigravity usage.AgentSummary
	Err         error
}

// conversationsLoadedMsg carries the result of a Conversations-screen
// refresh. The screen reads it via App and forwards List to its model.
// Err is non-nil only when every agent's walker failed — a per-agent
// failure is logged and swallowed so one corrupt transcript dir
// doesn't blank the whole list.
type conversationsLoadedMsg struct {
	List []conversations.Conversation
	Err  error
}

// openConversationsForProjectMsg is the Projects-screen → App
// trigger for the per-project drill-down: pressing `c` on a project
// row emits this with the project's path, App switches to
// ScreenConversations and pre-applies the path as a filter so the
// user lands on a view scoped to "this project's history."
type openConversationsForProjectMsg struct {
	Project string
}

// conversationDeletedMsg fires after a delete attempt completes. The
// App handler toasts the result and, on success, refreshes the
// Conversations list so the deleted row disappears immediately.
type conversationDeletedMsg struct {
	// ID is the conversation that was (or wasn't) deleted, for the
	// toast wording.
	ID string

	// Agent is the agent the conversation belonged to, also for the
	// toast.
	Agent string

	// Err is non-nil when the transcript couldn't be removed (path
	// guard rejected it, file already gone, permission denied).
	Err error
}

// conversationResumedMsg fires after a resume attempt completes (the
// tmux session has been spawned, or the attempt errored). Drives the
// post-resume toast + sessions refresh so the new session shows up
// immediately in the Sessions tab.
type conversationResumedMsg struct {
	// Session is the name of the new tmux session ccmux created for
	// the resumed conversation. Used by App to switch focus to it.
	Session string

	// Project is the resolved project label/path of the conversation,
	// for toast wording.
	Project string

	// Agent is the agent the conversation belongs to, for toast
	// wording and the post-resume attach.
	Agent string

	// Err is non-nil when the resume couldn't be started (agent
	// binary missing, tmux call failed, etc.).
	Err error
}

// Claude config screen messages.

// claudeReloadMsg asks the Claude config screen to re-read settings.json
// and re-list commands/skills, e.g. after the user edited settings.json
// in $EDITOR.
type claudeReloadMsg struct{}

// claudeModelChangedMsg signals that SetModel completed. Carries the
// backup path so the screen can surface "backup at …" in a toast.
type claudeModelChangedMsg struct {
	New    string
	Backup string
	Err    error
}

// claudeEffortChangedMsg signals that SetEffortLevel completed. Same
// shape as the model variant — New is the chosen level ("xhigh" / "" /
// etc.), Backup is the settings.json snapshot path.
type claudeEffortChangedMsg struct {
	New    string
	Backup string
	Err    error
}

// claudeAlwaysThinkingChangedMsg signals that SetAlwaysThinking completed.
// New carries the resulting on/off state so the toast can read "turned on"
// vs "turned off" without re-reading settings.json.
type claudeAlwaysThinkingChangedMsg struct {
	New    bool
	Backup string
	Err    error
}

// claudeYoloChangedMsg signals that SetYoloMode completed. Same shape
// as the always-thinking variant — New is the resulting on/off state.
type claudeYoloChangedMsg struct {
	New    bool
	Backup string
	Err    error
}
