package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/conversations"
	"github.com/skzv/ccmux/internal/tmux"
	"github.com/skzv/ccmux/internal/tui/components"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// projectMenuEntryKind tags a row in the project menu modal.
type projectMenuEntryKind int

const (
	menuSession projectMenuEntryKind = iota
	menuConversation
	menuNewSession
)

// projectMenuEntry is one selectable row in the project menu.
type projectMenuEntry struct {
	kind    projectMenuEntryKind
	session tmux.Session               // populated when kind == menuSession
	conv    conversations.Conversation // populated when kind == menuConversation
}

// projectMenuModel is the modal shown when the user opens a project on
// the Projects screen. It lists the project's running tmux sessions and
// its past agent conversations, plus a "Start a new session" action;
// selecting a row attaches, resumes, or creates accordingly. Esc
// cancels. It replaces the older rejoin/new picker, which could only
// offer the project's single canonical session.
type projectMenuModel struct {
	st          styles.Styles
	project     string // display name
	projectPath string
	entries     []projectMenuEntry
	cursor      int
}

// newProjectMenu builds the modal from the project's running sessions
// and past conversations. The "Start a new session" entry is always
// appended last, so that action stays available even when both lists
// have content.
func newProjectMenu(st styles.Styles, project, projectPath string, sessions []tmux.Session, convs []conversations.Conversation) projectMenuModel {
	entries := make([]projectMenuEntry, 0, len(sessions)+len(convs)+1)
	for _, s := range sessions {
		entries = append(entries, projectMenuEntry{kind: menuSession, session: s})
	}
	for _, c := range convs {
		entries = append(entries, projectMenuEntry{kind: menuConversation, conv: c})
	}
	entries = append(entries, projectMenuEntry{kind: menuNewSession})
	cursor := 0
	if len(sessions) == 0 && len(convs) > 0 {
		cursor = len(entries) - 1
	}
	return projectMenuModel{st: st, project: project, projectPath: projectPath, entries: entries, cursor: cursor}
}

// hasContent reports whether the menu lists anything beyond the
// always-present "Start a new session" action. App uses it to skip the
// modal entirely for a project with no sessions and no history —
// pressing Enter there just creates and attaches, rather than popping a
// one-item menu.
func (m projectMenuModel) hasContent() bool {
	return len(m.entries) > 1
}

func (m projectMenuModel) Update(msg tea.Msg) (projectMenuModel, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch km.String() {
	case "esc":
		return m, func() tea.Msg { return projectMenuCancelMsg{} }
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.entries)-1 {
			m.cursor++
		}
	case "enter":
		if m.cursor >= 0 && m.cursor < len(m.entries) {
			picked := m.entries[m.cursor]
			project, path := m.project, m.projectPath
			return m, func() tea.Msg {
				return projectMenuPickMsg{Project: project, ProjectPath: path, Entry: picked}
			}
		}
	}
	return m, nil
}

// View renders the modal into at most `height` lines. The rows scroll
// with the cursor: a project with dozens of past conversations used to
// render every row, so the modal outgrew the screen and — since the
// cursor starts on the trailing "Start a new session" row when nothing
// is running — the highlighted row and the key-hint footer were cut
// off the bottom.
func (m projectMenuModel) View(width, height int) string {
	st := m.st
	rowW := width - 4
	head := []string{
		st.Emphasis.Render(truncate(m.project, rowW)),
		st.Muted.Render(truncate(m.projectPath, rowW)),
		"",
	}
	footer := []string{"", st.Muted.Render(truncate(tr("↑/↓: move   enter: select   esc: cancel"), rowW))}

	// Section headers are emitted lazily as the entry kind changes, so
	// "Running sessions" / "Past conversations" appear only when those
	// lists are non-empty. Rows are one line each (labels truncated to
	// the row width) so the window arithmetic below is exact.
	var rows []string
	var isHeader []bool
	cursorRow := 0
	lastKind := projectMenuEntryKind(-1)
	for i, e := range m.entries {
		if e.kind != lastKind {
			rows = append(rows, st.Subtitle.Render(truncate(menuSectionHeader(e.kind), rowW)))
			isHeader = append(isHeader, true)
			lastKind = e.kind
		}
		if i == m.cursor {
			cursorRow = len(rows)
		}
		text := truncate(m.entryLabel(e), rowW-2)
		rows = append(rows, components.RenderListRow(st, text, i == m.cursor, rowW))
		isHeader = append(isHeader, false)
	}

	// Pane border takes 2 lines; the title block and footer the rest.
	budget := height - 2 - len(head) - len(footer)
	if budget < 1 {
		budget = 1
	}
	start, end := windowAroundCursor(cursorRow, len(rows), budget)
	// Keep a section's header on screen with its first entry when the
	// window has scrolled to exactly that entry.
	if start > 0 && start == cursorRow && isHeader[start-1] && end-1 > cursorRow {
		start--
		end--
	}
	lines := append(append(head, rows[start:end]...), footer...)
	return st.PaneFocused.Width(width - 2).Render(strings.Join(lines, "\n"))
}

// menuSectionHeader is the subtitle shown above the first row of each
// entry kind.
func menuSectionHeader(k projectMenuEntryKind) string {
	switch k {
	case menuSession:
		return tr("Running sessions")
	case menuConversation:
		return tr("Past conversations")
	default:
		return tr("Actions")
	}
}

// entryLabel renders the one-line description for a row.
func (m projectMenuModel) entryLabel(e projectMenuEntry) string {
	switch e.kind {
	case menuSession:
		label := padLabel(tr("attach"), 8) + e.session.Name
		if e.session.Attached {
			label += "   " + m.st.Muted.Render(tr("(attached)"))
		}
		return label
	case menuConversation:
		name := agent.ByID(e.conv.Agent).DisplayName()
		preview := strings.TrimSpace(e.conv.Preview)
		if preview == "" {
			preview = e.conv.ID
		}
		// truncate is the canonical ANSI-aware helper (sessions.go);
		// it replaced a rune-count-based local (truncRunes) that could
		// overflow the column budget on wide (CJK) characters.
		return padLabel(tr("resume"), 8) + name + "  " + m.st.Muted.Render(truncate(preview, 48))
	default:
		return tr("＋ Start a new session")
	}
}

// projectMenuMsg opens the project menu modal. attachOrCreateLocal
// emits it after gathering the project's running sessions and past
// conversations.
type projectMenuMsg struct {
	Project       string
	ProjectPath   string
	Sessions      []tmux.Session
	Conversations []conversations.Conversation
}

// projectMenuPickMsg is emitted by projectMenuModel on Enter — the user
// selected a row. App dispatches it: attach to the session, resume the
// conversation, or create a new session.
type projectMenuPickMsg struct {
	Project     string
	ProjectPath string
	Entry       projectMenuEntry
}

// projectMenuCancelMsg is emitted by the menu on Esc.
type projectMenuCancelMsg struct{}
