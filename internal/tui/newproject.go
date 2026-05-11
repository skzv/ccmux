package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/tui/styles"
)

// newProjectFormModel is the modal form rendered over the Projects screen
// when the user presses `n` to create a new project. Two fields: Name
// (required) and Description (optional but recommended — Claude sees it as
// the first prompt).
type newProjectFormModel struct {
	st      styles.Styles
	name    textinput.Model
	desc    textinput.Model
	focus   int // 0 = name, 1 = desc
	err     string
}

func newNewProjectForm(st styles.Styles) newProjectFormModel {
	n := textinput.New()
	n.Placeholder = "my-project"
	n.CharLimit = 64
	n.Width = 40
	n.Prompt = ""
	n.Focus()

	d := textinput.New()
	d.Placeholder = "what are you building? (one sentence; Claude sees this as your first message)"
	d.CharLimit = 240
	d.Width = 70
	d.Prompt = ""

	return newProjectFormModel{st: st, name: n, desc: d, focus: 0}
}

func (m newProjectFormModel) Update(msg tea.Msg) (newProjectFormModel, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "esc":
			return m, func() tea.Msg { return newProjectCancelMsg{} }
		case "tab", "down", "shift+tab", "up":
			m.focus = 1 - m.focus
			if m.focus == 0 {
				m.name.Focus()
				m.desc.Blur()
			} else {
				m.name.Blur()
				m.desc.Focus()
			}
			return m, textinput.Blink
		case "enter":
			name := strings.TrimSpace(m.name.Value())
			if name == "" {
				m.err = "name is required"
				return m, nil
			}
			return m, func() tea.Msg {
				return newProjectSubmitMsg{
					Name:        name,
					Description: strings.TrimSpace(m.desc.Value()),
				}
			}
		}
	}
	var cmd tea.Cmd
	if m.focus == 0 {
		m.name, cmd = m.name.Update(msg)
	} else {
		m.desc, cmd = m.desc.Update(msg)
	}
	return m, cmd
}

// View returns a rendered modal sized to the available width. The caller
// places it inside an outer Pane; we don't draw our own border.
func (m newProjectFormModel) View(width int) string {
	st := m.st
	title := st.Emphasis.Render("New project")
	hint := st.Subtitle.Render("ccmux scaffolds the dirs, starts Claude, and sends your description as the first prompt — no /init friction.")

	nameLabel := st.Muted.Render("name        ")
	descLabel := st.Muted.Render("description ")
	nameField := m.name.View()
	descField := m.desc.View()
	if m.focus == 0 {
		nameField = st.Emphasis.Render("▌ ") + nameField
		descField = "  " + descField
	} else {
		nameField = "  " + nameField
		descField = st.Emphasis.Render("▌ ") + descField
	}

	keys := st.Muted.Render("tab: next field   enter: create   esc: cancel")

	parts := []string{
		title,
		hint,
		"",
		nameLabel + nameField,
		descLabel + descField,
		"",
		keys,
	}
	if m.err != "" {
		parts = append(parts, st.StatusError.Render("⚠ "+m.err))
	}
	return st.PaneFocused.Width(width - 2).Render(strings.Join(parts, "\n"))
}
