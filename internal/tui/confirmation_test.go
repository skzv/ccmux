package tui

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

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
	if commandContainsQuit(cmd) {
		t.Fatal("opening the quit confirmation quit ccmux")
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
	if commandContainsQuit(cmd) {
		t.Fatal("opening the kill confirmation quit ccmux")
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
	drainCmd(cmd)
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

// renderedButton finds a dialog button in the rendered frame, the way a
// user sees it: the screen row holding its text and the column of the
// button's middle. Independent of the dialog's layout code on purpose.
func renderedButton(t *testing.T, frame, label string) (x, y int) {
	t.Helper()
	text := " " + label + " "
	for row, line := range strings.Split(ansi.Strip(frame), "\n") {
		if i := strings.LastIndex(line, text); i >= 0 && strings.Contains(line, " Cancel ") {
			return ansi.StringWidth(line[:i]) + ansi.StringWidth(text)/2, row
		}
	}
	t.Fatalf("button %q not found in frame:\n%s", label, ansi.Strip(frame))
	return 0, 0
}

func click(t *testing.T, a App, x, y int) (App, tea.Cmd) {
	t.Helper()
	m, cmd := a.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	return m.(App), cmd
}

// TestConfirmation_MouseActions — clicks land on the buttons where they
// are drawn. The hit-box used to sit one row below the buttons (and
// further off at 40 columns, where the hint wraps and the dialog grows),
// so clicking a button did nothing and clicking under it fired it.
func TestConfirmation_MouseActions(t *testing.T) {
	for _, size := range [][2]int{{100, 30}, {80, 24}, {40, 20}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			a := newConfirmationTestApp()
			a.width, a.height = size[0], size[1]

			a, _ = sendKey(t, a, keyRunes("q"))
			x, y := renderedButton(t, a.View(), "Cancel")
			// One row below the button is not the button.
			if a2, cmd := click(t, a, x, y+1); !a2.confirm.open() || commandContainsQuit(cmd) {
				t.Fatal("a click below the Cancel button acted on the dialog")
			}
			a, cmd := click(t, a, x, y)
			if a.confirm.open() {
				t.Fatal("clicking Cancel did not close the dialog")
			}
			if commandContainsQuit(cmd) {
				t.Fatal("clicking Cancel quit")
			}

			a, _ = sendKey(t, a, keyRunes("q"))
			x, y = renderedButton(t, a.View(), "Quit")
			a, cmd = click(t, a, x, y)
			if a.confirm.open() {
				t.Fatal("clicking Quit did not close the dialog")
			}
			if !commandContainsQuit(cmd) {
				t.Fatal("clicking Quit did not quit")
			}
		})
	}
}

// TestConfirmation_KillDialogNarrow — at phone width the kill dialog
// must fit, keep the session name whole (it used to be hard-wrapped
// mid-word), and its buttons must still take clicks.
func TestConfirmation_KillDialogNarrow(t *testing.T) {
	stubCurrentTmuxSession(t, "")
	a := newConfirmationTestApp()
	a.width, a.height = 40, 20
	a.sessionsM.SetSessions([]daemon.SessionState{{Name: "c-project-name", Host: "local"}})
	a, _ = sendKey(t, a, keyRunes("x"))
	if !a.confirm.open() {
		t.Fatal("x did not open the kill dialog")
	}
	out := a.View()
	assertNoOverflow(t, out, a.width)
	if !strings.Contains(ansi.Strip(out), `"c-project-name"`) {
		t.Errorf("session name broken across lines:\n%s", ansi.Strip(out))
	}
	x, y := renderedButton(t, out, "Cancel")
	if a, _ = click(t, a, x, y); a.confirm.open() {
		t.Error("clicking Cancel at 40 columns did nothing")
	}
}

func stubCurrentTmuxSession(t *testing.T, name string) {
	t.Helper()
	orig := currentTmuxSession
	currentTmuxSession = func() string { return name }
	t.Cleanup(func() { currentTmuxSession = orig })
}

// TestConfirmation_KillOwnSessionWarns — killing the tmux session ccmux
// itself runs in takes ccmux down too; the dialog must say so, and only
// for that session.
func TestConfirmation_KillOwnSessionWarns(t *testing.T) {
	stubCurrentTmuxSession(t, "c-home")
	a := newConfirmationTestApp()
	a.sessionsM.SetSessions([]daemon.SessionState{
		{Name: "c-home", Host: "local"},
		{Name: "c-other", Host: "local"},
	})
	for _, tc := range []struct {
		name string
		warn bool
	}{{"c-home", true}, {"c-other", false}} {
		a2, _ := a.openKillSessionConfirmation("local", tc.name)
		got := strings.Contains(ansi.Strip(a2.View()), "ccmux is running in this session")
		if got != tc.warn {
			t.Errorf("kill %s: warning shown = %v, want %v", tc.name, got, tc.warn)
		}
	}
	// A remote session with the same name is not this one.
	a2, _ := a.openKillSessionConfirmation("mini", "c-home")
	if strings.Contains(ansi.Strip(a2.View()), "ccmux is running in this session") {
		t.Error("warned for a same-named session on another host")
	}
}

// isMouseMsg reports whether msg is the one Bubble Tea's
// tea.EnableMouseCellMotion / tea.DisableMouse commands produce. Those
// message types are unexported, so match on the dynamic type.
func isMouseMsg(msg tea.Msg, mouseCmd tea.Cmd) bool {
	return msg != nil && reflect.TypeOf(msg) == reflect.TypeOf(mouseCmd())
}

func msgsContainMouse(msgs []tea.Msg, mouseCmd tea.Cmd) bool {
	for _, m := range msgs {
		if isMouseMsg(m, mouseCmd) {
			return true
		}
	}
	return false
}

// TestConfirmation_LeavesMouseModeAlone — mouse reporting is enabled
// for the whole program (tea.WithMouseCellMotion in Run), so a dialog
// must not toggle it: closing any confirmation sent tea.DisableMouse,
// and the mouse wheel stopped scrolling Notes / Agents previews for
// the rest of the run.
func TestConfirmation_LeavesMouseModeAlone(t *testing.T) {
	recordSessionRouting(t)
	a := newConfirmationTestApp()
	a.sessionsM.SetSessions([]daemon.SessionState{{Name: "c-alpha", Host: "local"}})
	var msgs []tea.Msg
	press := func(k tea.KeyMsg) {
		t.Helper()
		var cmd tea.Cmd
		a, cmd = sendKey(t, a, k)
		msgs = append(msgs, drainCmd(cmd)...)
	}
	press(keyRunes("q"))
	press(keyRunes("n")) // cancel quit
	press(keyRunes("x"))
	press(tea.KeyMsg{Type: tea.KeyEsc}) // cancel kill
	press(keyRunes("x"))
	press(keyRunes("y")) // confirm kill
	if msgsContainMouse(msgs, tea.DisableMouse) {
		t.Error("a confirmation dialog turned mouse reporting off")
	}
	if msgsContainMouse(msgs, tea.EnableMouseCellMotion) {
		t.Error("a confirmation dialog toggled mouse reporting (it is program-wide)")
	}
}

// TestMouseMode_ReenabledAfterExec — Bubble Tea v1 releases the
// terminal around tea.ExecProcess (mouse reporting off) and does not
// turn mouse reporting back on when it restores it, so every attach /
// detach and every $EDITOR round trip left the wheel dead. Both return
// paths must re-enable it.
func TestMouseMode_ReenabledAfterExec(t *testing.T) {
	stubRefreshSeams(t, nil)
	t.Setenv("HOME", t.TempDir())
	a := newConfirmationTestApp()
	a.cfg.Projects.Root = t.TempDir()

	for name, msg := range map[string]tea.Msg{
		"clean detach":     attachExitedMsg{},
		"failed attach":    attachExitedMsg{Err: errors.New("session not found")},
		"ssh auth failure": attachExitedMsg{Err: errors.New("exit status 255: Permission denied (publickey)"), RemoteSSHTarget: &attachRemoteTarget{Host: "mini", Port: 22}},
	} {
		_, cmd := updateApp(t, a, msg)
		if !msgsContainMouse(drainCmd(cmd), tea.EnableMouseCellMotion) {
			t.Errorf("%s: mouse reporting not re-enabled after the attach returned", name)
		}
	}

	for name, err := range map[string]error{"editor ok": nil, "editor failed": errors.New("exit status 1")} {
		msgs := drainCmd(func() tea.Msg { return editorExited(err, notesReloadMsg{}) })
		if !msgsContainMouse(msgs, tea.EnableMouseCellMotion) {
			t.Errorf("%s: mouse reporting not re-enabled after $EDITOR returned", name)
		}
		if _, ok := findMsg[notesReloadMsg](msgs); ok == (err != nil) {
			t.Errorf("%s: reload message = %v, want it only on success", name, ok)
		}
	}
}
