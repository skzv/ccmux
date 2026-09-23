package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/sshsetup"
)

// runWizardThroughApp opens the SSH wizard on `a` for `target`, types
// `user` / `port` into the Username step, and drives a passwordless
// run (probe OK, no other users) to completion through App.Update.
func runWizardThroughApp(t *testing.T, a App, target sshsetup.Target, user, port string) App {
	t.Helper()
	step := func(msg tea.Msg) tea.Cmd {
		t.Helper()
		model, cmd := a.Update(msg)
		a = model.(App)
		return cmd
	}
	step(openSSHWizardMsg{target: target})
	a.sshWizard.probeFn = fakeProbeOK
	a.sshWizard.enumerateFn = fakeEnumerateNone
	a.sshWizard.keyFn = fakeKey

	step(tea.KeyMsg{Type: tea.KeyEnter}) // confirm → user
	step(tea.KeyMsg{Type: tea.KeyCtrlU}) // clear the pre-filled user
	step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(user)})
	step(tea.KeyMsg{Type: tea.KeyTab})   // → port field
	step(tea.KeyMsg{Type: tea.KeyCtrlU}) // clear the pre-filled port
	step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(port)})
	probe := step(tea.KeyMsg{Type: tea.KeyEnter}) // user → probing
	enum := step(runCmd(probe))                   // probe OK → enumerate
	step(runCmd(enum))                            // no other users → done
	if a.sshWizard.Step() != sshWizardDone {
		t.Fatalf("wizard ended at %v, want sshWizardDone", a.sshWizard.Step())
	}
	done := step(tea.KeyMsg{Type: tea.KeyEnter}) // done → wizardCompletedMsg
	step(runCmd(done))
	return a
}

// TestApp_WizardSavesCorrectedUserAndPort — regression: when the user
// fixed the username / SSH port on the wizard's Username step, the key
// was installed for the corrected account but hosts.toml kept the old
// values, so the Network row still showed them and the next attach
// failed again. The correction must land on the configured host.
func TestApp_WizardSavesCorrectedUserAndPort(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	seed := config.Defaults()
	seed.Hosts = []config.Host{
		{Name: "mini", Address: "sputnik", User: "alice", Port: 7474, Mosh: true},
		{Name: "other", Address: "elsewhere", User: "alice"},
	}
	if err := config.Save(seed); err != nil {
		t.Fatal(err)
	}
	a := New(seed, "test")
	a.tour.Close()

	a = runWizardThroughApp(t, a, sshsetup.Target{User: "alice", Host: "sputnik", Port: 22}, "bob", "2222")

	disk, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, cfg := range []config.Config{a.cfg, disk} {
		mini, other := cfg.Hosts[0], cfg.Hosts[1]
		if mini.User != "bob" || mini.SSHPort != 2222 {
			t.Errorf("mini = %+v, want User=bob SSHPort=2222", mini)
		}
		if mini.Port != 7474 {
			t.Errorf("mini.Port (ccmuxd) = %d, want 7474 untouched", mini.Port)
		}
		if other.User != "alice" || other.SSHPort != 0 {
			t.Errorf("unrelated host changed: %+v", other)
		}
	}
}

// TestApp_WizardCorrectionFillsUserlessHost — a configured host with no
// user (the wizard guessed the local $USER) gets the confirmed user.
func TestApp_WizardCorrectionFillsUserlessHost(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	seed := config.Defaults()
	seed.Hosts = []config.Host{{Name: "mini", Address: "sputnik"}}
	if err := config.Save(seed); err != nil {
		t.Fatal(err)
	}
	a := New(seed, "test")
	a.tour.Close()

	a = runWizardThroughApp(t, a, sshsetup.Target{User: "localme", Host: "sputnik", Port: 22}, "bob", "22")

	disk, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if h := disk.Hosts[0]; h.User != "bob" || h.SSHPort != 0 {
		t.Errorf("mini = %+v, want User=bob and the default SSH port", h)
	}
}

// TestWizardHostMatches_PrefersExplicitUser — with an explicit-user row
// and a user-less row on the same address, only the explicit one is
// the wizard's host.
func TestWizardHostMatches_PrefersExplicitUser(t *testing.T) {
	hosts := []config.Host{
		{Name: "a", Address: "sputnik", User: "alice"},
		{Name: "b", Address: "sputnik"},
		{Name: "c", Address: "sputnik", User: "alice", SSHPort: 2222},
	}
	got := wizardHostMatches(hosts, sshsetup.Target{User: "alice", Host: "sputnik", Port: 22})
	if len(got) != 1 || got[0] != 0 {
		t.Errorf("matches = %v, want [0]", got)
	}
	got = wizardHostMatches(hosts, sshsetup.Target{User: "someone", Host: "sputnik", Port: 22})
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("fallback matches = %v, want [1] (the user-less row)", got)
	}
}
