package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/tui/styles"
)

// projectSessionPickerModel is the modal that opens when the user presses
// Enter on a project that already has a running session. Two fields:
//
//	action   Rejoin (attach to the existing session) or Start new
//	name     tmux name for the new session (only used for Start new)
//
// Tab cycles focus; ←/→ switches the action; Enter submits; Esc cancels.
// Layout mirrors the new-project and new-session forms so the interaction
// feels familiar.
type projectSessionPickerModel struct {
	st          styles.Styles
	existing    string // the already-running session name
	project     string // project display name
	projectPath string // working directory for the new session

	// focus: 0 = action picker, 1 = name field
	focus     int
	actionIdx int // 0 = rejoin, 1 = start new
	nameInput textinput.Model

	err string
}

const (
	pickFocusAction = 0
	pickFocusName   = 1
	pickFocusCount  = 2
)

var pickActions = []string{"Rejoin", "Start new"}

func newProjectSessionPicker(st styles.Styles, existing, project, projectPath, suggestedName string) projectSessionPickerModel {
	n := textinput.New()
	n.SetValue(suggestedName)
	n.CharLimit = 64
	n.Width = 40
	n.Prompt = ""

	return projectSessionPickerModel{
		st:          st,
		existing:    existing,
		project:     project,
		projectPath: projectPath,
		focus:       0,
		actionIdx:   0, // default to Rejoin
		nameInput:   n,
	}
}

func (m projectSessionPickerModel) Update(msg tea.Msg) (projectSessionPickerModel, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "esc":
			return m, func() tea.Msg { return projectSessionPickCancelMsg{} }
		case "tab", "down":
			m.focus = (m.focus + 1) % pickFocusCount
			m.applyFocus()
			return m, textinput.Blink
		case "shift+tab", "up":
			m.focus = (m.focus + pickFocusCount - 1) % pickFocusCount
			m.applyFocus()
			return m, textinput.Blink
		case "left":
			if m.focus == pickFocusAction {
				m.actionIdx = (m.actionIdx + len(pickActions) - 1) % len(pickActions)
				return m, nil
			}
		case "right":
			if m.focus == pickFocusAction {
				m.actionIdx = (m.actionIdx + 1) % len(pickActions)
				return m, nil
			}
		case "enter":
			return m.submit()
		}
	}
	if m.focus == pickFocusName {
		var cmd tea.Cmd
		m.nameInput, cmd = m.nameInput.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m projectSessionPickerModel) submit() (projectSessionPickerModel, tea.Cmd) {
	if m.actionIdx == 0 {
		// Rejoin — name field irrelevant.
		return m, func() tea.Msg {
			return projectSessionPickMsg{
				Action:      "rejoin",
				Existing:    m.existing,
				Project:     m.project,
				ProjectPath: m.projectPath,
			}
		}
	}
	// Start new — validate name.
	name := strings.TrimSpace(m.nameInput.Value())
	if name == "" {
		m.err = "session name is required"
		return m, nil
	}
	return m, func() tea.Msg {
		return projectSessionPickMsg{
			Action:      "new",
			Existing:    m.existing,
			NewName:     name,
			Project:     m.project,
			ProjectPath: m.projectPath,
		}
	}
}

func (m *projectSessionPickerModel) applyFocus() {
	if m.focus == pickFocusName {
		m.nameInput.Focus()
	} else {
		m.nameInput.Blur()
	}
}

func (m projectSessionPickerModel) View(width int) string {
	st := m.st

	title := st.Emphasis.Render(fmt.Sprintf("Session for %s", m.project))
	hint := st.Muted.Render(fmt.Sprintf("%q is already running.", m.existing))

	actionLabel := st.Muted.Render("action  ")
	nameLabel := st.Muted.Render("name    ")

	actionField := m.renderActionPicker()
	nameField := m.nameInput.View()

	rows := []*string{&actionField, &nameField}
	for i, r := range rows {
		if i == m.focus {
			*r = st.Emphasis.Render("▌ ") + *r
		} else {
			*r = "  " + *r
		}
	}

	keys := st.Muted.Render("tab: next field   ←/→: switch action   enter: confirm   esc: cancel")
	parts := []string{
		title,
		hint,
		"",
		actionLabel + actionField,
		nameLabel + nameField,
		"",
		keys,
	}
	if m.err != "" {
		parts = append(parts, st.StatusError.Render("⚠ "+m.err))
	}
	return st.PaneFocused.Width(width - 2).Render(strings.Join(parts, "\n"))
}

func (m projectSessionPickerModel) renderActionPicker() string {
	cur := pickActions[m.actionIdx]
	if len(pickActions) <= 1 {
		return m.st.Muted.Render(cur)
	}
	hint := fmt.Sprintf("%d of %d", m.actionIdx+1, len(pickActions))
	if m.focus == pickFocusAction {
		return "‹ " + m.st.Emphasis.Render(cur) + " ›   " + m.st.Muted.Render("("+hint+")")
	}
	return cur + "   " + m.st.Muted.Render("("+hint+")")
}
