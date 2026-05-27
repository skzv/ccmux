package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/tui/styles"
)

// TestNetworkSetupSSH_SkipsWizardForTailscaleSSHPeer — pressing
// `s` on a peer with Tailscale SSH enabled must NOT open the
// wizard. Instead we emit a toast explaining that no setup is
// needed. The whole point of TS-SSH is auth-without-key-install;
// running the wizard would just install a key the remote ignores.
func TestNetworkSetupSSH_SkipsWizardForTailscaleSSHPeer(t *testing.T) {
	m := newNetwork(styles.Default(), DefaultKeymap())
	m.SetHosts([]hostStatus{
		{Name: "tss-peer", DialHost: "tss-peer", TailscaleSSH: true},
	})
	cmd := m.SetupSSHCmd()
	if cmd == nil {
		t.Fatal("SetupSSHCmd returned nil for a setup-able row")
	}
	msg := cmd()
	if _, isWizard := msg.(openSSHWizardMsg); isWizard {
		t.Errorf("opened wizard for TS-SSH peer; want a toast instead")
	}
	toast, ok := msg.(toastMsg)
	if !ok {
		t.Fatalf("expected toastMsg, got %T", msg)
	}
	if !strings.Contains(strings.ToLower(toast.Text), "tailscale ssh") {
		t.Errorf("toast.Text = %q, want mention of Tailscale SSH", toast.Text)
	}
}

// TestNetworkSetupSSH_OpensWizardForPlainPeer — the negative
// control: when TailscaleSSH is false, the wizard opens as before.
func TestNetworkSetupSSH_OpensWizardForPlainPeer(t *testing.T) {
	m := newNetwork(styles.Default(), DefaultKeymap())
	m.SetHosts([]hostStatus{
		{Name: "plain-peer", DialHost: "plain-peer", TailscaleSSH: false},
	})
	cmd := m.SetupSSHCmd()
	if cmd == nil {
		t.Fatal("SetupSSHCmd nil for plain peer")
	}
	if _, ok := cmd().(openSSHWizardMsg); !ok {
		t.Errorf("expected openSSHWizardMsg for plain peer")
	}
}

// TestRemoteAttachTargetFromErr_SkipsTailscaleSSH — the post-
// attach auth-failure auto-route MUST NOT fire for Tailscale-SSH
// peers. Installing a key wouldn't help an ACL rejection.
func TestRemoteAttachTargetFromErr_SkipsTailscaleSSH(t *testing.T) {
	msg := attachExitedMsg{
		Err: errors.New("exit status 255: Permission denied (publickey)"),
		RemoteSSHTarget: &attachRemoteTarget{
			User:         "alice",
			Host:         "tss-peer",
			Port:         22,
			TailscaleSSH: true,
		},
	}
	got := remoteAttachTargetFromErr(msg)
	if got != nil {
		t.Errorf("remoteAttachTargetFromErr returned %+v; want nil for TS-SSH peer", got)
	}
}

// TestRemoteAttachTargetFromErr_FiresForPlainPeer — negative
// control. Same auth-shaped error but TailscaleSSH=false → the
// wizard auto-route should still fire.
func TestRemoteAttachTargetFromErr_FiresForPlainPeer(t *testing.T) {
	msg := attachExitedMsg{
		Err: errors.New("exit status 255: Permission denied (publickey)"),
		RemoteSSHTarget: &attachRemoteTarget{
			User: "alice", Host: "plain", Port: 22, TailscaleSSH: false,
		},
	}
	got := remoteAttachTargetFromErr(msg)
	if got == nil {
		t.Fatal("remoteAttachTargetFromErr returned nil for plain peer; want non-nil")
	}
}

// TestNetworkRenderRow_ShowsTSSSHBadge — the row for a Tailscale-
// SSH peer renders a small badge so the user knows why no wizard
// fires. Plain peers don't get the badge.
func TestNetworkRenderRow_ShowsTSSSHBadge(t *testing.T) {
	m := newNetwork(styles.Default(), DefaultKeymap())
	m.SetHosts([]hostStatus{
		{Name: "tss-peer", DialHost: "tss-peer", TailscaleSSH: true, OK: true},
		{Name: "plain-peer", DialHost: "plain-peer", TailscaleSSH: false, OK: true},
	})
	v := m.View(120, 30)
	if !strings.Contains(v, "ts-ssh") {
		t.Errorf("View missing ts-ssh badge; got %q", firstN(v, 400))
	}
	// Plain peer shouldn't get the badge — coarse check that the
	// badge appears only once for the two-row fixture.
	if strings.Count(v, "ts-ssh") != 1 {
		t.Errorf("ts-ssh count = %d, want 1 (only tss-peer should be badged)",
			strings.Count(v, "ts-ssh"))
	}
}

// _ to satisfy the unused-import guard if a future edit removes
// tea references; the import stays because Update messages use it.
var _ = tea.KeyMsg{}
