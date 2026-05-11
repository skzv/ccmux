package tui

import (
	"time"

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
}

// tickMsg is the periodic dashboard refresh trigger.
type tickMsg struct{ At time.Time }

// toastMsg displays a one-line transient notification in the status bar.
type toastMsg struct {
	Text  string
	Kind  toastKind
	Until time.Time
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
// triggers the actual tmux-attach via tea.ExecProcess.
type projectSessionReadyMsg struct {
	Session string
}

// sessionKilledMsg signals that a Sessions-screen `x` kill completed; the
// app responds with an immediate refresh.
type sessionKilledMsg struct {
	Name string
	Err  error
}

// Notes screen messages.

// openEditorMsg asks the app to suspend the TUI and run $EDITOR on `Path`.
// The Notes screen emits this after creating a new file; the App handles
// the tea.ExecProcess so the TUI knows to refresh on return.
type openEditorMsg struct {
	Editor string
	Path   string
}

// notesReloadMsg asks the Notes screen to re-list and re-render after a
// file was created/edited externally.
type notesReloadMsg struct{}
