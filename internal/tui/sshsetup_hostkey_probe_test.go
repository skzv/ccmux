package tui

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/sshsetup"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// seedStaleKnownHost writes a known_hosts under a temp HOME holding a
// stale entry for sputnik, so RemoveKnownHostEntries has work to do.
func seedStaleKnownHost(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "known_hosts"), []byte("sputnik ssh-ed25519 OLDKEY\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// probeSequence returns a probe seam that yields results in order,
// repeating the last one.
func probeSequence(results ...sshsetup.ProbeResult) func(context.Context, sshsetup.Target) sshsetup.ProbeResult {
	i := 0
	return func(context.Context, sshsetup.Target) sshsetup.ProbeResult {
		r := results[i]
		if i < len(results)-1 {
			i++
		}
		return r
	}
}

// TestSSHWizard_HostKeyMismatchFromProbe_NeverInstallsEmptyPassword —
// regression: a host-key mismatch reported by the pre-password re-probe
// lands on the mismatch step before any password is typed, and "y"
// (remove + retry) then ran the install with an empty password. It must
// re-probe instead and, when key auth still fails, ask for the password.
func TestSSHWizard_HostKeyMismatchFromProbe_NeverInstallsEmptyPassword(t *testing.T) {
	seedStaleKnownHost(t)
	var installPasswords []string
	m := newSSHWizard(styles.Default())
	m.keyFn = fakeKey
	m.enumerateFn = fakeEnumerateNone
	m.installFn = func(_ context.Context, _ sshsetup.Target, pw string, _ sshsetup.LocalKey, _ sshsetup.Progress) error {
		installPasswords = append(installPasswords, pw)
		return nil
	}
	m.probeFn = probeSequence(sshsetup.ProbeHostKeyMismatch, sshsetup.ProbeAuthFailed)

	m, probeCmd := wizardAtProbing(t, m, sshsetup.Target{User: "alice", Host: "sputnik", Port: 22})
	m, _ = m.Update(runCmd(probeCmd))
	if m.Step() != sshWizardHostKeyMismatch {
		t.Fatalf("setup: step = %v, want sshWizardHostKeyMismatch", m.Step())
	}

	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m, _ = m.Update(runCmd(cmd))
	if len(installPasswords) != 0 {
		t.Fatalf("install ran with password(s) %q before the user typed one", installPasswords)
	}
	if m.Step() != sshWizardPassword {
		t.Errorf("after remove + re-probe (auth failed) step = %v, want sshWizardPassword", m.Step())
	}
}

// TestSSHWizard_HostKeyMismatchFromProbe_KeyAuthNowWorks — once the
// stale key is gone the re-probe may find key auth already works; the
// wizard then finishes without ever asking for a password.
func TestSSHWizard_HostKeyMismatchFromProbe_KeyAuthNowWorks(t *testing.T) {
	seedStaleKnownHost(t)
	installs := 0
	m := newSSHWizard(styles.Default())
	m.keyFn = fakeKey
	m.enumerateFn = fakeEnumerateNone
	m.installFn = func(context.Context, sshsetup.Target, string, sshsetup.LocalKey, sshsetup.Progress) error {
		installs++
		return nil
	}
	m.probeFn = probeSequence(sshsetup.ProbeHostKeyMismatch, sshsetup.ProbeOK)

	m, probeCmd := wizardAtProbing(t, m, sshsetup.Target{User: "alice", Host: "sputnik", Port: 22})
	m, _ = m.Update(runCmd(probeCmd))
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m, cmd = m.Update(runCmd(cmd)) // re-probe: OK → enumerate
	m, _ = m.Update(runCmd(cmd))   // enumerate(none) → done
	if m.Step() != sshWizardDone {
		t.Errorf("step = %v, want sshWizardDone", m.Step())
	}
	if installs != 0 {
		t.Errorf("install ran %d time(s); key auth already worked", installs)
	}
}
