package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/tui/styles"
)

// renameFormModel is the single-field modal the Sessions screen opens on `R`.
// Pre-filled with the current session name; Enter renames, Esc cancels.
type renameFormModel struct {
	st      styles.Styles
	oldName string
	input   textinput.Model
	err     string
}

func newRenameForm(st styles.Styles, currentName string) renameFormModel {
	ti := textinput.New()
	ti.SetValue(currentName)
	ti.CharLimit = 64
	ti.Width = 40
	ti.Prompt = ""
	ti.Focus()

	return renameFormModel{
		st:      st,
		oldName: currentName,
		input:   ti,
	}
}

func (m renameFormModel) Update(msg tea.Msg) (renameFormModel, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "esc":
			return m, func() tea.Msg { return renameSessionCancelMsg{} }
		case "enter":
			newName := strings.TrimSpace(m.input.Value())
			if newName == "" {
				m.err = "name cannot be empty"
				return m, nil
			}
			if newName == m.oldName {
				// No-op — dismiss without a round-trip to tmux.
				return m, func() tea.Msg { return renameSessionCancelMsg{} }
			}
			return m, func() tea.Msg {
				return renameSessionSubmitMsg{OldName: m.oldName, NewName: newName}
			}
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m renameFormModel) View(width int) string {
	st := m.st
	title := st.Emphasis.Render("Rename session")
	hint := st.Subtitle.Render("Edit the tmux session name and press enter.")

	label := st.Muted.Render("name  ")
	field := st.Emphasis.Render("▌ ") + m.input.View()
	keys := st.Muted.Render("enter: confirm   esc: cancel")

	parts := []string{title, hint, "", label + field, "", keys}
	if m.err != "" {
		parts = append(parts, st.StatusError.Render("⚠ "+m.err))
	}
	return st.PaneFocused.Width(width - 2).Render(strings.Join(parts, "\n"))
}
