package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/skzv/ccmux/internal/tmuxchrome"
)

type confirmationKind int

const (
	confirmationNone confirmationKind = iota
	confirmationQuit
	confirmationKillSession
)

type confirmationFocus int

const (
	confirmationFocusCancel confirmationFocus = iota
	confirmationFocusConfirm
)

const (
	confirmationModalMaxWidth = 72
	confirmationModalMinWidth = 36
)

type confirmationModal struct {
	kind   confirmationKind
	target string
	// host is the kill target's host label: "" when the session lives
	// on this machine, otherwise the remote host whose daemon receives
	// the kill (and which the modal names, so the user can tell the
	// laptop's c-ccmux from the mini's).
	host  string
	focus confirmationFocus
	// self marks a kill of the tmux session ccmux itself runs in: the
	// dialog warns that ccmux goes down with it.
	self bool
}

func newQuitConfirmation() confirmationModal {
	return confirmationModal{kind: confirmationQuit, focus: confirmationFocusCancel}
}

func newKillSessionConfirmation(host, name string) confirmationModal {
	return confirmationModal{kind: confirmationKillSession, target: name, host: host, focus: confirmationFocusCancel}
}

func (m confirmationModal) open() bool {
	return m.kind != confirmationNone
}

func (m confirmationModal) title() string {
	switch m.kind {
	case confirmationQuit:
		return tr("Quit ccmux?")
	case confirmationKillSession:
		return tr("Kill session?")
	default:
		return ""
	}
}

// body is the dialog's explanation. target is the session name as it
// should appear (already shortened to fit, see renderConfirmationOverlay).
func (m confirmationModal) body(target string) string {
	switch m.kind {
	case confirmationQuit:
		return tr("Exit ccmux. Managed tmux sessions will keep running.")
	case confirmationKillSession:
		if m.host != "" {
			return fmt.Sprintf(tr("Kill tmux session %q on %s. This cannot be undone."), target, m.host)
		}
		return fmt.Sprintf(tr("Kill tmux session %q. This cannot be undone."), target)
	default:
		return ""
	}
}

// warning is an extra line for a kill that takes ccmux down with it.
func (m confirmationModal) warning() string {
	if m.kind == confirmationKillSession && m.self {
		return tr("ccmux is running in this session — it will close too.")
	}
	return ""
}

func (m confirmationModal) confirmLabel() string {
	switch m.kind {
	case confirmationQuit:
		return tr("Quit")
	case confirmationKillSession:
		return tr("Kill")
	default:
		return tr("Confirm")
	}
}

// Mouse reporting is program-wide (tea.WithMouseCellMotion in Run), so
// the dialogs below never toggle it: turning it off on close left the
// mouse wheel dead for the rest of the run.

func (a App) openQuitConfirmation() (App, tea.Cmd) {
	a.confirm = newQuitConfirmation()
	return a, nil
}

// openKillSessionConfirmation opens the kill modal for the session
// `name` on host label `host`. Local labels ("" and "local") collapse
// to "" so the modal and the kill route agree on what "local" means;
// every other host is named in the modal.
func (a App) openKillSessionConfirmation(host, name string) (App, tea.Cmd) {
	if isLocalSessionHost(host) {
		host = ""
	}
	a.confirm = newKillSessionConfirmation(host, name)
	a.confirm.self = host == "" && name == currentTmuxSession()
	return a, nil
}

// currentTmuxSession names the local tmux session ccmux runs in ("" when
// it isn't inside tmux). A package var so tests don't need tmux.
var currentTmuxSession = func() string {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	return tmuxchrome.CurrentSession(ctx)
}

func (a App) cancelConfirmation() (App, tea.Cmd) {
	a.confirm = confirmationModal{}
	return a, nil
}

func (a App) acceptConfirmation() (App, tea.Cmd) {
	confirm := a.confirm
	a.confirm = confirmationModal{}
	switch confirm.kind {
	case confirmationQuit:
		return a, tea.Quit
	case confirmationKillSession:
		if confirm.target == "" {
			return a, nil
		}
		return a, a.killSessionTargetCmd(confirm.host, confirm.target)
	default:
		return a, nil
	}
}

func (a App) updateConfirmationKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		a.confirm = confirmationModal{}
		return a, tea.Quit
	case "y":
		return a.acceptConfirmation()
	case "n", "esc":
		return a.cancelConfirmation()
	case "enter":
		if a.confirm.focus == confirmationFocusConfirm {
			return a.acceptConfirmation()
		}
		return a.cancelConfirmation()
	case "left", "h", "up", "k":
		a.confirm.focus = confirmationFocusCancel
		return a, nil
	case "right", "l", "down", "j":
		a.confirm.focus = confirmationFocusConfirm
		return a, nil
	default:
		return a, nil
	}
}

func (a App) updateConfirmationMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	event := tea.MouseEvent(msg)
	if event.Button != tea.MouseButtonLeft || event.Action != tea.MouseActionPress {
		return a, nil
	}
	focus, ok := a.confirmationButtonAt(event.X, event.Y)
	if !ok {
		return a, nil
	}
	a.confirm.focus = focus
	if focus == confirmationFocusConfirm {
		return a.acceptConfirmation()
	}
	return a.cancelConfirmation()
}

// confirmationButtonAt maps a click to a dialog button. The hit-boxes
// come from the rendered dialog itself — the row holding both buttons
// and their columns in it — rather than from geometry re-derived next to
// the render: that copy said the buttons sat one row lower than they do,
// and drifted further once the text wrapped on a narrow terminal.
func (a App) confirmationButtonAt(x, y int) (confirmationFocus, bool) {
	if !a.confirm.open() || a.width <= 0 || a.height <= 0 {
		return confirmationFocusCancel, false
	}
	cancelText := confirmationButtonText(tr("Cancel"))
	confirmText := confirmationButtonText(a.confirm.confirmLabel())
	pair := cancelText + confirmationButtonGap + confirmText
	lines := strings.Split(ansi.Strip(a.renderConfirmationOverlay(a.width, a.height)), "\n")
	if y < 0 || y >= len(lines) {
		return confirmationFocusCancel, false
	}
	i := strings.Index(lines[y], pair)
	if i < 0 {
		return confirmationFocusCancel, false
	}
	cancelX := ansi.StringWidth(lines[y][:i])
	confirmX := cancelX + ansi.StringWidth(cancelText+confirmationButtonGap)
	switch {
	case x >= cancelX && x < cancelX+ansi.StringWidth(cancelText):
		return confirmationFocusCancel, true
	case x >= confirmX && x < confirmX+ansi.StringWidth(confirmText):
		return confirmationFocusConfirm, true
	}
	return confirmationFocusCancel, false
}

// confirmationButtonGap separates the two buttons.
const confirmationButtonGap = "  "

// confirmationButtonText is a button's visible text: its label padded by
// one space each side (the filled background spans the padding).
func confirmationButtonText(label string) string { return " " + label + " " }

func (a App) renderConfirmationOverlay(width, height int) string {
	st := a.styles
	modalWidth := confirmationModalWidth(width)
	pad := st.Spacing.MD
	// Text column: the modal less its border and horizontal padding.
	// Every line is wrapped to it here, word by word, so the box never
	// re-wraps anything itself (it used to hard-break the session name
	// mid-word at phone widths).
	inner := maxInt(8, modalWidth-2-2*pad)
	wrap := func(s string) string { return lipgloss.NewStyle().Width(inner).Render(s) }

	// A session name too long for a line is shortened, not broken.
	target := a.confirm.target
	if lipgloss.Width(target) > inner-4 {
		target = truncate(target, maxInt(4, inner-4))
	}
	lines := []string{
		wrap(st.Title.Render(a.confirm.title())),
		"",
		// Break at spaces only: a word wrap also breaks after hyphens,
		// which split "c-my-project" across two lines.
		wrapAtSpaces(a.confirm.body(target), inner),
	}
	if w := a.confirm.warning(); w != "" {
		lines = append(lines, "", st.StatusWarning.Render(wrap(w)))
	}

	cancel := a.renderConfirmationButton(tr("Cancel"), a.confirm.focus == confirmationFocusCancel)
	confirm := a.renderConfirmationButton(a.confirm.confirmLabel(), a.confirm.focus == confirmationFocusConfirm)
	buttons := lipgloss.JoinHorizontal(lipgloss.Top, cancel, confirmationButtonGap, confirm)
	buttons = strings.Repeat(" ", max0((inner-lipgloss.Width(buttons))/2)) + buttons

	lines = append(lines, "", buttons, "", st.Muted.Render(wrap(tr("y confirm  n/esc cancel  arrows move"))))
	modal := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(st.P.Red).
		Padding(st.Spacing.SM, pad).
		Render(strings.Join(lines, "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
}

func (a App) renderConfirmationButton(label string, focused bool) string {
	text := confirmationButtonText(label)
	if focused {
		return lipgloss.NewStyle().
			Background(a.styles.P.Selected).
			Foreground(a.styles.P.Lavender).
			Bold(true).
			Render(text)
	}
	return lipgloss.NewStyle().
		Foreground(a.styles.P.FG).
		Background(a.styles.P.BGAlt).
		Render(text)
}

// wrapAtSpaces wraps plain text to width cells, breaking only between
// space-separated words. A word wider than width gets a line of its own
// (callers shorten such words first).
func wrapAtSpaces(s string, width int) string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(s) {
		switch {
		case line == "":
			line = word
		case ansi.StringWidth(line)+1+ansi.StringWidth(word) <= width:
			line += " " + word
		default:
			lines = append(lines, line)
			line = word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// confirmationModalWidth is the dialog's outer width (border included)
// on a screenWidth-wide terminal.
func confirmationModalWidth(screenWidth int) int {
	if screenWidth <= 0 {
		return 0
	}
	width := minInt(confirmationModalMaxWidth, screenWidth-4)
	if width < confirmationModalMinWidth {
		width = minInt(confirmationModalMinWidth, screenWidth-2)
	}
	if width < 10 {
		width = screenWidth
	}
	return width
}
