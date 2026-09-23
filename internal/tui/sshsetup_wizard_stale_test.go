package tui

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/sshsetup"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// wizardAtProbing opens a wizard and drives it confirm → user → probing,
// returning the model and the (not yet run) probe command.
func wizardAtProbing(t *testing.T, m *sshWizardModel, target sshsetup.Target) (*sshWizardModel, tea.Cmd) {
	t.Helper()
	m.Open(target, nil)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})    // confirm → user
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // user → probing
	if m.Step() != sshWizardProbing {
		t.Fatalf("setup: step = %v, want sshWizardProbing", m.Step())
	}
	return m, cmd
}

// TestSSHWizard_LateProbeAfterEscStaysClosed — regression: Esc during
// the re-probe closed the wizard, but the probe result still arrived
// later and was applied with no step check, flipping the wizard back
// onto the Password screen the user had just dismissed.
func TestSSHWizard_LateProbeAfterEscStaysClosed(t *testing.T) {
	m := newSSHWizard(styles.Default())
	m.probeFn = fakeProbeAuthFailed
	m, probeCmd := wizardAtProbing(t, m, sshsetup.Target{User: "alice", Host: "sputnik"})

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.Active() {
		t.Fatal("Esc during probing should close the wizard")
	}
	m, _ = m.Update(runCmd(probeCmd)) // the probe finishes after the cancel
	if m.Active() {
		t.Fatalf("late probe result reopened the cancelled wizard at step %v", m.Step())
	}
}

// TestSSHWizard_LateInstallAfterEscStaysClosed — same for the install:
// a result (here: wrong password) landing after Esc must not bounce the
// closed wizard back to the Password step.
func TestSSHWizard_LateInstallAfterEscStaysClosed(t *testing.T) {
	m := newSSHWizard(styles.Default())
	m.installFn = fakeInstallWrongPassword
	m.keyFn = fakeKey
	m.Open(sshsetup.Target{User: "alice", Host: "sputnik"}, nil)
	m = advanceUserToPassword(t, m)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("pw")})
	m, installCmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.Step() != sshWizardRunning {
		t.Fatalf("setup: step = %v, want sshWizardRunning", m.Step())
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m, _ = m.Update(runCmd(installCmd))
	if m.Active() {
		t.Fatalf("late install result reopened the cancelled wizard at step %v", m.Step())
	}
}

// TestSSHWizard_EscCancelsInstallContext — Esc must actually stop the
// in-flight install (it used to run on a detached 60s context).
func TestSSHWizard_EscCancelsInstallContext(t *testing.T) {
	started := make(chan struct{})
	m := newSSHWizard(styles.Default())
	m.keyFn = fakeKey
	m.installFn = func(ctx context.Context, _ sshsetup.Target, _ string, _ sshsetup.LocalKey, _ sshsetup.Progress) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	m.Open(sshsetup.Target{User: "alice", Host: "sputnik"}, nil)
	m = advanceUserToPassword(t, m)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("pw")})
	m, installCmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	done := make(chan tea.Msg, 1)
	go func() { done <- installCmd() }()
	<-started
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	select {
	case msg := <-done:
		res, ok := msg.(wizardInstallDoneMsg)
		if !ok || !errors.Is(res.err, context.Canceled) {
			t.Errorf("install ended with %#v, want context.Canceled", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Esc did not cancel the in-flight install")
	}
}

// TestSSHWizard_ResultFromPreviousOpeningDropped — cancel, re-open the
// wizard for the same host and start a fresh probe: the first run's
// probe result must not drive the second run.
func TestSSHWizard_ResultFromPreviousOpeningDropped(t *testing.T) {
	target := sshsetup.Target{User: "alice", Host: "sputnik"}
	m := newSSHWizard(styles.Default())
	m.probeFn = fakeProbeAuthFailed
	m, oldProbe := wizardAtProbing(t, m, target)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	m.probeFn = fakeProbeOK
	m.enumerateFn = fakeEnumerateNone
	m.keyFn = fakeKey
	m, newProbe := wizardAtProbing(t, m, target)

	m, _ = m.Update(runCmd(oldProbe)) // stale AuthFailed from the first opening
	if m.Step() != sshWizardProbing {
		t.Fatalf("stale probe result moved the new run to %v", m.Step())
	}
	m, cmd := m.Update(runCmd(newProbe)) // the current run's ProbeOK
	m, _ = m.Update(runCmd(cmd))         // enumerate(none) → done
	if m.Step() != sshWizardDone {
		t.Errorf("current run ended at %v, want sshWizardDone", m.Step())
	}
}

// TestApp_SSHWizardLateProbeAfterEscStaysClosed — the same regression
// through the App router: it forwards wizard results whenever a wizard
// model exists, so the guard has to hold end to end.
func TestApp_SSHWizardLateProbeAfterEscStaysClosed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := New(config.Defaults(), "test")
	a.tour.Close()
	model, _ := a.Update(openSSHWizardMsg{target: sshsetup.Target{User: "alice", Host: "sputnik", Port: 22}})
	a = model.(App)
	a.sshWizard.probeFn = fakeProbeAuthFailed

	model, _ = a.Update(tea.KeyMsg{Type: tea.KeyEnter}) // confirm → user
	a = model.(App)
	model, probeCmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter}) // user → probing
	a = model.(App)
	if a.sshWizard.Step() != sshWizardProbing {
		t.Fatalf("setup: wizard step = %v, want probing", a.sshWizard.Step())
	}
	model, _ = a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	a = model.(App)
	if a.sshWizard.Active() {
		t.Fatal("Esc should close the wizard")
	}

	model, _ = a.Update(runCmd(probeCmd))
	a = model.(App)
	if a.sshWizard.Active() {
		t.Fatalf("late probe result reopened the wizard (step %v)", a.sshWizard.Step())
	}
}
