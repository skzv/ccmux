package tui

import (
	"testing"

	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tui/styles"
)

func newTestRenameForm(t *testing.T, name string) renameFormModel {
	t.Helper()
	return newRenameForm(styles.Default(), name)
}

// TestRenameForm_EnterSubmitsTrimmedName — Enter emits renameSessionSubmitMsg
// with whitespace trimmed from the new name.
func TestRenameForm_EnterSubmitsTrimmedName(t *testing.T) {
	f := newTestRenameForm(t, "c-old")
	f.input.SetValue("  c-new  ")
	_, cmd := f.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("Enter produced no cmd")
	}
	msg := cmd()
	submit, ok := msg.(renameSessionSubmitMsg)
	if !ok {
		t.Fatalf("msg = %T, want renameSessionSubmitMsg", msg)
	}
	if submit.OldName != "c-old" {
		t.Errorf("OldName = %q, want c-old", submit.OldName)
	}
	if submit.NewName != "c-new" {
		t.Errorf("NewName = %q, want c-new (trimmed)", submit.NewName)
	}
}

// TestRenameForm_EmptyNameRejected — blank name must set err and NOT emit submit.
func TestRenameForm_EmptyNameRejected(t *testing.T) {
	f := newTestRenameForm(t, "c-old")
	f.input.SetValue("   ")
	f2, cmd := f.Update(keyMsg("enter"))
	if cmd != nil {
		msg := cmd()
		if _, ok := msg.(renameSessionSubmitMsg); ok {
			t.Error("blank name emitted renameSessionSubmitMsg; expected rejection")
		}
	}
	if f2.err == "" {
		t.Error("blank name should set f.err")
	}
}

// TestRenameForm_SameNameEmitsCancel — renaming to the same name should
// dismiss without a round-trip to tmux (cancel, not submit).
func TestRenameForm_SameNameEmitsCancel(t *testing.T) {
	f := newTestRenameForm(t, "c-same")
	f.input.SetValue("c-same")
	_, cmd := f.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("Enter on same-name produced no cmd")
	}
	if _, ok := cmd().(renameSessionCancelMsg); !ok {
		t.Errorf("same-name Enter emitted %T, want renameSessionCancelMsg", cmd())
	}
}

// TestRenameForm_EscEmitsCancel.
func TestRenameForm_EscEmitsCancel(t *testing.T) {
	f := newTestRenameForm(t, "c-old")
	_, cmd := f.Update(keyMsg("esc"))
	if cmd == nil {
		t.Fatal("Esc produced no cmd")
	}
	if _, ok := cmd().(renameSessionCancelMsg); !ok {
		t.Errorf("Esc emitted %T, want renameSessionCancelMsg", cmd())
	}
}

// TestApp_RenameFormInterceptsEnter — Enter inside the rename form must not
// fire the global attach-session handler on the Sessions screen.
func TestApp_RenameFormInterceptsEnter(t *testing.T) {
	st := styles.Default()
	km := DefaultKeymap()
	a := App{
		styles:    st,
		keys:      km,
		screen:    ScreenSessions,
		sessionsM: newSessions(st, km),
		projectsM: newProjects(st, km),
		matrix:    newMatrix(),
	}
	// Seed a session so attachSelectedSession has somewhere to land.
	a.sessionsM.SetSessions([]daemon.SessionState{{Name: "c-ccmux", Host: "local"}})

	// Open rename form with a different name in the input to force submit.
	rf := newRenameForm(st, "c-ccmux")
	rf.input.SetValue("c-ccmux-renamed")
	a.sessionsM.renameForm = &rf

	m, cmd := a.Update(keyMsg("enter"))
	a2 := m.(App)
	if cmd == nil {
		t.Fatal("Enter on rename form produced no cmd")
	}
	msg := cmd()
	if _, ok := msg.(renameSessionSubmitMsg); !ok {
		t.Fatalf("Enter on rename form emitted %T, want renameSessionSubmitMsg", msg)
	}
	// The form should still be set (App's submit handler clears it).
	if a2.sessionsM.renameForm == nil {
		t.Error("rename form was cleared prematurely; only the submit handler should clear it")
	}
}

// TestApp_RenameFormInterceptsDigitKeys — digit keys while the rename form is
// open must not switch screens.
func TestApp_RenameFormInterceptsDigitKeys(t *testing.T) {
	st := styles.Default()
	km := DefaultKeymap()
	a := App{
		styles:    st,
		keys:      km,
		screen:    ScreenSessions,
		sessionsM: newSessions(st, km),
		projectsM: newProjects(st, km),
		matrix:    newMatrix(),
	}
	rf := newRenameForm(st, "c-ccmux")
	a.sessionsM.renameForm = &rf

	m, _ := a.Update(keyMsg("1"))
	a2 := m.(App)
	if a2.screen != ScreenSessions {
		t.Errorf("digit key while rename form open switched to screen %v; expected ScreenSessions", a2.screen)
	}
}

// TestApp_SessionRenamedMsgTriggersRefresh — a successful sessionRenamedMsg
// must produce a refresh command and a toast.
func TestApp_SessionRenamedMsgTriggersRefresh(t *testing.T) {
	st := styles.Default()
	km := DefaultKeymap()
	a := App{
		styles:    st,
		keys:      km,
		screen:    ScreenSessions,
		sessionsM: newSessions(st, km),
		projectsM: newProjects(st, km),
		matrix:    newMatrix(),
	}
	m, cmd := a.Update(sessionRenamedMsg{OldName: "c-old", NewName: "c-new", Err: nil})
	a2 := m.(App)
	if cmd == nil {
		t.Error("sessionRenamedMsg{Err:nil} produced no refresh cmd")
	}
	if !a2.toasts.Active() {
		t.Error("sessionRenamedMsg{Err:nil} produced no toast")
	}
}
