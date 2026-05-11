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
	Text   string
	Kind   toastKind
	Until  time.Time
}

type toastKind int

const (
	toastInfo toastKind = iota
	toastSuccess
	toastWarning
	toastError
)
