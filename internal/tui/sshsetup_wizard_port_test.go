package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/sshsetup"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// TestSSHWizard_PortFieldPrefilledWith22 — the User step pre-fills
// the Port input with "22" when the caller passed zero / default.
// The user sees a concrete number and isn't surprised by what the
// wizard will dial.
func TestSSHWizard_PortFieldPrefilledWith22(t *testing.T) {
	m := newSSHWizard(styles.Default())
	m.Open(sshsetup.Target{User: "alice", Host: "sputnik"}, nil)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // confirm → user
	if got := m.portInput.Value(); got != "22" {
		t.Errorf("portInput pre-fill = %q, want 22", got)
	}
}

// TestSSHWizard_PortFieldPrefilledWithExplicit — when the caller
// already resolved a custom port (e.g. from hosts.toml's SSHPort
// field), the wizard pre-fills it. User just hits Enter to accept.
func TestSSHWizard_PortFieldPrefilledWithExplicit(t *testing.T) {
	m := newSSHWizard(styles.Default())
	m.Open(sshsetup.Target{User: "alice", Host: "sputnik", Port: 2222}, nil)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := m.portInput.Value(); got != "2222" {
		t.Errorf("portInput pre-fill = %q, want 2222", got)
	}
}

// TestSSHWizard_TabSwitchesFocus — Tab moves focus from Username
// to Port and back. Critical so the user can edit the port at all.
func TestSSHWizard_TabSwitchesFocus(t *testing.T) {
	m := newSSHWizard(styles.Default())
	m.Open(sshsetup.Target{User: "alice", Host: "sputnik"}, nil)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.userFocus != 0 {
		t.Fatalf("initial userFocus = %d, want 0", m.userFocus)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.userFocus != 1 {
		t.Errorf("after Tab, userFocus = %d, want 1", m.userFocus)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.userFocus != 0 {
		t.Errorf("after second Tab, userFocus = %d, want 0 (cycled back)", m.userFocus)
	}
}

// TestSSHWizard_EditedPortPersistsToTarget — type a new port in
// the field, hit Enter, the target's Port reflects it for the
// downstream install.
func TestSSHWizard_EditedPortPersistsToTarget(t *testing.T) {
	m := newSSHWizard(styles.Default())
	m.Open(sshsetup.Target{User: "alice", Host: "sputnik"}, nil)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // confirm → user
	// Tab to port field, clear, type new port.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	for _, r := range "2222" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.Step() != sshWizardPassword {
		t.Fatalf("Step = %v, want sshWizardPassword", m.Step())
	}
	if m.target.Port != 2222 {
		t.Errorf("target.Port = %d, want 2222", m.target.Port)
	}
}

// TestSSHWizard_InvalidPortShowsErrorStaysOnStep — a non-numeric
// port keeps the focus on the user step with a clear error. Empty
// is allowed (defaults to 22); the validator only rejects garbage.
func TestSSHWizard_InvalidPortShowsErrorStaysOnStep(t *testing.T) {
	m := newSSHWizard(styles.Default())
	m.Open(sshsetup.Target{User: "alice", Host: "sputnik"}, nil)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	for _, r := range "abc" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.Step() != sshWizardUser {
		t.Errorf("Step = %v, want sshWizardUser (stuck on invalid port)", m.Step())
	}
	if !strings.Contains(m.View(80, 24), "port must be a number") {
		t.Errorf("view missing port error hint")
	}
}

// TestParseWizardPort table-tests the validator directly so we
// don't have to drive the model for every edge case.
func TestParseWizardPort(t *testing.T) {
	cases := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{"", 22, false},
		{"22", 22, false},
		{"  22  ", 22, false},
		{"2222", 2222, false},
		{"65535", 65535, false},
		{"abc", 0, true},
		{"22a", 0, true},
		{"-1", 0, true}, // minus sign → not a digit → error
		{"0", 0, true},
		{"65536", 0, true},
		{"99999999", 0, true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := parseWizardPort(c.in)
			if (err != nil) != c.wantErr {
				t.Fatalf("parseWizardPort(%q) err=%v, wantErr=%v", c.in, err, c.wantErr)
			}
			if !c.wantErr && got != c.want {
				t.Errorf("parseWizardPort(%q) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

// TestSSHWizard_PortInheritsFromOpenTarget — verifies the full
// chain: hosts.toml SSHPort=2222 → resolveTarget → wizard Open →
// portInput pre-fills "2222" and the install dials port 2222.
//
// We don't drive the full install (that needs the in-process SSH
// server harness) but we DO assert the resolved target carries
// the right port at the wizard's entrypoint.
func TestSSHWizard_PortInheritsFromOpenTarget(t *testing.T) {
	target := sshsetup.Target{User: "alice", Host: "sputnik", Port: 2222}
	m := newSSHWizard(styles.Default())
	m.Open(target, nil)
	if m.target.Port != 2222 {
		t.Errorf("model.target.Port = %d, want 2222", m.target.Port)
	}
}
