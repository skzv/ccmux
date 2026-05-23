package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/conversations"
	"github.com/skzv/ccmux/internal/tmux"
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

func (m projectMenuModel) View(width int) string {
	st := m.st
	lines := []string{
		st.Emphasis.Render(m.project),
		st.Muted.Render(m.projectPath),
		"",
	}
	// Section headers are emitted lazily as the entry kind changes, so
	// "Running sessions" / "Past conversations" appear only when those
	// lists are non-empty.
	lastKind := projectMenuEntryKind(-1)
	for i, e := range m.entries {
		if e.kind != lastKind {
			lines = append(lines, st.Subtitle.Render(menuSectionHeader(e.kind)))
			lastKind = e.kind
		}
		text := m.entryLabel(e)
		if i == m.cursor {
			lines = append(lines, st.ListItemSelected.Render("▸ "+text))
		} else {
			lines = append(lines, st.ListItem.Render("  "+text))
		}
	}
	lines = append(lines, "", st.Muted.Render("↑/↓: move   enter: select   esc: cancel"))
	return st.PaneFocused.Width(width - 2).Render(strings.Join(lines, "\n"))
}

// menuSectionHeader is the subtitle shown above the first row of each
// entry kind.
func menuSectionHeader(k projectMenuEntryKind) string {
	switch k {
	case menuSession:
		return "Running sessions"
	case menuConversation:
		return "Past conversations"
	default:
		return "Actions"
	}
}

// entryLabel renders the one-line description for a row.
func (m projectMenuModel) entryLabel(e projectMenuEntry) string {
	switch e.kind {
	case menuSession:
		label := "attach   " + e.session.Name
		if e.session.Attached {
			label += "   " + m.st.Muted.Render("(attached)")
		}
		return label
	case menuConversation:
		name := agent.ByID(e.conv.Agent).DisplayName()
		preview := strings.TrimSpace(e.conv.Preview)
		if preview == "" {
			preview = e.conv.ID
		}
		return "resume   " + name + "  " + m.st.Muted.Render(truncRunes(preview, 48))
	default:
		return "＋ Start a new session"
	}
}

// truncRunes shortens `s` to at most `max` runes, appending an ellipsis
// when it cut. Rune-based so a multi-byte character is never split.
func truncRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
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
