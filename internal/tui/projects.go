package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// projectsModel lists projects under the configured root and offers
// quick-create / open-session actions.
type projectsModel struct {
	st       styles.Styles
	km       Keymap
	projects []project.Project
	cursor   int
}

func newProjects(st styles.Styles, km Keymap) projectsModel {
	return projectsModel{st: st, km: km}
}

func (m *projectsModel) SetProjects(p []project.Project) {
	m.projects = p
	if m.cursor >= len(p) {
		m.cursor = max0(len(p) - 1)
	}
}

func (m projectsModel) Update(msg tea.Msg) (projectsModel, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch {
		case keyMatches(km, m.km.Up):
			if m.cursor > 0 {
				m.cursor--
			}
		case keyMatches(km, m.km.Down):
			if m.cursor < len(m.projects)-1 {
				m.cursor++
			}
		}
	}
	return m, nil
}

func (m projectsModel) View(width, height int) string {
	leftW := width * 2 / 3
	rightW := width - leftW - 2
	return lipgloss.JoinHorizontal(lipgloss.Top,
		m.renderList(leftW, height),
		" ",
		m.renderDetail(rightW, height),
	)
}

func (m projectsModel) renderList(width, height int) string {
	if len(m.projects) == 0 {
		body := m.st.Muted.Render("No projects found under your projects root.\n\nUse " + m.st.Key.Render("n") + " to scaffold a new one.")
		return m.st.Pane.Width(width - 2).Height(height).Render(body)
	}
	rows := []string{m.st.Emphasis.Render("Projects"), ""}
	for i, p := range m.projects {
		marks := []string{}
		if p.HasGit {
			marks = append(marks, m.st.Muted.Render("git"))
		}
		if p.HasCM {
			marks = append(marks, m.st.Muted.Render("CLAUDE.md"))
		}
		if p.HasDocs {
			marks = append(marks, m.st.Muted.Render("docs/"))
		}
		line := p.Name + "   " + strings.Join(marks, " · ")
		if i == m.cursor {
			line = m.st.ListItemSelected.Render(line)
		} else {
			line = m.st.ListItem.Render(line)
		}
		rows = append(rows, line)
	}
	return m.st.PaneFocused.Width(width - 2).Height(height).Render(strings.Join(rows, "\n"))
}

func (m projectsModel) renderDetail(width, height int) string {
	if len(m.projects) == 0 || m.cursor < 0 {
		return m.st.Pane.Width(width - 2).Height(height).Render(m.st.Muted.Render("No selection."))
	}
	p := m.projects[m.cursor]
	lines := []string{
		m.st.Emphasis.Render(p.Name),
		m.st.Muted.Render(p.Path),
		"",
		"session name  " + m.st.Emphasis.Render(p.SessionName()),
		"",
		m.st.Subtitle.Render("Keys"),
		m.st.Key.Render("enter") + "  attach or create session",
		m.st.Key.Render("n") + "      new project (mkproj)",
		m.st.Key.Render("u") + "      upgrade project",
		m.st.Key.Render("4") + "      open Notes for this project",
	}
	return m.st.Pane.Width(width - 2).Height(height).Render(strings.Join(lines, "\n"))
}
