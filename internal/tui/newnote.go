package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/tui/styles"
)

// newNoteFormModel is the modal the Notes screen opens on `n`. Two
// fields — filename (required, pre-filled with a dated default under
// notes/) and an optional title that gets rendered as the file's
// leading H1. On Enter we emit a newNoteSubmitMsg with the chosen
// values; the screen handles the actual file write + $EDITOR
// hand-off. Esc cancels via newNoteCancelMsg.
//
// Field shape mirrors renameFormModel (single bubbles/textinput per
// row) so screens that already route through the modal-overlay
// pattern in app.go don't grow a second form lifecycle. The "huh"
// library is available in the project but routing a huh.Form through
// the existing app.Update chain would duplicate book-keeping that
// already lives in the textinput pattern.
type newNoteFormModel struct {
	st       styles.Styles
	filename textinput.Model
	title    textinput.Model
	focus    int // 0 = filename, 1 = title
	err      string
}

// newNoteFormFocusCount enumerates the two rows (filename, title)
// so the cycling math has one named constant to follow.
const newNoteFormFocusCount = 2

// defaultNewNoteFilename returns the suggested filename for a new
// note: `notes/note-YYYY-MM-DD-HHMM.md`. Dated so unattended
// successive presses don't overwrite each other. `now` is injectable
// for tests; production callers pass time.Now().
func defaultNewNoteFilename(now time.Time) string {
	return "notes/note-" + now.Format("2006-01-02-1504") + ".md"
}

func newNewNoteForm(st styles.Styles, now time.Time) newNoteFormModel {
	fn := textinput.New()
	fn.SetValue(defaultNewNoteFilename(now))
	fn.CharLimit = 200
	fn.Width = 60
	fn.Prompt = ""
	fn.Focus()

	tt := textinput.New()
	tt.Placeholder = "optional H1 title"
	tt.CharLimit = 120
	tt.Width = 60
	tt.Prompt = ""

	return newNoteFormModel{
		st:       st,
		filename: fn,
		title:    tt,
		focus:    0,
	}
}

func (m newNoteFormModel) Update(msg tea.Msg) (newNoteFormModel, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "esc":
			return m, func() tea.Msg { return newNoteCancelMsg{} }
		case "tab", "down":
			m.focus = (m.focus + 1) % newNoteFormFocusCount
			m.applyFocus()
			return m, textinput.Blink
		case "shift+tab", "up":
			m.focus = (m.focus + newNoteFormFocusCount - 1) % newNoteFormFocusCount
			m.applyFocus()
			return m, textinput.Blink
		case "enter":
			fn := strings.TrimSpace(m.filename.Value())
			if fn == "" {
				m.err = "filename is required"
				return m, nil
			}
			if !strings.HasSuffix(strings.ToLower(fn), ".md") {
				fn += ".md"
			}
			title := strings.TrimSpace(m.title.Value())
			return m, func() tea.Msg {
				return newNoteSubmitMsg{Filename: fn, Title: title}
			}
		}
	}
	var cmd tea.Cmd
	if m.focus == 0 {
		m.filename, cmd = m.filename.Update(msg)
	} else {
		m.title, cmd = m.title.Update(msg)
	}
	return m, cmd
}

func (m *newNoteFormModel) applyFocus() {
	if m.focus == 0 {
		m.filename.Focus()
		m.title.Blur()
	} else {
		m.title.Focus()
		m.filename.Blur()
	}
}

func (m newNoteFormModel) View(width int) string {
	st := m.st
	title := st.Emphasis.Render("New note")
	hint := st.Subtitle.Render("Creates the file under the project and opens it in $EDITOR.")

	filenameLabel := st.Muted.Render("filename  ")
	titleLabel := st.Muted.Render("title     ")
	filenameField := m.filename.View()
	titleField := m.title.View()

	rows := []*string{&filenameField, &titleField}
	for i, r := range rows {
		if i == m.focus {
			*r = st.Emphasis.Render("▌ ") + *r
		} else {
			*r = "  " + *r
		}
	}

	keys := st.Muted.Render("tab: next field   enter: create   esc: cancel")

	parts := []string{
		title,
		hint,
		"",
		filenameLabel + filenameField,
		titleLabel + titleField,
		"",
		keys,
	}
	if m.err != "" {
		parts = append(parts, st.StatusError.Render("⚠ "+m.err))
	}
	return st.PaneFocused.Width(width - 2).Render(strings.Join(parts, "\n"))
}
