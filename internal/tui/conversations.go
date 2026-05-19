package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/conversations"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// conversationsModel is the "past conversations" browser — every
// Claude / Codex / Antigravity session the user has had on disk,
// regardless of whether ccmux launched it. Two entry points feed the
// same list state:
//
//  1. `8` from any screen → no filter, show all conversations.
//  2. `c` on a Projects row → pre-applies `projectFilter` to the
//     selected project's path, so the list reads "conversations for
//     this project."
//
// The data layer (internal/conversations) is shared with the
// `ccmux list-conversations` CLI; this screen is its TUI face.
type conversationsModel struct {
	st styles.Styles
	km Keymap

	// list is the latest-load. Bubble Tea pattern: App fires a
	// refresh command on screen entry, the result lands here via
	// conversationsLoadedMsg. The full list is kept here; the
	// rendered slice is whatever passes the filter at render time.
	list []conversations.Conversation

	// cursor is the index into the *filtered* slice (not list).
	// Preserved across refreshes by ID rather than position so a new
	// conversation appearing at the top doesn't shift the selection.
	cursor int

	// projectFilter narrows the rendered list to conversations whose
	// Project matches this string (substring match). Empty = no
	// filter (the global view). Set by the App when the user enters
	// the screen via the Projects-tab `c` keybind.
	projectFilter string

	// loadErr holds the last walker error so the screen can surface
	// it instead of going silent. Cleared on a successful load.
	loadErr string

	// loading is true between refresh-request and conversationsLoadedMsg
	// landing. Drives the "Loading…" placeholder so an empty list
	// during a slow filesystem walk doesn't look like "no
	// conversations."
	loading bool

	// pendingDelete holds the conversation ID currently armed for
	// deletion. Deleting a transcript is irreversible, so `x` arms
	// rather than acts: first `x` sets this to the selected row's ID
	// and the row shows a "press x to confirm" warning; a second `x`
	// on the same row fires the delete. Esc, moving the cursor, or a
	// refresh all disarm — the user can't accidentally confirm a
	// delete they armed minutes ago on a different row.
	pendingDelete string
}

func newConversations(st styles.Styles, km Keymap) conversationsModel {
	return conversationsModel{st: st, km: km}
}

// SetList replaces the conversation list and preserves the cursor by
// conversation ID across loads. Called by App when
// conversationsLoadedMsg lands.
func (m *conversationsModel) SetList(list []conversations.Conversation) {
	var selectedID string
	visible := m.filtered()
	if m.cursor >= 0 && m.cursor < len(visible) {
		selectedID = visible[m.cursor].ID
	}
	m.list = list
	m.loading = false
	m.loadErr = ""
	// A refresh invalidates any armed delete: the list the user armed
	// against is no longer the list on screen. Forcing a re-arm after
	// every reload is the safe choice for an irreversible action.
	m.pendingDelete = ""

	visible = m.filtered()
	if selectedID != "" {
		for i, c := range visible {
			if c.ID == selectedID {
				m.cursor = i
				return
			}
		}
	}
	if m.cursor >= len(visible) {
		m.cursor = max0(len(visible) - 1)
	}
}

// SetLoadErr stashes a walker error so View can surface it. Empty
// string clears the previous error.
func (m *conversationsModel) SetLoadErr(err string) {
	m.loadErr = err
	m.loading = false
}

// SetLoading toggles the loading placeholder. App calls SetLoading(true)
// when issuing a refresh and (false) on completion via SetList /
// SetLoadErr.
func (m *conversationsModel) SetLoading(b bool) {
	m.loading = b
}

// SetProjectFilter narrows the list to conversations whose Project
// contains the given substring. Pass "" to clear. The cursor resets
// to the top when the filter changes — a stale cursor pointing into
// the old filtered slice would be confusing after the filter scope
// shifts.
func (m *conversationsModel) SetProjectFilter(filter string) {
	if m.projectFilter != filter {
		m.cursor = 0
	}
	m.projectFilter = filter
}

// Selected returns the conversation under the cursor, respecting the
// current filter. Returns nil when the (filtered) list is empty.
func (m conversationsModel) Selected() *conversations.Conversation {
	visible := m.filtered()
	if m.cursor < 0 || m.cursor >= len(visible) {
		return nil
	}
	c := visible[m.cursor]
	return &c
}

// filtered returns the slice of conversations matching the current
// projectFilter. Empty filter returns the full list. Filter match is
// case-insensitive substring on the Project field.
func (m conversationsModel) filtered() []conversations.Conversation {
	if m.projectFilter == "" {
		return m.list
	}
	needle := strings.ToLower(m.projectFilter)
	out := make([]conversations.Conversation, 0, len(m.list))
	for _, c := range m.list {
		if strings.Contains(strings.ToLower(c.Project), needle) {
			out = append(out, c)
		}
	}
	return out
}

func (m conversationsModel) Update(msg tea.Msg) (conversationsModel, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch {
	case keyMatches(km, m.km.Up):
		if m.cursor > 0 {
			m.cursor--
		}
		// Moving off the armed row disarms — a delete confirm must be
		// two presses on the SAME row, never a stale arm + a fresh x.
		m.pendingDelete = ""
	case keyMatches(km, m.km.Down):
		if m.cursor < len(m.filtered())-1 {
			m.cursor++
		}
		m.pendingDelete = ""
	case keyMatches(km, m.km.Kill):
		// `x`: arm-then-confirm delete of the selected conversation.
		sel := m.Selected()
		if sel == nil {
			return m, nil
		}
		if m.pendingDelete == sel.ID {
			// Second x on the armed row → fire the delete.
			c := *sel
			m.pendingDelete = ""
			return m, func() tea.Msg {
				err := conversations.Delete(c)
				return conversationDeletedMsg{ID: c.ID, Agent: string(c.Agent), Err: err}
			}
		}
		// First x → arm this row.
		m.pendingDelete = sel.ID
	case km.String() == "esc":
		// Esc disarms a pending delete first; only if nothing is armed
		// does it fall through to clearing the project filter. This
		// ordering means a user who armed a delete and changed their
		// mind hits esc once to back out, not twice.
		if m.pendingDelete != "" {
			m.pendingDelete = ""
			return m, nil
		}
		// Esc clears the project filter (returns to the global view).
		// Doesn't navigate away — the user can still hit 1-8 for that.
		if m.projectFilter != "" {
			m.projectFilter = ""
			m.cursor = 0
		}
	}
	return m, nil
}

func (m conversationsModel) View(width, height int) string {
	st := m.st
	header := st.Title.Render("Conversations")
	if m.projectFilter != "" {
		header = lipgloss.JoinHorizontal(lipgloss.Top, header,
			"  "+st.Muted.Render("filter: "+m.projectFilter+"  (esc to clear)"))
	}

	if m.loading {
		body := st.Muted.Render("Loading conversations from ~/.claude, ~/.codex, ~/.gemini/antigravity-cli …")
		return st.Pane.Width(width - 2).Height(height - 2).Render(
			lipgloss.JoinVertical(lipgloss.Left, header, "", body),
		)
	}
	if m.loadErr != "" {
		body := st.StatusError.Render("⚠ " + m.loadErr)
		return st.Pane.Width(width - 2).Height(height - 2).Render(
			lipgloss.JoinVertical(lipgloss.Left, header, "", body),
		)
	}

	visible := m.filtered()
	if len(visible) == 0 {
		var body string
		if m.projectFilter != "" {
			body = st.Muted.Render("No conversations for project " + st.Emphasis.Render(m.projectFilter) + ".")
		} else {
			body = st.Muted.Render("No conversations found. Run claude / codex / agy at least once to create transcripts.")
		}
		return st.Pane.Width(width - 2).Height(height - 2).Render(
			lipgloss.JoinVertical(lipgloss.Left, header, "", body),
		)
	}

	// Two-column layout: list on the left, detail on the right.
	listW := width * 5 / 8
	detailW := width - listW - 1
	if detailW < 20 {
		// Narrow terminal: drop the detail pane.
		return st.Pane.Width(width - 2).Height(height - 2).Render(
			lipgloss.JoinVertical(lipgloss.Left, header, "", m.renderList(visible, listW, height-4)),
		)
	}

	list := m.renderList(visible, listW, height-4)
	detail := m.renderDetail(visible[m.cursor], detailW, height-4)
	body := lipgloss.JoinHorizontal(lipgloss.Top, list, " ", detail)
	hint := st.Muted.Render("enter resume · x delete · esc clear filter · 1-8 switch screens")
	return st.Pane.Width(width - 2).Height(height - 2).Render(
		lipgloss.JoinVertical(lipgloss.Left, header, "", body, hint),
	)
}

// renderList draws the scrollable conversation list. Each row shows
// agent, relative time, project (or "—" for Antigravity rows that
// don't carry one), and a preview snippet.
func (m conversationsModel) renderList(visible []conversations.Conversation, width, height int) string {
	rows := make([]string, 0, len(visible))
	const (
		agentW = 12
		timeW  = 12
	)
	for i, c := range visible {
		marker := "  "
		if i == m.cursor {
			marker = m.st.Emphasis.Render("▸ ")
		}
		agentLabel := string(c.Agent)
		// Tag with brackets to match the dashboard styling for
		// non-default agents.
		if c.Agent != agent.IDClaude {
			agentLabel = "[" + agentLabel + "]"
		}
		when := relativeTimeShort(c.LastActivity)
		preview := c.Preview
		if preview == "" {
			preview = "(" + c.Project + ")"
		}
		// Width budget: agent + when + spaces are fixed; preview takes
		// the rest. Truncate the preview to avoid wrap.
		previewBudget := width - len(marker) - agentW - timeW - 4
		if previewBudget < 10 {
			previewBudget = 10
		}
		// Armed-for-delete row: replace the preview with a loud
		// confirm prompt so the user can't miss what x-again will do.
		if c.ID == m.pendingDelete {
			row := fmt.Sprintf("%s%-*s  %-*s  %s",
				marker,
				agentW, m.st.Muted.Render(truncate(agentLabel, agentW)),
				timeW, m.st.Muted.Render(when),
				m.st.StatusError.Render("delete this conversation? press x to confirm · esc cancels"),
			)
			rows = append(rows, row)
			if len(rows) >= height {
				break
			}
			continue
		}
		row := fmt.Sprintf("%s%-*s  %-*s  %s",
			marker,
			agentW, m.st.Muted.Render(truncate(agentLabel, agentW)),
			timeW, m.st.Muted.Render(when),
			truncate(preview, previewBudget),
		)
		if i == m.cursor {
			row = m.st.ListItemSelected.Render(row)
		}
		rows = append(rows, row)
		if len(rows) >= height {
			break
		}
	}
	return strings.Join(rows, "\n")
}

// renderDetail draws the right-hand pane for the selected conversation.
// Shows full ID, project, last-activity timestamp, preview, and the
// resume keybind hint.
func (m conversationsModel) renderDetail(c conversations.Conversation, width, height int) string {
	st := m.st
	lines := []string{
		st.Emphasis.Render(string(c.Agent)),
		"",
		st.Muted.Render("ID         ") + c.ID,
		st.Muted.Render("Project    ") + emptyOr(c.Project, "(unknown)"),
		st.Muted.Render("Last active") + "  " + c.LastActivity.Format("2006-01-02 15:04"),
	}
	if c.Preview != "" {
		lines = append(lines, "", st.Muted.Render("Preview"), wrap(c.Preview, width-2))
	}
	lines = append(lines, "", st.Key.Render("enter")+"  resume this conversation in a new tmux session")
	if m.pendingDelete == c.ID {
		lines = append(lines, st.StatusError.Render("x")+"    press x again to confirm delete · esc to cancel")
	} else {
		lines = append(lines, st.Key.Render("x")+"    delete this conversation")
	}
	body := strings.Join(lines, "\n")
	_ = height // reserved for future scrolling
	return body
}

// relativeTimeShort is the compact form used in list rows: "5m",
// "2h", "3d", "Apr-30". Longer than 30 days gets the date.
func relativeTimeShort(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return t.Format("Jan-02")
	}
}

func emptyOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// wrap is a minimal word-wrapper for the detail-pane preview. We
// don't pull in a wrapping library because the existing screens hand-
// roll the same trick.
func wrap(s string, width int) string {
	if width <= 0 {
		return s
	}
	var b strings.Builder
	col := 0
	for _, word := range strings.Fields(s) {
		w := len(word)
		if col == 0 {
			b.WriteString(word)
			col = w
			continue
		}
		if col+1+w > width {
			b.WriteByte('\n')
			b.WriteString(word)
			col = w
			continue
		}
		b.WriteByte(' ')
		b.WriteString(word)
		col += 1 + w
	}
	return b.String()
}
