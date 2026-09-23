package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/daemon"
)

// Host-aware session mutations for the Sessions screen (`x` kill, `R`
// rename). A row carries a (Host, Name) pair and the same tmux name can
// exist on several machines — `c-ccmux` on the laptop AND on the mini
// is the normal case — so every mutation must be routed by host. The
// old path dropped the host and always ran tmux locally: picking the
// mini's row and confirming a kill killed the laptop's session.

// isLocalSessionHost reports whether a session row's Host label means
// "this machine". Refresh stamps local rows "local"; an empty label is
// the daemon wire default; and a row can also carry this machine's own
// hostname (the Devices-panel name of the Local host row). Mirrors the
// resolution attachSelectedSession uses.
func (a App) isLocalSessionHost(host string) bool {
	if host == "" || host == "local" {
		return true
	}
	if h := a.localHostStatus(); h != nil && h.Name == host {
		return true
	}
	return false
}

// remoteDaemonAddr resolves a remote session row's host label to the
// "host:port" of that machine's ccmuxd, from the live hosts list the
// refresh built (configured hosts and discovered peers both carry the
// daemon address in Address). ok is false when the host is unknown or
// has no daemon to talk to (a mobile peer, a peer without ccmuxd).
func (a App) remoteDaemonAddr(host string) (addr string, ok bool) {
	hs := a.lookupHostByName(host)
	if hs == nil || hs.Local || hs.Mobile || hs.NeedsInstall || hs.Address == "" {
		return "", false
	}
	return hs.Address, true
}

// sessionDisplayName is how a toast names a session: the bare tmux name
// for this machine, "name (host)" for a remote one so the user can see
// which machine the action hit.
func sessionDisplayName(host, name string) string {
	if host == "" {
		return name
	}
	return name + " (" + host + ")"
}

// killSessionTargetCmd kills the session `name` on the machine its row
// lives on: the local tmux server for local rows, the owning host's
// ccmuxd for remote rows. An unknown / daemon-less remote host is
// refused with a toast — never silently retargeted at the local server.
func (a App) killSessionTargetCmd(host, name string) tea.Cmd {
	if a.isLocalSessionHost(host) {
		return killSessionCmd(name)
	}
	addr, ok := a.remoteDaemonAddr(host)
	if !ok {
		return noReachableDaemonToast(host)
	}
	return killRemoteSessionCmd(addr, host, name)
}

// renameSessionTargetCmd is killSessionTargetCmd's twin for `R`: a
// remote row is renamed through its host's ccmuxd rename endpoint.
func (a App) renameSessionTargetCmd(host, oldName, newName string) tea.Cmd {
	if a.isLocalSessionHost(host) {
		return renameSessionCmd(oldName, newName)
	}
	addr, ok := a.remoteDaemonAddr(host)
	if !ok {
		return noReachableDaemonToast(host)
	}
	return renameRemoteSessionCmd(addr, host, oldName, newName)
}

func noReachableDaemonToast(host string) tea.Cmd {
	until := time.Now().Add(5 * time.Second)
	return func() tea.Msg {
		return toastMsg{Text: fmt.Sprintf(tr("no reachable daemon for host: %s"), host), Kind: toastError, Until: until}
	}
}

// killRemoteSessionCmd asks the ccmuxd at addr to kill `name`. A package
// var (like killSessionCmd) so tests can observe routing without a
// network round trip.
var killRemoteSessionCmd = func(addr, host, name string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := daemon.RemoteClient(addr).Kill(ctx, name)
		return sessionKilledMsg{Name: name, Host: host, Err: err}
	}
}

// renameRemoteSessionCmd asks the ccmuxd at addr to rename a session.
var renameRemoteSessionCmd = func(addr, host, oldName, newName string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := daemon.RemoteClient(addr).Rename(ctx, oldName, newName)
		return sessionRenamedMsg{Host: host, OldName: oldName, NewName: newName, Err: err}
	}
}
