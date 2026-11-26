package tui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/tui/styles"
)

// quickReplyFormModel is the single-line modal the Sessions screen opens
// on `i`. Pressing Enter sends the reply text + Enter into the target
// session's active agent pane without attaching; Esc cancels.
type quickReplyFormModel struct {
	st      styles.Styles
	host    string
	session string
	input   textinput.Model
	err     string
}

func newQuickReplyForm(st styles.Styles, host, session string) quickReplyFormModel {
	ti := textinput.New()
	ti.Placeholder = tr("type reply (e.g. y, continue, …)")
	ti.CharLimit = 512
	ti.Width = 52
	ti.Prompt = ""
	ti.Focus()

	return quickReplyFormModel{
		st:      st,
		host:    host,
		session: session,
		input:   ti,
	}
}

func (m quickReplyFormModel) Update(msg tea.Msg) (quickReplyFormModel, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "esc":
			return m, func() tea.Msg { return quickReplyCancelMsg{} }
		case "enter":
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				m.err = tr("reply cannot be empty")
				return m, nil
			}
			return m, func() tea.Msg {
				return quickReplySubmitMsg{Host: m.host, Name: m.session, Text: text}
			}
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m quickReplyFormModel) View(width int) string {
	st := m.st
	target := m.session
	if !isLocalSessionHost(m.host) {
		target = fmt.Sprintf("%s @%s", m.session, m.host)
	}
	title := st.Emphasis.Render(tr("Quick reply") + " → " + target)
	hint := st.Subtitle.Render(tr("Sends text + Enter to the session's agent pane without attaching."))

	label := st.Muted.Render(padLabel(tr("reply"), 8))
	field := st.Emphasis.Render("▌ ") + m.input.View()
	keys := st.Muted.Render(tr("enter: send   esc: cancel"))

	parts := []string{title, hint, "", label + field, "", keys}
	if m.err != "" {
		parts = append(parts, st.StatusError.Render("⚠ "+m.err))
	}
	frameW := max(20, width-2)
	return st.PaneFocused.Width(frameW).Render(strings.Join(parts, "\n"))
}

// sgrOrEscapeRE matches any ESC-introduced terminal sequence (CSI, OSC,
// DCS, or two-byte escape). sanitizePreviewANSI keeps ONLY safe SGR color
// sequences (`ESC [ <digits/;> m`) and strips everything else (cursor moves,
// OSC title/clipboard writes, screen clears, BEL, raw control chars) so
// ANSI-colored tmux captures can never corrupt the TUI frame.
var (
	ansiEscRE = regexp.MustCompile(`\x1b(?:\][^\x07\x1b]*(?:\x07|\x1b\\)|P[^\x1b]*\x1b\\|\[[0-?]*[ -/]*[@-~]|[@-Z\\-_])`)
	safeSGRRE = regexp.MustCompile(`^\x1b\[[0-9;]*m$`)
)

func sanitizePreviewANSI(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "")
	cleaned := ansiEscRE.ReplaceAllStringFunc(s, func(seq string) string {
		if safeSGRRE.MatchString(seq) {
			return seq
		}
		return ""
	})
	var b strings.Builder
	b.Grow(len(cleaned))
	for i := 0; i < len(cleaned); i++ {
		c := cleaned[i]
		if c == '\x1b' || c == '\n' || c == '\t' || c >= 0x20 {
			b.WriteByte(c)
		}
	}
	return b.String()
}
