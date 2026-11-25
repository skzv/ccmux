package tui

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemonservice"
	"github.com/skzv/ccmux/internal/setupwizard"
	"github.com/skzv/ccmux/internal/tui/styles"
)

func TestAddHostForm_ValidatesAndSubmits(t *testing.T) {
	f := newAddHostForm(styles.Default())
	// Submitting empty name fails validation.
	f, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("empty form should not emit submit cmd")
	}
	if f.err == "" {
		t.Fatal("expected validation error on empty name")
	}

	// Fill name, tab to address, fill address, tab to port (skip user), enter custom port, toggle mosh.
	f.name.SetValue("mac-mini")
	f.address.SetValue("100.64.0.9")
	f.user.SetValue("alice")
	f.sshPort.SetValue("2222")
	f.focus = 4
	f, _ = f.Update(tea.KeyMsg{Type: tea.KeySpace}) // toggle mosh false
	f, cmd = f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("expected submit cmd; err=%q", f.err)
	}
	msg, ok := cmd().(addHostSubmitMsg)
	if !ok {
		t.Fatalf("got msg %T, want addHostSubmitMsg", cmd())
	}
	if msg.Host.Name != "mac-mini" || msg.Host.Address != "100.64.0.9" || msg.Host.User != "alice" || msg.Host.SSHPort != 2222 || msg.Host.Mosh {
		t.Errorf("unexpected host: %+v", msg.Host)
	}
}

func TestNetwork_AddAndRemoveHostViaApp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	a := newTestApp(ScreenNetwork)
	a.width, a.height = 120, 40

	// Press 'a' opens add-host modal and captures input.
	m, _ := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	a = m.(App)
	if !a.network.AddFormOpen() || !a.modalCapturingText() {
		t.Fatal("pressing 'a' on Network did not open add-host modal")
	}

	// Submit host via addHostSubmitMsg -> addHostCmd -> hostAddedMsg.
	m, cmd := a.Update(addHostSubmitMsg{Host: config.Host{Name: "mini", Address: "100.64.0.9", Port: 7474, SSHPort: 22, Mosh: true}})
	a = m.(App)
	if a.network.AddFormOpen() {
		t.Fatal("add-host modal should close after submit")
	}
	if cmd == nil {
		t.Fatal("expected addHostCmd")
	}
	res := cmd()
	added, ok := res.(hostAddedMsg)
	if !ok || added.Err != nil {
		t.Fatalf("addHostCmd = %#v, want nil err", res)
	}
	cfg, err := config.Load()
	if err != nil || len(cfg.Hosts) != 1 || cfg.Hosts[0].Name != "mini" {
		t.Fatalf("config after add = %+v (err=%v)", cfg.Hosts, err)
	}

	// Now select that configured host and press 'x' -> opens confirmation -> confirm removes it.
	a.network.SetHosts([]hostStatus{{Name: "mini", Address: "100.64.0.9", Source: "configured"}})
	m, _ = a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	a = m.(App)
	if !a.confirm.open() || a.confirm.kind != confirmationRemoveHost {
		t.Fatalf("expected confirmationRemoveHost modal, got %+v", a.confirm)
	}
	a.confirm.focus = confirmationFocusConfirm
	m, cmd = a.acceptConfirmation()
	a = m.(App)
	if cmd == nil {
		t.Fatal("expected removeHostCmd")
	}
	rmRes := cmd()
	removed, ok := rmRes.(hostRemovedMsg)
	if !ok || removed.Err != nil {
		t.Fatalf("removeHostCmd = %#v, want nil err", rmRes)
	}
	cfg, err = config.Load()
	if err != nil || len(cfg.Hosts) != 0 {
		t.Fatalf("config after remove = %+v (err=%v)", cfg.Hosts, err)
	}
}

func TestPairModal_OpensRendersQRAndCloses(t *testing.T) {
	origFetch := fetchPairTokenFn
	t.Cleanup(func() { fetchPairTokenFn = origFetch })
	fetchPairTokenFn = func(context.Context) (string, error) {
		return "ccmux://pair?host=100.64.0.5&port=7474&token=abc123", nil
	}

	a := newTestApp(ScreenNetwork)
	a.width, a.height = 100, 36

	m, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	a = m.(App)
	if !a.pair.IsOpen() || !a.modalCapturingText() {
		t.Fatal("pressing 'P' did not open pair modal")
	}
	if cmd == nil {
		t.Fatal("expected fetchPairTokenCmd")
	}
	m, _ = a.Update(cmd())
	a = m.(App)
	view := a.View()
	if !strings.Contains(view, "ccmux://pair?host=100.64.0.5") {
		t.Errorf("pair modal view missing URL:\n%s", view)
	}

	m, _ = a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	a = m.(App)
	if a.pair.IsOpen() {
		t.Fatal("esc did not close pair modal")
	}
}

func TestDaemonLifecycleAndClaudeMCPActions(t *testing.T) {
	origProbe, origRestart := daemonProbeFn, daemonRestartFn
	t.Cleanup(func() {
		daemonProbeFn = origProbe
		daemonRestartFn = origRestart
	})
	restarted := false
	daemonProbeFn = func() daemonservice.Status {
		return daemonservice.Status{OS: "darwin", Running: true, ServiceEnabled: true}
	}
	daemonRestartFn = func() (daemonservice.Status, error) {
		restarted = true
		return daemonservice.Status{OS: "darwin", Running: true}, nil
	}

	a := newTestApp(ScreenSettings)
	a.width, a.height = 100, 36
	m, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	a = m.(App)
	if cmd == nil {
		t.Fatal("pressing 'D' on Settings did not return daemonLifecycleCmd")
	}
	done, ok := cmd().(daemonActionDoneMsg)
	if !ok || done.Err != nil || !restarted {
		t.Fatalf("daemonLifecycleCmd = %#v restarted=%v", done, restarted)
	}

	origStatus, origReg := mcpStatusFn, mcpRegisterFn
	t.Cleanup(func() {
		mcpStatusFn = origStatus
		mcpRegisterFn = origReg
	})
	registered := false
	mcpStatusFn = func() (setupwizard.MCPRegistration, error) {
		return setupwizard.MCPRegistration{}, nil
	}
	mcpRegisterFn = func(context.Context, io.Writer, setupwizard.RegisterOptions) error {
		registered = true
		return nil
	}

	a.screen = ScreenAgents
	a.agentsM = newAgents(a.styles, a.keys)
	m, cmd = a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	a = m.(App)
	if cmd == nil {
		t.Fatal("pressing 'w' on Agents->Claude did not return toggleClaudeMCPCmd")
	}
	toggled, ok := cmd().(claudeMCPToggledMsg)
	if !ok || toggled.Err != nil || !registered {
		t.Fatalf("toggleClaudeMCPCmd = %#v registered=%v", toggled, registered)
	}
}
