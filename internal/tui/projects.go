package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/scaffold"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// projectsModel is the Projects screen. Two states:
//   - listing: tree of projects with detail pane
//   - form != nil: modal for creating a new project
type projectsModel struct {
	st       styles.Styles
	km       Keymap
	projects []project.Project
	cursor   int
	form     *newProjectFormModel
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
	// Modal mode: route everything to the form.
	if m.form != nil {
		switch msg := msg.(type) {
		case newProjectCancelMsg:
			m.form = nil
			return m, nil
		case newProjectSubmitMsg:
			// Drop the form, kick off scaffold+session start as a tea.Cmd.
			m.form = nil
			return m, scaffoldAndStartCmd(msg)
		}
		f, cmd := m.form.Update(msg)
		m.form = &f
		return m, cmd
	}

	if km, ok := msg.(tea.KeyMsg); ok {
		switch {
		case keyMatches(km, m.km.NewItem):
			f := newNewProjectForm(m.st)
			m.form = &f
			return m, tea.Batch(textInputBlink())
		case km.String() == "u":
			// Upgrade current working directory (non-destructive).
			return m, upgradeCwdCmd()
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
	if m.form != nil {
		// Show form centered with project list dimmed behind it.
		formW := minInt(80, width-4)
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, m.form.View(formW))
	}
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

func (m projectsModel) renderList(width, height int) string {
	header := m.st.Emphasis.Render("Projects") + "  " + m.st.Muted.Render("(n: new   u: upgrade cwd   enter: attach)")
	if len(m.projects) == 0 {
		body := lipgloss.JoinVertical(lipgloss.Left,
			header,
			"",
			m.st.Muted.Render("No projects found under your projects root."),
			"",
			"Press "+m.st.Key.Render("n")+" to scaffold a new one.",
		)
		return m.st.Pane.Width(width - 2).Height(height - 2).Render(body)
	}
	rows := []string{header, ""}
	for i, p := range m.projects {
		marks := []string{}
		if p.HasGit {
			marks = append(marks, "git")
		}
		if p.HasCM {
			marks = append(marks, "CLAUDE")
		}
		if p.HasDocs {
			marks = append(marks, "docs/")
		}
		tail := ""
		if len(marks) > 0 {
			tail = "   " + m.st.Muted.Render(strings.Join(marks, " · "))
		}
		line := p.Name + tail
		if i == m.cursor {
			line = m.st.ListItemSelected.Render(line)
		} else {
			line = m.st.ListItem.Render(line)
		}
		rows = append(rows, line)
	}
	return m.st.PaneFocused.Width(width - 2).Height(height - 2).Render(strings.Join(rows, "\n"))
}

func (m projectsModel) renderDetail(width, height int) string {
	if len(m.projects) == 0 || m.cursor < 0 {
		return m.st.Pane.Width(width - 2).Height(height - 2).Render(m.st.Muted.Render("No selection."))
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
		m.st.Key.Render("n") + "      new project (modal form)",
		m.st.Key.Render("u") + "      upgrade cwd (current shell, not selected)",
		m.st.Key.Render("4") + "      open Notes for this project",
	}
	return m.st.Pane.Width(width - 2).Height(height - 2).Render(strings.Join(lines, "\n"))
}

// scaffoldAndStartCmd runs scaffold + StartSession and returns a
// projectSessionReadyMsg with the session name so app.go can dispatch the
// tmux-attach via tea.ExecProcess.
func scaffoldAndStartCmd(submit newProjectSubmitMsg) tea.Cmd {
	return func() tea.Msg {
		opts := scaffold.Options{Name: submit.Name, Description: submit.Description}
		session, err := scaffold.StartSession(context.Background(), opts)
		if err != nil {
			return toastMsg{Text: "new project: " + err.Error(), Kind: toastError, Until: time.Now().Add(6 * time.Second)}
		}
		return projectSessionReadyMsg{Session: session}
	}
}

// upgradeCwdCmd injects the ccmux structure into the cwd (no .git changes,
// no Claude session started).
func upgradeCwdCmd() tea.Cmd {
	return func() tea.Msg {
		cwd, err := os.Getwd()
		if err != nil {
			return toastMsg{Text: "upgrade: " + err.Error(), Kind: toastError, Until: time.Now().Add(5 * time.Second)}
		}
		opts := scaffold.Options{Name: filepath.Base(cwd), Dir: cwd, SkipGit: true}
		if err := scaffold.Scaffold(&opts); err != nil {
			return toastMsg{Text: "upgrade: " + err.Error(), Kind: toastError, Until: time.Now().Add(5 * time.Second)}
		}
		return toastMsg{Text: "upgraded " + cwd, Kind: toastSuccess, Until: time.Now().Add(4 * time.Second)}
	}
}

// textInputBlink is a small wrapper around textinput.Blink so we don't have
// to import textinput in app.go.
func textInputBlink() tea.Cmd {
	// imported indirectly; the new-project form uses textinput which already
	// owns its blink scheduling. This is here for symmetry / future use.
	return nil
}

func isNarrow(width int) bool { return width < 80 }

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
