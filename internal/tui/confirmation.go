package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
		return "Quit ccmux?"
	case confirmationKillSession:
		return "Kill session?"
	default:
		return ""
	}
}

func (m confirmationModal) body() string {
	switch m.kind {
	case confirmationQuit:
		return "Exit ccmux. Managed tmux sessions will keep running."
	case confirmationKillSession:
		if m.host != "" {
			return fmt.Sprintf(tr("Kill tmux session %q on %s. This cannot be undone."), m.target, m.host)
		}
		return fmt.Sprintf("Kill tmux session %q. This cannot be undone.", m.target)
	default:
		return ""
	}
}

func (m confirmationModal) confirmLabel() string {
	switch m.kind {
	case confirmationQuit:
		return "Quit"
	case confirmationKillSession:
		return "Kill"
	default:
		return "Confirm"
	}
}

func (a App) openQuitConfirmation() (App, tea.Cmd) {
	a.confirm = newQuitConfirmation()
	return a, tea.EnableMouseCellMotion
}

// openKillSessionConfirmation opens the kill modal for the session
// `name` on host label `host`. Local labels ("", "local", this
// machine's hostname) collapse to "" so the modal and the kill route
// agree on what "local" means.
func (a App) openKillSessionConfirmation(host, name string) (App, tea.Cmd) {
	if a.isLocalSessionHost(host) {
		host = ""
	}
	a.confirm = newKillSessionConfirmation(host, name)
	return a, tea.EnableMouseCellMotion
}

func (a App) cancelConfirmation() (App, tea.Cmd) {
	a.confirm = confirmationModal{}
	return a, tea.DisableMouse
}

func (a App) acceptConfirmation() (App, tea.Cmd) {
	confirm := a.confirm
	a.confirm = confirmationModal{}
	switch confirm.kind {
	case confirmationQuit:
		return a, tea.Batch(tea.DisableMouse, tea.Quit)
	case confirmationKillSession:
		if confirm.target == "" {
			return a, tea.DisableMouse
		}
		return a, tea.Batch(tea.DisableMouse, a.killSessionTargetCmd(confirm.host, confirm.target))
	default:
		return a, tea.DisableMouse
	}
}

func (a App) updateConfirmationKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		a.confirm = confirmationModal{}
		return a, tea.Batch(tea.DisableMouse, tea.Quit)
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

func (a App) confirmationButtonAt(x, y int) (confirmationFocus, bool) {
	if !a.confirm.open() || a.width <= 0 || a.height <= 0 {
		return confirmationFocusCancel, false
	}
	left, top, width, height := a.confirmationBounds()
	buttonY := top + height - 4
	if y < buttonY || y > buttonY+1 {
		return confirmationFocusCancel, false
	}
	mid := left + width/2
	if x >= left+2 && x < mid {
		return confirmationFocusCancel, true
	}
	if x >= mid && x < left+width-2 {
		return confirmationFocusConfirm, true
	}
	return confirmationFocusCancel, false
}

func (a App) renderConfirmationOverlay(width, height int) string {
	modalWidth := confirmationModalWidth(width)
	contentWidth := maxInt(10, modalWidth-6)
	title := a.styles.Title.Render(a.confirm.title())
	body := truncate(a.confirm.body(), contentWidth)
	hint := a.styles.Muted.Render(tr("y confirm  n/esc cancel  arrows move"))

	cancel := a.renderConfirmationButton("Cancel", a.confirm.focus == confirmationFocusCancel)
	confirm := a.renderConfirmationButton(a.confirm.confirmLabel(), a.confirm.focus == confirmationFocusConfirm)
	buttons := lipgloss.JoinHorizontal(lipgloss.Top, cancel, "  ", confirm)
	buttons = lipgloss.PlaceHorizontal(contentWidth, lipgloss.Center, buttons)

	lines := []string{title, "", body, "", buttons, "", hint}
	modal := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(a.styles.P.Red).
		Padding(a.styles.Spacing.SM, a.styles.Spacing.MD).
		Width(contentWidth).
		Render(strings.Join(lines, "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
}

func (a App) renderConfirmationButton(label string, focused bool) string {
	text := " " + label + " "
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

func (a App) confirmationBounds() (left, top, width, height int) {
	width = confirmationModalWidth(a.width)
	height = 11
	left = max0((a.width - width) / 2)
	top = max0((a.height - height) / 2)
	return left, top, width, height
}

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
