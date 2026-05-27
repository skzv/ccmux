package tui

import (
	"os/user"
	"testing"

	"github.com/skzv/ccmux/internal/tui/styles"
)

// TestRemoteTargetForSSH_ParsesUserPrefix — when the dial string
// is "alice@sputnik", the resulting target splits the user out so
// the wizard knows to install for alice (not local $USER). This
// pins the bug where the Network screen's Enter→SSH path used to
// silently swallow the auth failure; the fix routes through
// attachExitedMsg with RemoteSSHTarget populated, and this is the
// helper that populates it.
func TestRemoteTargetForSSH_ParsesUserPrefix(t *testing.T) {
	sel := hostStatus{Name: "sputnik", User: ""}
	got := remoteTargetForSSH(sel, "alice@sputnik")
	if got.User != "alice" {
		t.Errorf("User = %q, want alice", got.User)
	}
	if got.Host != "sputnik" {
		t.Errorf("Host = %q, want sputnik", got.Host)
	}
	if got.Port != 22 {
		t.Errorf("Port = %d, want 22", got.Port)
	}
}

// TestRemoteTargetForSSH_HonorsSelectionUser — bare-host dial
// with the row's User set (configured host case): the result
// picks up the User from the hostStatus.
func TestRemoteTargetForSSH_HonorsSelectionUser(t *testing.T) {
	sel := hostStatus{Name: "sputnik", User: "deploy"}
	got := remoteTargetForSSH(sel, "sputnik")
	if got.User != "deploy" {
		t.Errorf("User = %q, want deploy", got.User)
	}
}

// TestRemoteTargetForSSH_FallsBackToLocalUser — auto-discovered
// peer case: no User on the row, no user@ prefix in dial. We must
// default to the local $USER so the wizard's install attempt
// matches the (failed) ssh attempt the user just made. Otherwise
// the wizard would error on "user is required" and the user would
// be stuck.
func TestRemoteTargetForSSH_FallsBackToLocalUser(t *testing.T) {
	sel := hostStatus{Name: "project-server", User: ""}
	got := remoteTargetForSSH(sel, "project-server")
	u, _ := user.Current()
	if got.User == "" {
		t.Errorf("User = empty; must fall back to local $USER")
	}
	if u != nil && got.User != u.Username {
		t.Errorf("User = %q, want local %q", got.User, u.Username)
	}
}

// TestNetworkSSHCmd_EmitsAttachExitedMsg — the regression test
// for the original bug report: pressing Enter on the Network
// screen against a not-yet-keyed peer used to silently swallow
// the "Permission denied (publickey)" error via
// refreshAfterDetachMsg. After the fix, the callback routes
// through attachExitedMsg with both Err and RemoteSSHTarget
// populated, so the App's auto-route can fire the wizard.
//
// We can't fully drive tea.ExecProcess in a unit test (it
// suspends the program), so this test asserts the shape of the
// SSHCmd's Cmd is non-nil for a setup-able row.
func TestNetworkSSHCmd_NonNilForRealHost(t *testing.T) {
	m := newNetwork(styles.Default(), DefaultKeymap())
	m.SetHosts([]hostStatus{
		{Name: "project-server", DialHost: "project-server", User: ""},
	})
	if c := m.SSHCmd(); c == nil {
		t.Fatal("SSHCmd should return a non-nil Cmd for a setup-able row")
	}
}
