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
	fn.Width = newNoteInputWidth
	fn.Prompt = ""
	fn.Focus()

	tt := textinput.New()
	tt.Placeholder = tr("optional H1 title")
	tt.CharLimit = 120
	tt.Width = newNoteInputWidth
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
				m.err = tr("filename is required")
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

// newNoteInputWidth is the inputs' width on a wide form.
const newNoteInputWidth = 60

// fieldLayout lays the form's rows out one line each, as in the
// new-session form: the label column fits the translated labels and the
// inputs get what is left (a fixed 60-cell input after the label used
// to wrap the filename on a narrow screen).
func (m newNoteFormModel) fieldLayout(width int) (labels []string, labelW, fieldW int) {
	textW := width - 4 // the pane's border + padding
	labels = []string{tr("filename"), tr("title")}
	labelW = labelColumn(10, textW/2, labels...)
	return labels, labelW, maxInt(4, textW-labelW-2)
}

// FitTo sizes the inputs for a width-wide form (see fitInput). The
// Notes screen calls it before each render, so the size sticks and
// typing scrolls within the field.
func (m *newNoteFormModel) FitTo(width int) {
	_, _, fieldW := m.fieldLayout(width)
	fitInput(&m.filename, minInt(newNoteInputWidth, fieldW-1))
	fitInput(&m.title, minInt(newNoteInputWidth, fieldW-1))
}

func (m newNoteFormModel) View(width int) string {
	st := m.st
	title := st.Emphasis.Render(tr("New note"))
	hint := st.Subtitle.Render(tr("Creates the file under the project and opens it in $EDITOR."))

	labels, labelW, fieldW := m.fieldLayout(width)
	filenameLabel := st.Muted.Render(columnLabel(labels[0], labelW))
	titleLabel := st.Muted.Render(columnLabel(labels[1], labelW))
	m.FitTo(width)
	filenameField := truncate(m.filename.View(), fieldW)
	titleField := truncate(m.title.View(), fieldW)

	rows := []*string{&filenameField, &titleField}
	for i, r := range rows {
		if i == m.focus {
			*r = st.Emphasis.Render("▌ ") + *r
		} else {
			*r = "  " + *r
		}
	}

	keys := st.Muted.Render(tr("tab: next field   enter: create   esc: cancel"))

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
