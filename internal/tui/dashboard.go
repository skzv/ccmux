package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// dashboardModel renders the at-a-glance landing screen: aggregate counts,
// a top-N sessions table, and a host-status panel.
type dashboardModel struct {
	st       styles.Styles
	km       Keymap
	sessions []daemon.SessionState
}

func newDashboard(st styles.Styles, km Keymap) dashboardModel {
	return dashboardModel{st: st, km: km}
}

func (m dashboardModel) Update(msg tea.Msg) (dashboardModel, tea.Cmd) {
	return m, nil
}

func (m *dashboardModel) SetSessions(ss []daemon.SessionState) {
	m.sessions = ss
}

func (m dashboardModel) View(width, height int) string {
	hero := m.heroPanel(width)
	stats := m.statsPanel(width / 2)
	sessions := m.topSessions(width-width/2-2, height-lipgloss.Height(hero)-2)
	right := lipgloss.JoinVertical(lipgloss.Left, stats, m.hintPanel(width/2))
	row := lipgloss.JoinHorizontal(lipgloss.Top, sessions, " ", right)
	return lipgloss.JoinVertical(lipgloss.Left, hero, row)
}

func (m dashboardModel) heroPanel(width int) string {
	title := m.st.Title.Render("Hello.")
	sub := m.st.Subtitle.Render("Welcome to ccmux. One TUI for every Claude session, every project, every device.")
	body := lipgloss.JoinVertical(lipgloss.Left, title, sub)
	return m.st.Pane.Width(width - 2).Render(body)
}

func (m dashboardModel) statsPanel(width int) string {
	active := 0
	idle := 0
	waiting := 0
	for _, s := range m.sessions {
		switch s.State {
		case "active":
			active++
		case "idle":
			idle++
		case "needs_input":
			waiting++
		}
	}
	rows := []string{
		m.st.Emphasis.Render("Session summary"),
		"",
		fmt.Sprintf("%s  %d active", m.st.StateActive.Render("●"), active),
		fmt.Sprintf("%s  %d idle", m.st.StateIdle.Render("●"), idle),
		fmt.Sprintf("%s  %d waiting for input", m.st.StateNeedsInput.Render("●"), waiting),
		"",
		m.st.Muted.Render(time.Now().Format("Monday, January 2 — 15:04:05")),
	}
	return m.st.Pane.Width(width - 2).Render(strings.Join(rows, "\n"))
}

func (m dashboardModel) hintPanel(width int) string {
	hint := []string{
		m.st.Emphasis.Render("Quick keys"),
		"",
		m.st.Key.Render("2") + "  jump to Sessions",
		m.st.Key.Render("3") + "  jump to Projects",
		m.st.Key.Render("4") + "  Notes",
		m.st.Key.Render("5") + "  Claude config",
		m.st.Key.Render("r") + "  refresh now",
		m.st.Key.Render("?") + "  full keybindings",
	}
	return m.st.Pane.Width(width - 2).Render(strings.Join(hint, "\n"))
}

func (m dashboardModel) topSessions(width, height int) string {
	if width < 20 {
		width = 20
	}
	rows := []string{m.st.Emphasis.Render("Sessions") + "  " + m.st.Muted.Render(fmt.Sprintf("(%d)", len(m.sessions)))}
	rows = append(rows, "")
	if len(m.sessions) == 0 {
		rows = append(rows, m.st.Muted.Render("No active sessions. Press "+m.st.Key.Render("3")+" to start one."))
	} else {
		max := height - 4
		if max < 3 {
			max = 3
		}
		for i, s := range m.sessions {
			if i >= max {
				rows = append(rows, m.st.Muted.Render(fmt.Sprintf("… and %d more", len(m.sessions)-i)))
				break
			}
			rows = append(rows, renderSessionLine(m.st, s, width-4))
		}
	}
	return m.st.Pane.Width(width).Height(height).Render(strings.Join(rows, "\n"))
}

// renderSessionLine produces one line per session: host dot, state glyph,
// name, idle time.
func renderSessionLine(st styles.Styles, s daemon.SessionState, width int) string {
	host := st.HostColor(s.Host).Render("● " + s.Host)
	state := stateGlyph(st, s.State)
	name := s.Name
	if len(name) > 30 {
		name = name[:27] + "…"
	}
	age := ""
	if !s.LastChange.IsZero() {
		age = humanDuration(time.Since(s.LastChange))
	}
	return fmt.Sprintf("%s %s %s  %s", host, state, st.Emphasis.Render(name), st.Muted.Render(age))
}

func stateGlyph(st styles.Styles, state string) string {
	switch state {
	case "active":
		return st.StateActive.Render("▶")
	case "idle":
		return st.StateIdle.Render("◌")
	case "needs_input":
		return st.StateNeedsInput.Render("🔔")
	case "error":
		return st.StateError.Render("✗")
	default:
		return st.StateUnknown.Render("?")
	}
}

func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
