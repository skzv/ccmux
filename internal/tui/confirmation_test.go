package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tui/styles"
)

func newConfirmationTestApp() App {
	st := styles.Default()
	km := DefaultKeymap()
	return App{
		styles:    st,
		keys:      km,
		width:     100,
		height:    30,
		screen:    ScreenSessions,
		sessionsM: newSessions(st, km),
		projectsM: newProjects(st, km),
		matrix:    newMatrix(),
	}
}

func sendKey(t *testing.T, a App, key tea.KeyMsg) (App, tea.Cmd) {
	t.Helper()
	m, cmd := a.Update(key)
	return m.(App), cmd
}

func keyRunes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func commandContainsQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); ok {
		return true
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, subcmd := range batch {
			if subcmd == nil {
				continue
			}
			if _, ok := subcmd().(tea.QuitMsg); ok {
				return true
			}
		}
	}
	return false
}

func TestConfirmation_QuitKeyboardConfirmAndCancel(t *testing.T) {
	a := newConfirmationTestApp()

	a, cmd := sendKey(t, a, keyRunes("q"))
	if !a.confirm.open() || a.confirm.kind != confirmationQuit {
		t.Fatalf("q did not open quit confirmation: %#v", a.confirm)
	}
	if a.confirm.focus != confirmationFocusCancel {
		t.Fatalf("initial focus = %v, want cancel", a.confirm.focus)
	}
	if cmd == nil {
		t.Fatal("opening confirmation returned nil cmd; want mouse-enable cmd")
	}
	out := a.View()
	assertPresent(t, out, "Quit ccmux?", "Managed tmux sessions will keep running.", "Cancel", "Quit")
	assertNoOverflow(t, out, a.width)

	a, cmd = sendKey(t, a, keyRunes("n"))
	if a.confirm.open() {
		t.Fatal("n did not cancel quit confirmation")
	}
	if commandContainsQuit(cmd) {
		t.Fatal("cancel command quit ccmux")
	}

	a, _ = sendKey(t, a, keyRunes("q"))
	a, cmd = sendKey(t, a, keyRunes("y"))
	if a.confirm.open() {
		t.Fatal("y did not close quit confirmation")
	}
	if !commandContainsQuit(cmd) {
		t.Fatal("y did not return a quit command")
	}
}

func TestConfirmation_KillKeyboardCancelConfirmCapturedTargetAndNoSelection(t *testing.T) {
	a := newConfirmationTestApp()

	a, cmd := sendKey(t, a, keyRunes("x"))
	if a.confirm.open() {
		t.Fatal("x with no selected session opened confirmation")
	}
	if cmd != nil {
		t.Fatalf("x with no selected session returned cmd %T", cmd())
	}

	a.sessionsM.SetSessions([]daemon.SessionState{
		{Name: "c-alpha", Host: "local"},
		{Name: "c-beta", Host: "local"},
	})
	a = navigate(t, a, 1)
	if got := selName(a.sessionsM.Selected()); got != "c-beta" {
		t.Fatalf("selection = %q, want c-beta", got)
	}

	a, cmd = sendKey(t, a, keyRunes("x"))
	if !a.confirm.open() || a.confirm.kind != confirmationKillSession {
		t.Fatalf("x did not open kill confirmation: %#v", a.confirm)
	}
	if a.confirm.target != "c-beta" {
		t.Fatalf("captured target = %q, want c-beta", a.confirm.target)
	}
	if cmd == nil {
		t.Fatal("opening kill confirmation returned nil cmd; want mouse-enable cmd")
	}
	assertPresent(t, a.View(), "Kill session?", "c-beta", "Cancel", "Kill")

	a, cmd = sendKey(t, a, tea.KeyMsg{Type: tea.KeyEsc})
	if a.confirm.open() {
		t.Fatal("esc did not cancel kill confirmation")
	}
	if got := selName(a.sessionsM.Selected()); got != "c-beta" {
		t.Fatalf("selection after cancel = %q, want c-beta", got)
	}
	if commandContainsQuit(cmd) {
		t.Fatal("kill cancel returned quit command")
	}

	var killed string
	orig := killSessionCmd
	killSessionCmd = func(name string) tea.Cmd {
		return func() tea.Msg {
			killed = name
			return sessionKilledMsg{Name: name}
		}
	}
	t.Cleanup(func() { killSessionCmd = orig })

	a, _ = sendKey(t, a, keyRunes("x"))
	a.sessionsM.cursor = 0
	a, cmd = sendKey(t, a, keyRunes("y"))
	if a.confirm.open() {
		t.Fatal("y did not close kill confirmation")
	}
	if cmd == nil {
		t.Fatal("kill confirm returned nil cmd")
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("kill confirm cmd = %T, want tea.BatchMsg", msg)
	}
	for _, subcmd := range batch {
		if subcmd != nil {
			_ = subcmd()
		}
	}
	if killed != "c-beta" {
		t.Fatalf("killed target = %q, want captured c-beta", killed)
	}
}

func TestConfirmation_ArrowFocusEnterAndInputBlocking(t *testing.T) {
	a := newConfirmationTestApp()
	a.sessionsM.SetSessions([]daemon.SessionState{
		{Name: "c-alpha", Host: "local"},
		{Name: "c-beta", Host: "local"},
	})
	a = navigate(t, a, 1)
	a, _ = sendKey(t, a, keyRunes("q"))

	a, _ = sendKey(t, a, keyRunes("2"))
	if a.screen != ScreenSessions {
		t.Fatalf("screen changed while modal open: %v", a.screen)
	}
	if got := selName(a.sessionsM.Selected()); got != "c-beta" {
		t.Fatalf("selection changed while modal open: %q", got)
	}

	a, _ = sendKey(t, a, keyRunes("r"))
	if !a.confirm.open() {
		t.Fatal("refresh key closed modal")
	}

	a, _ = sendKey(t, a, tea.KeyMsg{Type: tea.KeyRight})
	if a.confirm.focus != confirmationFocusConfirm {
		t.Fatalf("right focus = %v, want confirm", a.confirm.focus)
	}
	a, _ = sendKey(t, a, tea.KeyMsg{Type: tea.KeyLeft})
	if a.confirm.focus != confirmationFocusCancel {
		t.Fatalf("left focus = %v, want cancel", a.confirm.focus)
	}
	a, _ = sendKey(t, a, tea.KeyMsg{Type: tea.KeyRight})
	a, cmd := sendKey(t, a, tea.KeyMsg{Type: tea.KeyEnter})
	if a.confirm.open() {
		t.Fatal("enter on confirm did not close modal")
	}
	if !commandContainsQuit(cmd) {
		t.Fatal("enter on confirm did not return quit command")
	}
}

func TestConfirmation_MouseActions(t *testing.T) {
	a := newConfirmationTestApp()
	a, _ = sendKey(t, a, keyRunes("q"))
	left, top, width, height := a.confirmationBounds()
	buttonY := top + height - 4

	m, cmd := a.Update(tea.MouseMsg{
		X:      left + width/4,
		Y:      buttonY,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	a = m.(App)
	if a.confirm.open() {
		t.Fatal("mouse cancel did not close modal")
	}
	if commandContainsQuit(cmd) {
		t.Fatal("mouse cancel returned quit command")
	}

	a, _ = sendKey(t, a, keyRunes("q"))
	left, top, width, height = a.confirmationBounds()
	buttonY = top + height - 4
	m, cmd = a.Update(tea.MouseMsg{
		X:      left + width*3/4,
		Y:      buttonY,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	a = m.(App)
	if a.confirm.open() {
		t.Fatal("mouse confirm did not close modal")
	}
	if !commandContainsQuit(cmd) {
		t.Fatal("mouse confirm did not return quit command")
	}
}
