package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/sshsetup"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// TestApp_NetworkSKey_OpensWizard — verify the 's' key on the
// Network screen produces an openSSHWizardMsg with the focused
// host's target, AND the App's Update routes it into the wizard
// model (which then renders the confirm screen).
//
// The test drives the App via tea.Msg fan-in just like Bubble
// Tea's runtime would, without ever starting an actual program.
func TestApp_NetworkSKey_OpensWizard(t *testing.T) {
	cfg := config.Config{
		Hosts: []config.Host{
			{Name: "sputnik", Address: "sputnik.tail-1234.ts.net", User: "alice"},
		},
	}
	app := New(cfg, "test")
	app.tour.Close() // skip the tour so the keystrokes route as expected
	// Set the network model's hosts directly so we have something
	// to focus. SetHosts is the standard way the App pushes data
	// in after each refresh; calling it here is faithful to the
	// runtime flow.
	app.network.SetHosts([]hostStatus{
		{
			Name:     "sputnik",
			Address:  "sputnik.tail-1234.ts.net",
			DialHost: "sputnik.tail-1234.ts.net",
			User:     "alice",
		},
	})

	// Navigate to the Network screen.
	app.screen = ScreenNetwork

	// Send 's'. The App must produce a Cmd; running that Cmd
	// must yield openSSHWizardMsg.
	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if cmd == nil {
		t.Fatal("App.Update did not return a Cmd for 's'")
	}
	msg := cmd()
	open, ok := msg.(openSSHWizardMsg)
	if !ok {
		t.Fatalf("Cmd produced %T, want openSSHWizardMsg", msg)
	}
	if open.target.Host != "sputnik.tail-1234.ts.net" {
		t.Errorf("wizard target Host = %q, want sputnik.tail-1234.ts.net", open.target.Host)
	}
	if open.target.User != "alice" {
		t.Errorf("wizard target User = %q, want alice", open.target.User)
	}
	if open.target.Port != 22 {
		t.Errorf("wizard target Port = %d, want 22 (SSH, not ccmuxd's 7474)", open.target.Port)
	}

	// Feed the openSSHWizardMsg back through Update to confirm
	// the App opens the wizard model in response.
	model, _ = model.(App).Update(msg)
	app2 := model.(App)
	if app2.sshWizard == nil || !app2.sshWizard.Active() {
		t.Fatal("App did not open the wizard after openSSHWizardMsg")
	}
	if app2.sshWizard.Step() != sshWizardConfirm {
		t.Errorf("Step = %v, want sshWizardConfirm", app2.sshWizard.Step())
	}
}

// TestApp_WizardCompletedMsg_PersistsAddedUsers — exercise the
// post-success persistence path. We construct a wizard-completed
// message with two added users, feed it to Update, and verify
// hosts.toml gained two rows in-memory.
func TestApp_WizardCompletedMsg_PersistsAddedUsers(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // isolate config.Save target
	cfg := config.Config{
		Hosts: []config.Host{
			{Name: "alice@sputnik", Address: "sputnik", User: "alice"},
		},
	}
	app := New(cfg, "test")
	app.tour.Close()
	msg := wizardCompletedMsg{
		target: sshsetup.Target{User: "alice", Host: "sputnik", Port: 22},
		added:  []string{"bob", "carol"},
	}
	model, _ := app.Update(msg)
	app2 := model.(App)
	// In-memory cfg should have the two new entries appended.
	got := app2.cfg.Hosts
	if len(got) != 3 {
		t.Fatalf("cfg.Hosts = %d rows, want 3 (alice + bob + carol)", len(got))
	}
	// Order: alice (existing) at [0], bob at [1], carol at [2].
	if got[1].User != "bob" || got[1].Address != "sputnik" {
		t.Errorf("row[1] = %+v, want bob@sputnik", got[1])
	}
	if got[2].User != "carol" || got[2].Address != "sputnik" {
		t.Errorf("row[2] = %+v, want carol@sputnik", got[2])
	}
}

// TestApp_WizardCancelledMsg_NoToastNoConfigChange — Esc bail
// should leave the config and the toast queue alone.
func TestApp_WizardCancelledMsg_NoToastNoConfigChange(t *testing.T) {
	cfg := config.Config{}
	app := New(cfg, "test")
	app.tour.Close()
	beforeRows := len(app.cfg.Hosts)
	beforeToasts := app.toasts.Active()

	_, cmd := app.Update(wizardCancelledMsg{})
	if cmd != nil {
		// Cancel must not emit a toast or any other Cmd.
		if msg := cmd(); msg != nil {
			t.Errorf("cancel produced an unexpected message: %T", msg)
		}
	}
	if got := len(app.cfg.Hosts); got != beforeRows {
		t.Errorf("cfg.Hosts changed on cancel: before=%d after=%d", beforeRows, got)
	}
	if app.toasts.Active() != beforeToasts {
		t.Errorf("toasts changed on cancel; should be unchanged")
	}
}

// TestApp_WizardOverlayRenders — when the wizard is active, the
// App's View must render the wizard overlay (not the underlying
// screen). The smoke test is "the confirm slide's distinctive
// string is in the output".
func TestApp_WizardOverlayRenders(t *testing.T) {
	app := New(config.Config{}, "test")
	app.width = 100
	app.height = 30
	app.tour.Close()
	app.sshWizard.Open(sshsetup.Target{User: "alice", Host: "sputnik"}, nil)
	v := app.View()
	if !strings.Contains(v, "alice@sputnik") {
		t.Errorf("View() didn't render wizard overlay; got %q", firstN(v, 200))
	}
}

// TestApp_ModalCapturingText_IncludesWizard — guards the matrix
// easter-egg and other capital-letter shortcuts so they don't
// fire while the user is typing into the password field.
func TestApp_ModalCapturingText_IncludesWizard(t *testing.T) {
	app := New(config.Config{}, "test")
	app.tour.Close()
	if app.modalCapturingText() {
		t.Fatal("fresh app should not be capturing text")
	}
	app.sshWizard.Open(sshsetup.Target{User: "alice", Host: "sputnik"}, nil)
	if !app.modalCapturingText() {
		t.Fatal("app must report modalCapturingText() while wizard is active")
	}
}

// TestNetworkSetupSSHCmd_NilForUnactionableRows — local and mobile
// rows aren't valid SSH targets; the command must return nil so
// the parent App can show a toast instead of opening a useless
// wizard.
func TestNetworkSetupSSHCmd_NilForUnactionableRows(t *testing.T) {
	cases := []hostStatus{
		{Name: "this-host", Local: true, DialHost: "irrelevant"},
		{Name: "iphone", Mobile: true, DialHost: "phone.tailnet"},
		{Name: "nothing", DialHost: "", Address: ""},
	}
	for _, h := range cases {
		t.Run(h.Name, func(t *testing.T) {
			m := newNetwork(styles.Default(), DefaultKeymap())
			m.SetHosts([]hostStatus{h})
			if c := m.SetupSSHCmd(); c != nil {
				t.Errorf("SetupSSHCmd should be nil for %s", h.Name)
			}
		})
	}
}
