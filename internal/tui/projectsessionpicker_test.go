package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/tui/styles"
)

func newTestPicker(t *testing.T) projectSessionPickerModel {
	t.Helper()
	return newProjectSessionPicker(styles.Default(), "c-myproject", "myproject", "/code/myproject", "c-myproject-2")
}

// TestProjectSessionPicker_DefaultsToRejoin — newly opened picker should
// default to "Rejoin" so the common case (user fat-fingered Enter a second
// time) doesn't accidentally spawn a new session.
func TestProjectSessionPicker_DefaultsToRejoin(t *testing.T) {
	p := newTestPicker(t)
	if p.actionIdx != 0 {
		t.Errorf("actionIdx = %d, want 0 (Rejoin)", p.actionIdx)
	}
}

// TestProjectSessionPicker_LeftRightSwitchesAction — ←/→ on the action row
// toggles between Rejoin and Start new.
func TestProjectSessionPicker_LeftRightSwitchesAction(t *testing.T) {
	p := newTestPicker(t)
	// Right → Start new
	p, _ = p.Update(keyMsg("right"))
	if p.actionIdx != 1 {
		t.Errorf("after right: actionIdx = %d, want 1 (Start new)", p.actionIdx)
	}
	// Right again → wraps back to Rejoin
	p, _ = p.Update(keyMsg("right"))
	if p.actionIdx != 0 {
		t.Errorf("after second right: actionIdx = %d, want 0 (Rejoin)", p.actionIdx)
	}
	// Left → Start new
	p, _ = p.Update(keyMsg("left"))
	if p.actionIdx != 1 {
		t.Errorf("after left: actionIdx = %d, want 1 (Start new)", p.actionIdx)
	}
}

// TestProjectSessionPicker_LeftRightIgnoredOnNameField — ←/→ moves the text
// cursor when the name field is focused, not the action picker.
func TestProjectSessionPicker_LeftRightIgnoredOnNameField(t *testing.T) {
	p := newTestPicker(t)
	// Tab to name field.
	p, _ = p.Update(keyMsg("tab"))
	if p.focus != pickFocusName {
		t.Fatalf("after tab focus = %d, want %d (name)", p.focus, pickFocusName)
	}
	origAction := p.actionIdx
	p, _ = p.Update(keyMsg("right"))
	if p.actionIdx != origAction {
		t.Error("right key on name field changed actionIdx; should only move text cursor")
	}
}

// TestProjectSessionPicker_TabCyclesFocus — Tab steps through action → name → action.
func TestProjectSessionPicker_TabCyclesFocus(t *testing.T) {
	p := newTestPicker(t)
	if p.focus != pickFocusAction {
		t.Fatalf("initial focus = %d, want %d (action)", p.focus, pickFocusAction)
	}
	p, _ = p.Update(keyMsg("tab"))
	if p.focus != pickFocusName {
		t.Errorf("after first tab focus = %d, want %d (name)", p.focus, pickFocusName)
	}
	p, _ = p.Update(keyMsg("tab"))
	if p.focus != pickFocusAction {
		t.Errorf("after second tab focus = %d, want %d (action)", p.focus, pickFocusAction)
	}
}

// TestProjectSessionPicker_RejoinEnterSubmitsRejoin — Enter with Rejoin
// selected emits projectSessionPickMsg{Action: "rejoin"}.
func TestProjectSessionPicker_RejoinEnterSubmitsRejoin(t *testing.T) {
	p := newTestPicker(t) // defaults to Rejoin
	_, cmd := p.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("Enter on Rejoin produced no cmd")
	}
	msg := cmd()
	pick, ok := msg.(projectSessionPickMsg)
	if !ok {
		t.Fatalf("msg = %T, want projectSessionPickMsg", msg)
	}
	if pick.Action != "rejoin" {
		t.Errorf("Action = %q, want rejoin", pick.Action)
	}
	if pick.Existing != "c-myproject" {
		t.Errorf("Existing = %q, want c-myproject", pick.Existing)
	}
}

// TestProjectSessionPicker_NewEnterCarriesName — "Start new" + Enter emits
// projectSessionPickMsg{Action: "new", NewName: <typed>}.
func TestProjectSessionPicker_NewEnterCarriesName(t *testing.T) {
	p := newTestPicker(t)
	// Switch to "Start new".
	p, _ = p.Update(keyMsg("right"))
	// Tab to name field and type a custom name.
	p, _ = p.Update(keyMsg("tab"))
	// Clear the pre-filled value and type a new one.
	p.nameInput.SetValue("c-myproject-custom")
	// Enter submits.
	_, cmd := p.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("Enter on Start new produced no cmd")
	}
	msg := cmd()
	pick, ok := msg.(projectSessionPickMsg)
	if !ok {
		t.Fatalf("msg = %T, want projectSessionPickMsg", msg)
	}
	if pick.Action != "new" {
		t.Errorf("Action = %q, want new", pick.Action)
	}
	if pick.NewName != "c-myproject-custom" {
		t.Errorf("NewName = %q, want c-myproject-custom", pick.NewName)
	}
}

// TestProjectSessionPicker_NewEnterRejectsEmptyName — if the name field is
// blank, Enter on "Start new" must set an error and NOT emit a submit msg.
func TestProjectSessionPicker_NewEnterRejectsEmptyName(t *testing.T) {
	p := newTestPicker(t)
	p, _ = p.Update(keyMsg("right")) // Switch to Start new
	p, _ = p.Update(keyMsg("tab"))   // Move to name field
	p.nameInput.SetValue("")
	p2, cmd := p.Update(keyMsg("enter"))
	if cmd != nil {
		msg := cmd()
		if _, ok := msg.(projectSessionPickMsg); ok {
			t.Error("empty name should not emit projectSessionPickMsg")
		}
	}
	if p2.err == "" {
		t.Error("empty name should set p.err")
	}
}

// TestProjectSessionPicker_EscEmitsCancel.
func TestProjectSessionPicker_EscEmitsCancel(t *testing.T) {
	p := newTestPicker(t)
	_, cmd := p.Update(keyMsg("esc"))
	if cmd == nil {
		t.Fatal("Esc produced no cmd")
	}
	if _, ok := cmd().(projectSessionPickCancelMsg); !ok {
		t.Errorf("Esc emitted %T, want projectSessionPickCancelMsg", cmd())
	}
}

// TestApp_ProjectPickerInterceptsEnter — Enter while the session picker is open
// must not fire the global attach handler.
func TestApp_ProjectPickerInterceptsEnter(t *testing.T) {
	st := styles.Default()
	km := DefaultKeymap()
	a := App{
		styles:    st,
		keys:      km,
		screen:    ScreenProjects,
		sessionsM: newSessions(st, km),
		projectsM: newProjects(st, km),
		matrix:    newMatrix(),
	}
	// Open the session picker.
	p := newProjectSessionPicker(st, "c-myproject", "myproject", "/code/myproject", "c-myproject-2")
	a.projectsM.picker = &p

	// Enter should be routed to the picker, not the global attach handler.
	m, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	a2 := m.(App)

	// The picker should still be set (it doesn't nil itself on Enter — App does).
	if a2.projectsM.picker == nil {
		t.Error("picker was cleared by App on Enter — only the submit handler should clear it")
	}
	// A command should have been produced (the picker's submit).
	if cmd == nil {
		t.Fatal("Enter on picker produced no cmd")
	}
	// The command should yield a projectSessionPickMsg.
	msg := cmd()
	if _, ok := msg.(projectSessionPickMsg); !ok {
		t.Errorf("cmd() = %T, want projectSessionPickMsg", msg)
	}
}
