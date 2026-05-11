package tui

import (
	"time"

	"github.com/skzv/ccmux/internal/claudeusage"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/project"
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
	Name      string
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
type newProjectSubmitMsg struct {
	Name        string
	Description string
}

// newProjectCancelMsg is emitted by the modal form when the user hits Esc.
type newProjectCancelMsg struct{}

// projectSessionReadyMsg is emitted after scaffold + StartSession finishes;
// triggers the actual tmux-attach via tea.ExecProcess. Project is the
// human-readable label passed to tmuxchrome.Apply so the attached
// status bar reads "ccmux | <project>" rather than the raw session name.
type projectSessionReadyMsg struct {
	Session string
	Project string
}

// sessionKilledMsg signals that a Sessions-screen `x` kill completed; the
// app responds with an immediate refresh.
type sessionKilledMsg struct {
	Name string
	Err  error
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

// usageTickMsg fires periodically to refresh the dashboard's usage panel.
// Slower cadence than the session tick because walking the transcript
// tree is more expensive.
type usageTickMsg struct{ At time.Time }

// usageLoadedMsg carries the result of a claudeusage.Walk.
type usageLoadedMsg struct {
	Agg *claudeusage.Aggregate
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
