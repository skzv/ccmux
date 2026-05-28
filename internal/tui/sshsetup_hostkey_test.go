package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/sshsetup"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// TestSSHWizard_HostKeyMismatch_RoutesToDedicatedStep — when
// install fails with ErrHostKeyMismatch, the wizard must land on
// sshWizardHostKeyMismatch (NOT the generic Error step) so the
// user gets the one-keystroke recovery affordance.
func TestSSHWizard_HostKeyMismatch_RoutesToDedicatedStep(t *testing.T) {
	m := newSSHWizard(styles.Default())
	m.installFn = fakeInstallHostKeyMismatch
	m.keyFn = fakeKey
	m.Open(sshsetup.Target{User: "alice", Host: "sputnik", Port: 22}, nil)
	m = advanceUserToPassword(t, m)
	for _, r := range "hunter2" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = m.Update(runCmd(cmd))
	if m.Step() != sshWizardHostKeyMismatch {
		t.Fatalf("Step = %v, want sshWizardHostKeyMismatch", m.Step())
	}
	// Rendered view should mention the host AND the remediation.
	v := m.View(100, 30)
	if !strings.Contains(v, "sputnik") {
		t.Errorf("view missing host name; got %q", firstN(v, 200))
	}
	if !strings.Contains(v, "remove + retry") {
		t.Errorf("view missing remediation hint; got %q", firstN(v, 200))
	}
}

// TestSSHWizard_HostKeyMismatch_YRemovesAndRetries — pressing 'y'
// on the mismatch step removes the stale known_hosts line and
// re-runs the install with the password already in memory. This
// is the whole UX improvement — the user doesn't have to drop to
// a shell and run ssh-keygen -R manually.
func TestSSHWizard_HostKeyMismatch_YRemovesAndRetries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(sshDir, "known_hosts"),
		[]byte("sputnik ssh-ed25519 OLDKEY\nelsewhere ssh-rsa OTHER\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	// Use a counting install fn — first call returns mismatch,
	// second succeeds. Lets us assert that 'y' actually triggers
	// a NEW install call (not just clears the stale entry and
	// stops).
	calls := 0
	m := newSSHWizard(styles.Default())
	m.installFn = func(ctx context.Context, tgt sshsetup.Target, password string, key sshsetup.LocalKey, p sshsetup.Progress) error {
		calls++
		if calls == 1 {
			return sshsetup.ErrHostKeyMismatch
		}
		return nil
	}
	m.enumerateFn = fakeEnumerateNone
	m.keyFn = fakeKey
	m.Open(sshsetup.Target{User: "alice", Host: "sputnik", Port: 22}, nil)
	m = advanceUserToPassword(t, m)
	for _, r := range "hunter2" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = m.Update(runCmd(cmd))
	if m.Step() != sshWizardHostKeyMismatch {
		t.Fatalf("Step = %v, want sshWizardHostKeyMismatch", m.Step())
	}

	// Press 'y' → remove entry + retry install.
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if m.Step() != sshWizardRunning {
		t.Fatalf("Step after y = %v, want sshWizardRunning", m.Step())
	}
	// Drive the new install goroutine to completion.
	m, cmd = m.Update(runCmd(cmd)) // install done → enumerate
	m, _ = m.Update(runCmd(cmd))   // enumerate done → done

	if m.Step() != sshWizardDone {
		t.Fatalf("Step after retry = %v, want sshWizardDone", m.Step())
	}
	if calls != 2 {
		t.Errorf("installFn calls = %d, want 2 (initial + retry)", calls)
	}
	// known_hosts should have the stale entry gone but the
	// unrelated row preserved.
	data, _ := os.ReadFile(filepath.Join(sshDir, "known_hosts"))
	if strings.Contains(string(data), "OLDKEY") {
		t.Errorf("OLDKEY still in known_hosts:\n%s", data)
	}
	if !strings.Contains(string(data), "elsewhere") {
		t.Errorf("unrelated entry incorrectly removed:\n%s", data)
	}
}

// TestSSHWizard_HostKeyMismatch_NCancels — 'n' or Esc bails
// without touching known_hosts. Important for the "I don't
// recognize this change, let me investigate" path.
func TestSSHWizard_HostKeyMismatch_NCancels(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sshDir := filepath.Join(home, ".ssh")
	_ = os.MkdirAll(sshDir, 0o700)
	khPath := filepath.Join(sshDir, "known_hosts")
	original := []byte("sputnik ssh-ed25519 OLDKEY\n")
	_ = os.WriteFile(khPath, original, 0o644)

	m := newSSHWizard(styles.Default())
	m.installFn = fakeInstallHostKeyMismatch
	m.keyFn = fakeKey
	m.Open(sshsetup.Target{User: "alice", Host: "sputnik", Port: 22}, nil)
	m = advanceUserToPassword(t, m)
	for _, r := range "p" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = m.Update(runCmd(cmd))

	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	msg := runCmd(cmd)
	if _, ok := msg.(wizardCancelledMsg); !ok {
		t.Fatalf("expected wizardCancelledMsg from 'n', got %T", msg)
	}
	// known_hosts must be untouched by a cancel.
	got, _ := os.ReadFile(khPath)
	if string(got) != string(original) {
		t.Errorf("known_hosts mutated by cancel:\n  was: %q\n  now: %q", original, got)
	}
}

func fakeInstallHostKeyMismatch(ctx context.Context, t sshsetup.Target, password string, key sshsetup.LocalKey, p sshsetup.Progress) error {
	return sshsetup.ErrHostKeyMismatch
}
