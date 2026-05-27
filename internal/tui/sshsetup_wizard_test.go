package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/sshsetup"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// TestSSHWizard_ZeroValue_Inert — an uninitialized wizard renders
// nothing and absorbs no input. Critical because the root app
// embeds the wizard unconditionally; an "always-active" zero value
// would shadow every keypress.
func TestSSHWizard_ZeroValue_Inert(t *testing.T) {
	var m sshWizardModel
	if m.Active() {
		t.Fatal("zero wizard must report Active() == false")
	}
	if v := m.View(80, 24); v != "" {
		t.Fatalf("zero wizard must render empty; got %q", v[:min(40, len(v))])
	}
}

// TestSSHWizard_OpenStartsAtConfirm pins the entry slide.
func TestSSHWizard_OpenStartsAtConfirm(t *testing.T) {
	m := newSSHWizard(styles.Default())
	m.Open(sshsetup.Target{User: "alice", Host: "sputnik"}, "resume-payload")
	if !m.Active() {
		t.Fatal("Open must activate")
	}
	if m.Step() != sshWizardConfirm {
		t.Errorf("Step = %v, want sshWizardConfirm", m.Step())
	}
	v := m.View(80, 24)
	if !strings.Contains(v, "alice@sputnik") {
		t.Errorf("confirm view missing target; view=%q", firstN(v, 200))
	}
	if !strings.Contains(v, "[Enter] continue") {
		t.Errorf("confirm view missing key hint")
	}
}

// TestSSHWizard_ConfirmEnterAdvancesToPassword exercises the
// happy-path advance through the confirm step.
func TestSSHWizard_ConfirmEnterAdvancesToPassword(t *testing.T) {
	m := newSSHWizard(styles.Default())
	m.Open(sshsetup.Target{User: "alice", Host: "sputnik"}, nil)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.Step() != sshWizardPassword {
		t.Errorf("after Enter on confirm, Step = %v, want sshWizardPassword", m.Step())
	}
}

// TestSSHWizard_EscOnConfirmCancels — Esc from the very first
// slide produces a cancel message with the resume payload intact.
func TestSSHWizard_EscOnConfirmCancels(t *testing.T) {
	m := newSSHWizard(styles.Default())
	m.Open(sshsetup.Target{User: "alice", Host: "sputnik"}, "carry-me")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	msg := runCmd(cmd)
	cm, ok := msg.(wizardCancelledMsg)
	if !ok {
		t.Fatalf("expected wizardCancelledMsg, got %T", msg)
	}
	if cm.resume != "carry-me" {
		t.Errorf("resume payload = %v, want carry-me", cm.resume)
	}
}

// TestSSHWizard_EmptyPasswordStays — Enter with no password keeps
// us on the password step and shows a hint instead of advancing.
func TestSSHWizard_EmptyPasswordStays(t *testing.T) {
	m := makeOpenAt(t, sshWizardPassword, sshsetup.Target{User: "alice", Host: "sputnik"})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.Step() != sshWizardPassword {
		t.Errorf("Step = %v, want sshWizardPassword (empty password)", m.Step())
	}
	if !strings.Contains(m.View(80, 24), "password is required") {
		t.Errorf("empty-password hint missing from view")
	}
}

// TestSSHWizard_PasswordFlowOK — type a password, Enter, install
// succeeds, enumerate returns nothing → Done. Drives the full
// happy path through a faked installer.
func TestSSHWizard_PasswordFlowOK(t *testing.T) {
	m := newSSHWizard(styles.Default())
	m.installFn = fakeInstallOK
	m.enumerateFn = fakeEnumerateNone
	m.keyFn = fakeKey

	m.Open(sshsetup.Target{User: "alice", Host: "sputnik"}, nil)
	// confirm → password
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	// type password
	for _, r := range "hunter2" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	// submit
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.Step() != sshWizardRunning {
		t.Fatalf("Step = %v, want sshWizardRunning right after Enter", m.Step())
	}
	// run install goroutine; it returns InstallDoneMsg
	doneMsg := runCmd(cmd)
	m, cmd = m.Update(doneMsg)
	// We expect to be on startEnumerate's enqueue path now.
	if m.Step() != sshWizardRunning {
		t.Logf("step after install done: %v", m.Step())
	}
	// run the enumerate goroutine
	enumMsg := runCmd(cmd)
	m, _ = m.Update(enumMsg)
	if m.Step() != sshWizardDone {
		t.Fatalf("Step = %v, want sshWizardDone after enumerate(none)", m.Step())
	}
}

// TestSSHWizard_WrongPasswordReprompts — install returns
// ErrWrongPassword → wizard bounces back to the password screen
// with a visible error, password input is cleared.
func TestSSHWizard_WrongPasswordReprompts(t *testing.T) {
	m := newSSHWizard(styles.Default())
	m.installFn = fakeInstallWrongPassword
	m.keyFn = fakeKey
	m.Open(sshsetup.Target{User: "alice", Host: "sputnik"}, nil)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // confirm → password
	for _, r := range "bad" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // submit → running
	doneMsg := runCmd(cmd)
	m, _ = m.Update(doneMsg)
	if m.Step() != sshWizardPassword {
		t.Fatalf("Step = %v, want sshWizardPassword (after wrong-password retry)", m.Step())
	}
	if !strings.Contains(m.View(80, 24), "password rejected") {
		t.Errorf("retry view missing 'password rejected' hint")
	}
	// And the field must be cleared so the user types fresh.
	if got := m.passwd.Value(); got != "" {
		t.Errorf("passwd.Value() = %q, want empty after retry", got)
	}
}

// TestSSHWizard_ErrorStateRetry — non-password failure → error
// state; pressing 'r' bounces back to password (which is what we
// promise in the key hint).
func TestSSHWizard_ErrorStateRetry(t *testing.T) {
	m := newSSHWizard(styles.Default())
	m.installFn = fakeInstallGenericErr
	m.keyFn = fakeKey
	m.Open(sshsetup.Target{User: "alice", Host: "sputnik"}, nil)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	for _, r := range "x" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = m.Update(runCmd(cmd))
	if m.Step() != sshWizardError {
		t.Fatalf("Step = %v, want sshWizardError", m.Step())
	}
	// Press 'r' to retry → back to password.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if m.Step() != sshWizardPassword {
		t.Errorf("after 'r', Step = %v, want sshWizardPassword", m.Step())
	}
}

// TestSSHWizard_EnumerateSelection — install succeeds, enumerate
// returns two users; user toggles one, hits Enter, the completed
// message carries the picked user.
func TestSSHWizard_EnumerateSelection(t *testing.T) {
	m := newSSHWizard(styles.Default())
	m.installFn = fakeInstallOK
	m.enumerateFn = fakeEnumerateBobCarol
	m.keyFn = fakeKey
	m.Open(sshsetup.Target{User: "alice", Host: "sputnik"}, nil)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // confirm
	for _, r := range "p" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // submit install
	m, cmd = m.Update(runCmd(cmd))                     // install done → enumerate cmd
	m, _ = m.Update(runCmd(cmd))                       // enumerate done → enumerate step
	if m.Step() != sshWizardEnumerate {
		t.Fatalf("Step = %v, want sshWizardEnumerate", m.Step())
	}
	// Toggle the first user (cursor starts at 0 → "bob").
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	// Enter → done; emitCompleted carries `added=[bob]`.
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	msg := runCmd(cmd)
	completed, ok := msg.(wizardCompletedMsg)
	if !ok {
		t.Fatalf("expected wizardCompletedMsg, got %T", msg)
	}
	if !equalStrings(completed.added, []string{"bob"}) {
		t.Errorf("added = %v, want [bob]", completed.added)
	}
}

// TestSSHWizard_EnumerateEscSkipsAdd — Esc on the enumerate screen
// completes WITHOUT adding any users (we promise this in the key
// hint). The key is already installed at this point so we still
// emit Completed, not Cancelled.
func TestSSHWizard_EnumerateEscSkipsAdd(t *testing.T) {
	m := newSSHWizard(styles.Default())
	m.installFn = fakeInstallOK
	m.enumerateFn = fakeEnumerateBobCarol
	m.keyFn = fakeKey
	m.Open(sshsetup.Target{User: "alice", Host: "sputnik"}, nil)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	for _, r := range "p" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m, cmd = m.Update(runCmd(cmd))
	m, _ = m.Update(runCmd(cmd))
	// Pre-toggle bob so we can prove Esc clears the selection.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	msg := runCmd(cmd)
	completed, ok := msg.(wizardCompletedMsg)
	if !ok {
		t.Fatalf("expected wizardCompletedMsg from Esc on enumerate, got %T", msg)
	}
	if len(completed.added) != 0 {
		t.Errorf("added = %v, want empty (Esc means 'skip')", completed.added)
	}
}

// TestSSHWizard_DoneEnterCompletes — final acknowledgement.
func TestSSHWizard_DoneEnterCompletes(t *testing.T) {
	m := newSSHWizard(styles.Default())
	m.step = sshWizardDone
	m.target = sshsetup.Target{User: "alice", Host: "sputnik"}
	m.resumeOnDone = "payload"
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	msg := runCmd(cmd)
	completed, ok := msg.(wizardCompletedMsg)
	if !ok {
		t.Fatalf("expected wizardCompletedMsg, got %T", msg)
	}
	if completed.resume != "payload" {
		t.Errorf("resume = %v, want payload", completed.resume)
	}
}

// TestSSHWizard_EnumerateSelectAllNone — 'a' selects everything,
// 'n' clears. Exercises the bulk-toggle ergonomics.
func TestSSHWizard_EnumerateSelectAllNone(t *testing.T) {
	m := newSSHWizard(styles.Default())
	m.step = sshWizardEnumerate
	m.others = []string{"bob", "carol", "dave"}
	m.selected = map[string]bool{}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	for _, u := range m.others {
		if !m.selected[u] {
			t.Errorf("after 'a', %s not selected", u)
		}
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	for _, u := range m.others {
		if m.selected[u] {
			t.Errorf("after 'n', %s still selected", u)
		}
	}
}

// TestWizardWrap — tiny safety net for the error-screen wrapper. We
// don't bother round-tripping every edge; just confirm it splits on
// spaces and respects the column cap.
func TestWizardWrap(t *testing.T) {
	got := wizardWrap("one two three four five", 10)
	want := []string{"one two", "three four", "five"}
	if !equalStrings(got, want) {
		t.Errorf("wizardWrap = %v, want %v", got, want)
	}
}

// --- helpers ---------------------------------------------------------

func fakeKey() (sshsetup.LocalKey, error) {
	return sshsetup.LocalKey{
		PrivatePath: "/dev/null",
		PublicPath:  "/dev/null",
		PublicLine:  "ssh-ed25519 AAAA fake@test",
	}, nil
}

func fakeInstallOK(ctx context.Context, t sshsetup.Target, password string, key sshsetup.LocalKey, p sshsetup.Progress) error {
	if p != nil {
		p("hostkey", "ok")
		p("done", "ok")
	}
	_ = ctx
	_ = password
	return nil
}

func fakeInstallWrongPassword(ctx context.Context, t sshsetup.Target, password string, key sshsetup.LocalKey, p sshsetup.Progress) error {
	_ = ctx
	_ = password
	return sshsetup.ErrWrongPassword
}

func fakeInstallGenericErr(ctx context.Context, t sshsetup.Target, password string, key sshsetup.LocalKey, p sshsetup.Progress) error {
	_ = ctx
	_ = password
	return errors.New("sshd refused to honor authorized_keys (something custom)")
}

func fakeEnumerateNone(ctx context.Context, t sshsetup.Target, key sshsetup.LocalKey) ([]string, error) {
	_ = ctx
	return nil, nil
}

func fakeEnumerateBobCarol(ctx context.Context, t sshsetup.Target, key sshsetup.LocalKey) ([]string, error) {
	_ = ctx
	return []string{"bob", "carol"}, nil
}

// makeOpenAt jumps the wizard into a specific step without driving
// the upstream transitions. Used to keep per-step tests focused.
func makeOpenAt(t *testing.T, step sshWizardStep, target sshsetup.Target) *sshWizardModel {
	t.Helper()
	m := newSSHWizard(styles.Default())
	m.Open(target, nil)
	m.step = step
	if step == sshWizardPassword {
		m.passwd.Focus()
	}
	return m
}

// runCmd materializes a Bubble Tea Cmd to its first message
// (synchronously). Used so tests can drive the model without a
// running tea.Program. We assume single-message Cmd which is what
// the wizard emits.
func runCmd(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	// Bubble Tea Cmds return a tea.Msg from running the function.
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(2 * time.Second):
		return nil
	}
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
