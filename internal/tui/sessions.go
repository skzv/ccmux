package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tmux"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// sessionsModel is the full session list with a details pane.
// Under narrow terminals (< 80 cols), only the list is shown.
type sessionsModel struct {
	st       styles.Styles
	km       Keymap
	sessions []daemon.SessionState
	cursor   int
}

func newSessions(st styles.Styles, km Keymap) sessionsModel {
	return sessionsModel{st: st, km: km}
}

func (m *sessionsModel) SetSessions(ss []daemon.SessionState) {
	m.sessions = ss
	if m.cursor >= len(ss) {
		m.cursor = max0(len(ss) - 1)
	}
}

// Selected returns the currently-highlighted session, or nil.
func (m sessionsModel) Selected() *daemon.SessionState {
	if m.cursor < 0 || m.cursor >= len(m.sessions) {
		return nil
	}
	s := m.sessions[m.cursor]
	return &s
}

func (m sessionsModel) Update(msg tea.Msg) (sessionsModel, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch {
		case keyMatches(km, m.km.Up):
			if m.cursor > 0 {
				m.cursor--
			}
		case keyMatches(km, m.km.Down):
			if m.cursor < len(m.sessions)-1 {
				m.cursor++
			}
		case keyMatches(km, m.km.Kill):
			if sel := m.Selected(); sel != nil {
				name := sel.Name
				return m, killSessionCmd(name)
			}
		}
	}
	return m, nil
}

func (m sessionsModel) View(width, height int) string {
	if isNarrow(width) {
		return m.renderList(width, height)
	}
	leftW := width * 2 / 3
	rightW := width - leftW - 1
	return lipgloss.JoinHorizontal(lipgloss.Top,
		m.renderList(leftW, height),
		" ",
		m.renderDetail(rightW, height),
	)
}

func (m sessionsModel) renderList(width, height int) string {
	header := m.st.Emphasis.Render("Sessions") + "  " + m.st.Muted.Render(
		fmt.Sprintf("(%d total — enter: attach   x: kill   r: refresh)", len(m.sessions)),
	)
	if len(m.sessions) == 0 {
		body := lipgloss.JoinVertical(lipgloss.Left,
			header,
			"",
			m.st.Muted.Render("No sessions yet."),
			"",
			"Press "+m.st.Key.Render("3")+" to open Projects and create one.",
		)
		return m.st.Pane.Width(width - 2).Height(height - 2).Render(body)
	}
	rows := []string{header, ""}
	for i, s := range m.sessions {
		line := renderSessionLine(m.st, s, width-4)
		if i == m.cursor {
			line = m.st.ListItemSelected.Render(line)
		} else {
			line = m.st.ListItem.Render(line)
		}
		rows = append(rows, line)
	}
	return m.st.PaneFocused.Width(width - 2).Height(height - 2).Render(strings.Join(rows, "\n"))
}

func (m sessionsModel) renderDetail(width, height int) string {
	sel := m.Selected()
	if sel == nil {
		return m.st.Pane.Width(width - 2).Height(height - 2).Render(m.st.Muted.Render("Nothing selected."))
	}
	lines := []string{
		m.st.Emphasis.Render(sel.Name),
		m.st.Muted.Render(fmt.Sprintf("on %s", sel.Host)),
		"",
		fmt.Sprintf("state    %s %s", stateGlyph(m.st, sel.State), sel.State),
		fmt.Sprintf("project  %s", sel.Project),
		fmt.Sprintf("path     %s", truncate(sel.Path, width-12)),
		fmt.Sprintf("windows  %d", sel.Windows),
		fmt.Sprintf("attached %v", sel.Attached),
		fmt.Sprintf("created  %s", relTime(sel.Created)),
		fmt.Sprintf("changed  %s", relTime(sel.LastChange)),
		"",
		m.st.Subtitle.Render("Keys"),
		m.st.Key.Render("enter") + "  attach",
		m.st.Key.Render("x") + "      kill",
		m.st.Key.Render("R") + "      rename (coming soon)",
		m.st.Key.Render("k") + "      toggle keep-awake (coming soon)",
		m.st.Key.Render("s") + "      snapshot (coming soon)",
	}
	return m.st.Pane.Width(width - 2).Height(height - 2).Render(strings.Join(lines, "\n"))
}

// killSessionCmd runs `tmux kill-session -t <name>` and reports the result
// via sessionKilledMsg, which the app uses to trigger a refresh.
func killSessionCmd(name string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		err := tmux.Kill(ctx, name)
		return sessionKilledMsg{Name: name, Err: err}
	}
}

func relTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return humanDuration(time.Since(t)) + " ago"
}

func truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return s[:n-1] + "…"
}

func max0(x int) int {
	if x < 0 {
		return 0
	}
	return x
}
