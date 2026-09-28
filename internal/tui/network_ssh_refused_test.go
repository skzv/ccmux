package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// TestNetworkSSH_ConnectionRefusedToastsInsteadOfWizard — Enter on a
// Network row whose ssh fails to connect (ssh exits 255 with
// "Connection refused") opened the full-screen SSH setup wizard,
// because any exit 255 was taken for an auth failure. A key install
// can't fix a refused connection: the user must get the error toast,
// naming the command that ran and what it said.
func TestNetworkSSH_ConnectionRefusedToastsInsteadOfWizard(t *testing.T) {
	host := hostStatus{Name: "box", Source: "configured", DialHost: "127.0.0.9", User: "me", OK: true}
	label := host.sshShellCommandLine()
	msg := runCaptured(t, `echo "ssh: connect to host 127.0.0.9 port 22: Connection refused" >&2; exit 255`, label, true)
	if msg.Err == nil {
		t.Fatal("ssh exit 255 not reported as a failure")
	}
	msg.RemoteSSHTarget = remoteTargetForSSH(host, host.sshShellTarget())

	a := newAppForTest(t)
	_, cmd := a.Update(msg)
	// Only the batch's last command is run: the refreshes before it
	// would probe real hosts.
	if batch, ok := cmd().(tea.BatchMsg); ok && len(batch) > 0 {
		if _, wizard := batch[len(batch)-1]().(openSSHWizardMsg); wizard {
			t.Fatal("a refused connection opened the SSH setup wizard")
		}
	}
	toast := lastToast(t, cmd)
	for _, want := range []string{"ssh -t me@127.0.0.9", "Connection refused"} {
		if !strings.Contains(toast.Text, want) {
			t.Errorf("toast %q lacks %q", toast.Text, want)
		}
	}
}

// TestNetwork_SelectedPaneShowsRealSSHCommand — the Selected pane's
// hint said `ssh -t 127.0.0.9` while Enter ran `ssh -t me@127.0.0.9`
// (and a custom port went unmentioned). The hint is the command.
func TestNetwork_SelectedPaneShowsRealSSHCommand(t *testing.T) {
	cases := []struct {
		host hostStatus
		want string
	}{
		{hostStatus{Name: "box", Source: "configured", DialHost: "127.0.0.9", User: "me", OK: true}, "ssh -t me@127.0.0.9"},
		{hostStatus{Name: "mini", Source: "configured", DialHost: "mac-mini", User: "sasha", SSHPort: 2222, OK: true}, "ssh -t -p 2222 sasha@mac-mini"},
		{hostStatus{Name: "peer", Source: "discovered", Discovered: true, Address: "100.64.0.2:7474", OK: true}, "ssh -t 100.64.0.2"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			m := newNetwork(mustStyles(t), DefaultKeymap())
			m.SetHosts([]hostStatus{tc.host})
			out := ansi.Strip(m.View(160, 40))
			if !strings.Contains(out, tc.want) {
				t.Errorf("Selected pane lacks %q:\n%s", tc.want, out)
			}
		})
	}
}
