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

// dashboardModel renders the at-a-glance landing screen.
// On wide terminals: hero + (sessions list | stats + hints).
// On narrow terminals (< 80 cols): everything stacked vertically.
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
	if isNarrow(width) {
		return m.viewNarrow(width, height)
	}
	return m.viewWide(width, height)
}

func (m dashboardModel) viewWide(width, height int) string {
	hero := m.heroPanel(width)
	heroH := lipgloss.Height(hero)
	rowH := height - heroH
	if rowH < 8 {
		rowH = 8
	}

	leftW := width * 2 / 3
	rightW := width - leftW - 1

	sessions := m.topSessions(leftW, rowH)

	stats := m.statsPanel(rightW)
	hints := m.hintPanel(rightW)
	right := lipgloss.JoinVertical(lipgloss.Left, stats, hints)

	row := lipgloss.JoinHorizontal(lipgloss.Top, sessions, " ", right)
	return lipgloss.JoinVertical(lipgloss.Left, hero, row)
}

func (m dashboardModel) viewNarrow(width, height int) string {
	hero := m.heroPanel(width)
	stats := m.statsPanel(width)
	heroH := lipgloss.Height(hero)
	statsH := lipgloss.Height(stats)
	listH := height - heroH - statsH
	if listH < 5 {
		listH = 5
	}
	sessions := m.topSessions(width, listH)
	return lipgloss.JoinVertical(lipgloss.Left, hero, stats, sessions)
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
		m.st.Muted.Render(time.Now().Format("Mon Jan 2 — 15:04:05")),
	}
	return m.st.Pane.Width(width - 2).Render(strings.Join(rows, "\n"))
}

func (m dashboardModel) hintPanel(width int) string {
	hint := []string{
		m.st.Emphasis.Render("Quick keys"),
		"",
		m.st.Key.Render("2") + "  Sessions",
		m.st.Key.Render("3") + "  Projects",
		m.st.Key.Render("4") + "  Notes",
		m.st.Key.Render("5") + "  Claude config",
		m.st.Key.Render("n") + "  new project",
		m.st.Key.Render("r") + "  refresh",
		m.st.Key.Render("?") + "  full keys",
	}
	return m.st.Pane.Width(width - 2).Render(strings.Join(hint, "\n"))
}

// topSessions produces a pane exactly `height` lines tall and `width` cells
// wide. We clamp the content to (height - 2) lines so Lipgloss's
// minimum-height semantics doesn't push the pane taller than requested.
func (m dashboardModel) topSessions(width, height int) string {
	if width < 16 {
		width = 16
	}
	if height < 5 {
		height = 5
	}
	// Pane border accounts for 2 lines; padding is 0 vertically.
	contentLines := height - 2

	header := m.st.Emphasis.Render("Sessions") + "  " + m.st.Muted.Render(fmt.Sprintf("(%d)", len(m.sessions)))
	rows := []string{header, ""}
	remaining := contentLines - len(rows)
	if remaining < 0 {
		remaining = 0
	}

	if len(m.sessions) == 0 {
		if remaining > 0 {
			rows = append(rows, m.st.Muted.Render("No active sessions."))
			remaining--
		}
		if remaining > 0 {
			rows = append(rows, "Press "+m.st.Key.Render("3")+" to start one.")
			remaining--
		}
	} else {
		inner := width - 4
		if inner < 10 {
			inner = 10
		}
		// If we have more sessions than rows, reserve one line for "and N more".
		maxSessions := remaining
		needsTail := len(m.sessions) > maxSessions
		if needsTail {
			maxSessions = remaining - 1
		}
		if maxSessions < 1 {
			maxSessions = 1
		}
		for i := 0; i < maxSessions && i < len(m.sessions); i++ {
			rows = append(rows, renderSessionLine(m.st, m.sessions[i], inner))
		}
		if needsTail {
			rows = append(rows, m.st.Muted.Render(fmt.Sprintf("… and %d more", len(m.sessions)-maxSessions)))
		}
	}

	// Pad to exactly contentLines so the pane renders at the target height.
	for len(rows) < contentLines {
		rows = append(rows, "")
	}
	if len(rows) > contentLines {
		rows = rows[:contentLines]
	}

	// Lipgloss Width/Height set CONTENT dimensions; border adds +2 to each.
	// To produce a pane exactly height x width cells, pass (width-2, height-2).
	return m.st.Pane.Width(width - 2).Height(contentLines).Render(strings.Join(rows, "\n"))
}

// renderSessionLine produces one line per session: host dot, state glyph,
// name, idle time. Truncates the session name so the line fits in `inner`
// columns (the content area inside the pane border + padding).
func renderSessionLine(st styles.Styles, s daemon.SessionState, inner int) string {
	hostDot := st.HostColor(s.Host).Render("●")
	state := stateGlyph(st, s.State)
	age := ""
	if !s.LastChange.IsZero() {
		age = humanDuration(time.Since(s.LastChange))
	}

	// Fixed-width prefix: hostDot(1) + space + state(1) + space = ~4 cells visually
	prefix := hostDot + " " + state + " "
	suffix := ""
	if age != "" {
		suffix = "  " + st.Muted.Render(age)
	}
	nameBudget := inner - 4 - lipgloss.Width(suffix)
	if nameBudget < 6 {
		nameBudget = 6
	}
	name := s.Name
	if lipgloss.Width(name) > nameBudget {
		runes := []rune(name)
		if len(runes) > nameBudget-1 {
			runes = runes[:nameBudget-1]
		}
		name = string(runes) + "…"
	}
	return prefix + st.Emphasis.Render(name) + suffix
}

func stateGlyph(st styles.Styles, state string) string {
	switch state {
	case "active":
		return st.StateActive.Render("▶")
	case "idle":
		return st.StateIdle.Render("◌")
	case "needs_input":
		return st.StateNeedsInput.Render("!")
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
