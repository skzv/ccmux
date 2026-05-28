package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/sshsetup"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// TestSSHShellCommand_ArgvShape pins the ssh argv the post-setup
// "open shell" step builds. Port 22 (and 0) stay on the bare
// `ssh -t user@host` form; a custom port adds `-p`.
func TestSSHShellCommand_ArgvShape(t *testing.T) {
	cases := []struct {
		name   string
		target sshsetup.Target
		want   []string
	}{
		{
			name:   "default-port",
			target: sshsetup.Target{User: "alice", Host: "sputnik", Port: 22},
			want:   []string{"ssh", "-t", "alice@sputnik"},
		},
		{
			name:   "zero-port-same-as-22",
			target: sshsetup.Target{User: "alice", Host: "sputnik", Port: 0},
			want:   []string{"ssh", "-t", "alice@sputnik"},
		},
		{
			name:   "custom-port",
			target: sshsetup.Target{User: "bob", Host: "sputnik", Port: 2222},
			want:   []string{"ssh", "-t", "-p", "2222", "bob@sputnik"},
		},
		{
			name:   "no-user",
			target: sshsetup.Target{Host: "sputnik", Port: 22},
			want:   []string{"ssh", "-t", "sputnik"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd := sshShellCommand(c.target)
			if !equalStrings(cmd.Args, c.want) {
				t.Errorf("argv = %v, want %v", cmd.Args, c.want)
			}
		})
	}
}

// TestShouldOpenShell_OnlyWhenResumeAsks pins the decision: an
// OpenShell resume returns the target + true; any other resume
// (nil, wrong type, OpenShell=false) returns false so the wizard
// falls back to the "SSH ready" toast.
func TestShouldOpenShell_OnlyWhenResumeAsks(t *testing.T) {
	tgt := sshsetup.Target{User: "alice", Host: "sputnik", Port: 22}

	if got, ok := shouldOpenShell(sshWizardResume{OpenShell: true}, tgt); !ok || got != tgt {
		t.Errorf("OpenShell:true → (%+v, %v), want (%+v, true)", got, ok, tgt)
	}
	if _, ok := shouldOpenShell(sshWizardResume{OpenShell: false}, tgt); ok {
		t.Error("OpenShell:false should not open a shell")
	}
	if _, ok := shouldOpenShell(nil, tgt); ok {
		t.Error("nil resume should not open a shell")
	}
	if _, ok := shouldOpenShell("some-other-payload", tgt); ok {
		t.Error("unrelated resume payload should not open a shell")
	}
}

// TestNetworkSetupSSH_CarriesOpenShellResume — pressing `s` on the
// Network tab must carry the OpenShell intent so a successful setup
// drops the user into the device. This is the regression guard for
// the reported bug: setup ran, then nothing opened.
func TestNetworkSetupSSH_CarriesOpenShellResume(t *testing.T) {
	m := newNetwork(styles.Default(), DefaultKeymap())
	m.SetHosts([]hostStatus{
		{Name: "sputnik", DialHost: "sputnik", User: "alice"},
	})
	cmd := m.SetupSSHCmd()
	if cmd == nil {
		t.Fatal("SetupSSHCmd returned nil for a setup-able row")
	}
	open, ok := cmd().(openSSHWizardMsg)
	if !ok {
		t.Fatalf("expected openSSHWizardMsg, got %T", cmd())
	}
	r, ok := open.resume.(sshWizardResume)
	if !ok || !r.OpenShell {
		t.Errorf("Network `s` resume = %+v, want sshWizardResume{OpenShell:true}", open.resume)
	}
}

// TestWizardCompleted_OpenShellResume_OpensShell — the end-to-end
// fix: a wizard completion carrying an OpenShell resume must return
// a (non-nil) command — the ssh ExecProcess — rather than only the
// "SSH ready" toast. We can't introspect tea.ExecProcess, so we
// assert the App took the shell branch by checking that the command
// it returns is NOT the toast (the toast path returns a toastMsg;
// the shell path returns an exec the runtime owns, which yields no
// synchronous tea.Msg).
func TestWizardCompleted_OpenShellResume_OpensShell(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := New(config.Config{}, "test")
	app.tour.Close()

	target := sshsetup.Target{User: "alice", Host: "sputnik", Port: 22}

	// Control: no resume → toast.
	_, toastCmd := app.Update(wizardCompletedMsg{target: target})
	if msg := runWizardCmd(toastCmd); !isToast(msg) {
		t.Fatalf("no-resume completion should produce a toast, got %T", msg)
	}

	// Fix: OpenShell resume → the shell exec (NOT a toast).
	_, shellCmd := app.Update(wizardCompletedMsg{
		target: target,
		resume: sshWizardResume{OpenShell: true},
	})
	if shellCmd == nil {
		t.Fatal("OpenShell completion returned a nil command — shell never opens (the reported bug)")
	}
	if msg := runWizardCmd(shellCmd); isToast(msg) {
		t.Fatalf("OpenShell completion produced a toast instead of opening the shell: %+v", msg)
	}
}

func isToast(msg tea.Msg) bool {
	_, ok := msg.(toastMsg)
	return ok
}

// runWizardCmd materializes a tea.Cmd to its message. tea.ExecProcess
// returns an internal exec-blocking message (not toastMsg), and may
// return nil here since it expects the runtime to drive it; either
// way it is distinguishable from the toast path.
func runWizardCmd(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	defer func() { _ = recover() }() // ExecProcess Cmds can panic without a running Program
	return cmd()
}
