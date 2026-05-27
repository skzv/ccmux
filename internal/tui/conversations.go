package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/conversations"
	"github.com/skzv/ccmux/internal/tui/components"
	"github.com/skzv/ccmux/internal/tui/styles"
)

type conversationAgentSectionDef struct {
	Label string
	Agent agent.ID
}

type conversationAgentSection struct {
	def           conversationAgentSectionDef
	conversations []conversations.Conversation
}

var conversationAgentSections = []conversationAgentSectionDef{
	{Label: "Claude", Agent: agent.IDClaude},
	{Label: "Codex", Agent: agent.IDCodex},
	{Label: "Cursor", Agent: agent.IDCursor},
	{Label: "Agy", Agent: agent.IDAntigravity},
}

func conversationAgentSectionIndex(id agent.ID) (int, bool) {
	for i, section := range conversationAgentSections {
		if section.Agent == id {
			return i, true
		}
	}
	return 0, false
}

// conversationsModel is the "past conversations" browser — every known
// agent session the user has had on disk, regardless of whether ccmux
// launched it. Two entry points feed the same list state:
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

	// activeSection is the index into conversationAgentSections. The
	// cursor is scoped to that section, while sectionCursors preserves
	// each agent's row position when focus moves across sections.
	activeSection  int
	cursor         int
	sectionCursors []int

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

	// showHeadless includes headless agent runs in the list (anything
	// Conversation.IsHeadless reports true for: Claude `claude -p` /
	// SDK / automation wrappers, Codex `codex exec`). Seeded from
	// config.Conversations.ShowHeadless; toggled live with H. Default
	// false hides them because automation runs accumulate fast and
	// drown out interactive sessions in the list. Apply path: this
	// flag flips to inverted ExcludeHeadless on conversations.Options
	// at refresh time — see App.refreshConversationsCmd.
	showHeadless bool
}

func newConversations(st styles.Styles, km Keymap) conversationsModel {
	m := conversationsModel{st: st, km: km}
	m.ensureSectionCursors()
	return m
}

// SetList replaces the conversation list and preserves the cursor by
// conversation ID across loads. Called by App when
// conversationsLoadedMsg lands.
func (m *conversationsModel) SetList(list []conversations.Conversation) {
	var selectedID string
	if sel := m.Selected(); sel != nil {
		selectedID = sel.ID
	}
	m.list = list
	m.loading = false
	m.loadErr = ""
	// A refresh invalidates any armed delete: the list the user armed
	// against is no longer the list on screen. Forcing a re-arm after
	// every reload is the safe choice for an irreversible action.
	m.pendingDelete = ""
	m.preserveSelectedID(selectedID)
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
// contains the given substring. Pass "" to clear. The selected
// conversation is preserved by ID when it remains visible; otherwise
// the cursor clamps inside the focused section.
func (m *conversationsModel) SetProjectFilter(filter string) {
	if m.projectFilter == filter {
		return
	}
	var selectedID string
	if sel := m.Selected(); sel != nil {
		selectedID = sel.ID
	}
	m.projectFilter = filter
	m.pendingDelete = ""
	m.preserveSelectedID(selectedID)
}

// SetShowHeadless seeds the live toggle from config at startup. The
// App reads config.Conversations.ShowHeadless and pushes the value
// here so the first refresh applies it. After startup, the user owns
// the flag via the H keybind — we don't re-read config every refresh.
func (m *conversationsModel) SetShowHeadless(b bool) {
	m.showHeadless = b
}

// ToggleHeadless flips the headless-visibility flag and reports the
// new value so the caller can rebuild the conversations list with the
// matching filter. Selection is preserved by ID until the refreshed
// list proves the row is no longer visible.
func (m *conversationsModel) ToggleHeadless() bool {
	var selectedID string
	if sel := m.Selected(); sel != nil {
		selectedID = sel.ID
	}
	m.showHeadless = !m.showHeadless
	m.pendingDelete = ""
	m.preserveSelectedID(selectedID)
	return m.showHeadless
}

// ShowHeadless reports the current state of the headless-visibility
// flag. Used by the View to label the toggle hint and by tests.
func (m conversationsModel) ShowHeadless() bool {
	return m.showHeadless
}

// Selected returns the conversation under the cursor, respecting the
// current filter. Returns nil when the (filtered) list is empty.
func (m conversationsModel) Selected() *conversations.Conversation {
	sections := m.sections()
	if m.activeSection < 0 || m.activeSection >= len(sections) {
		return nil
	}
	visible := sections[m.activeSection].conversations
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

func (m conversationsModel) sections() []conversationAgentSection {
	sections := make([]conversationAgentSection, len(conversationAgentSections))
	for i, def := range conversationAgentSections {
		sections[i] = conversationAgentSection{def: def}
	}
	for _, c := range m.filtered() {
		if i, ok := conversationAgentSectionIndex(c.Agent); ok {
			sections[i].conversations = append(sections[i].conversations, c)
		}
	}
	return sections
}

func (m *conversationsModel) ensureSectionCursors() {
	if len(m.sectionCursors) == len(conversationAgentSections) {
		if m.activeSection < 0 || m.activeSection >= len(conversationAgentSections) {
			m.activeSection = 0
			m.cursor = 0
		}
		return
	}
	next := make([]int, len(conversationAgentSections))
	copy(next, m.sectionCursors)
	m.sectionCursors = next
	if m.activeSection < 0 || m.activeSection >= len(conversationAgentSections) {
		m.activeSection = 0
	}
	m.sectionCursors[m.activeSection] = m.cursor
}

func (m *conversationsModel) preserveSelectedID(selectedID string) {
	sections := m.sections()
	m.ensureSectionCursors()
	if selectedID != "" {
		for sectionIdx, section := range sections {
			for rowIdx, c := range section.conversations {
				if c.ID == selectedID {
					m.activeSection = sectionIdx
					m.cursor = rowIdx
					m.sectionCursors[sectionIdx] = rowIdx
					m.clampSelection(sections)
					return
				}
			}
		}
	}
	m.clampSelection(sections)
}

func (m *conversationsModel) clampSelection(sections []conversationAgentSection) {
	m.ensureSectionCursors()
	if len(sections) == 0 {
		m.activeSection = 0
		m.cursor = 0
		return
	}
	if m.activeSection < 0 || m.activeSection >= len(sections) {
		m.activeSection = 0
		m.cursor = 0
	}
	m.sectionCursors[m.activeSection] = m.cursor
	for i, section := range sections {
		last := len(section.conversations) - 1
		switch {
		case last < 0:
			m.sectionCursors[i] = 0
		case m.sectionCursors[i] < 0:
			m.sectionCursors[i] = 0
		case m.sectionCursors[i] > last:
			m.sectionCursors[i] = last
		}
	}
	m.cursor = m.sectionCursors[m.activeSection]
}

func (m *conversationsModel) moveFocusedRow(delta int, sections []conversationAgentSection) {
	m.clampSelection(sections)
	if m.activeSection < 0 || m.activeSection >= len(sections) {
		return
	}
	rows := sections[m.activeSection].conversations
	if len(rows) == 0 {
		m.cursor = 0
		m.sectionCursors[m.activeSection] = 0
		return
	}
	next := m.cursor + delta
	if next < 0 {
		next = 0
	}
	if next >= len(rows) {
		next = len(rows) - 1
	}
	m.cursor = next
	m.sectionCursors[m.activeSection] = next
}

func (m *conversationsModel) moveFocusedSection(delta int, sections []conversationAgentSection) {
	m.clampSelection(sections)
	if len(conversationAgentSections) == 0 {
		return
	}
	m.sectionCursors[m.activeSection] = m.cursor
	m.activeSection = (m.activeSection + delta + len(conversationAgentSections)) % len(conversationAgentSections)
	m.cursor = m.sectionCursors[m.activeSection]
	m.clampSelection(sections)
}

func (m conversationsModel) focusedSectionDef() conversationAgentSectionDef {
	if m.activeSection < 0 || m.activeSection >= len(conversationAgentSections) {
		return conversationAgentSections[0]
	}
	return conversationAgentSections[m.activeSection]
}

func (m conversationsModel) Update(msg tea.Msg) (conversationsModel, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	sections := m.sections()
	switch {
	case keyMatches(km, m.km.Up):
		m.moveFocusedRow(-1, sections)
		// Moving off the armed row disarms — a delete confirm must be
		// two presses on the SAME row, never a stale arm + a fresh x.
		m.pendingDelete = ""
	case keyMatches(km, m.km.Down):
		m.moveFocusedRow(1, sections)
		m.pendingDelete = ""
	case keyMatches(km, m.km.Left) || km.String() == "shift+tab":
		m.moveFocusedSection(-1, sections)
		m.pendingDelete = ""
	case keyMatches(km, m.km.Right) || km.String() == "tab":
		m.moveFocusedSection(1, sections)
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

	sections := m.sections()
	m.clampSelection(sections)
	nav := m.renderAgentNav(sections)
	contentH := height - 6
	if contentH < 1 {
		contentH = 1
	}

	// Narrow: list only — drop the detail pane (T2) and the inline
	// hint line (T2). Shares the one TUI breakpoint via isNarrow.
	if isNarrow(width) {
		return st.Pane.Width(width - 2).Height(height - 2).Render(
			lipgloss.JoinVertical(lipgloss.Left, header, "", nav, "", m.renderList(sections, width-4, contentH)),
		)
	}

	// Wide: two-column — list on the left, detail on the right.
	listW := width * 5 / 8
	detailW := width - listW - 1
	list := m.renderList(sections, listW, contentH)
	var detail string
	if sel := m.Selected(); sel != nil {
		detail = m.renderDetail(*sel, detailW, contentH)
	} else {
		detail = m.renderEmptyDetail(m.focusedSectionDef(), detailW, contentH)
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, list, " ", detail)
	return st.Pane.Width(width - 2).Height(height - 2).Render(
		lipgloss.JoinVertical(lipgloss.Left, header, "", nav, "", body),
	)
}

func (m conversationsModel) renderAgentNav(sections []conversationAgentSection) string {
	parts := make([]string, 0, len(sections))
	for i, section := range sections {
		label := fmt.Sprintf("%s %d", section.def.Label, len(section.conversations))
		if i == m.activeSection {
			parts = append(parts, m.st.Emphasis.Render("▸ "+label))
			continue
		}
		parts = append(parts, m.st.Muted.Render("  "+label))
	}
	return strings.Join(parts, "  ")
}

// renderList draws only the focused agent's conversation list. The
// agent selector above it keeps the full section map visible without
// stacking every section into the scroll region.
func (m conversationsModel) renderList(sections []conversationAgentSection, width, height int) string {
	if height < 1 || len(sections) == 0 {
		return ""
	}
	sectionIdx := m.activeSection
	if sectionIdx < 0 || sectionIdx >= len(sections) {
		sectionIdx = 0
	}
	section := sections[sectionIdx]
	if len(section.conversations) == 0 {
		return components.RenderListRow(m.st, m.st.Muted.Render(m.emptySectionText(section.def)), false, width)
	}
	start, end := windowAroundCursor(m.cursor, len(section.conversations), height)
	rows := make([]string, 0, end-start)
	rowInner := width - 2 // RenderListRow eats 2 cells for the prefix
	for i := start; i < end; i++ {
		selected := i == m.cursor
		content := m.renderConversationRowContent(section.conversations[i], rowInner)
		rows = append(rows, components.RenderListRow(m.st, content, selected, width))
	}
	return strings.Join(rows, "\n")
}

// renderConversationRowContent formats the inner content of a single
// conversation row (agent label, timestamp, preview) sized to fit
// `width` cells. Selection treatment is applied by the caller via
// components.RenderListRow.
func (m conversationsModel) renderConversationRowContent(c conversations.Conversation, width int) string {
	const (
		agentW = 12
		timeW  = 12
	)
	agentLabel := conversationAgentLabel(c.Agent)
	when := relativeTimeShort(c.LastActivity)
	preview := c.Preview
	if preview == "" {
		preview = "(" + c.Project + ")"
	}
	previewBudget := width - agentW - timeW - 4
	if previewBudget < 10 {
		previewBudget = 10
	}
	// Armed-for-delete row: replace the preview with a loud confirm
	// prompt so the user can't miss what x-again will do.
	if c.ID == m.pendingDelete {
		return fmt.Sprintf("%-*s  %-*s  %s",
			agentW, m.st.Muted.Render(truncate(agentLabel, agentW)),
			timeW, m.st.Muted.Render(when),
			m.st.StatusError.Render("delete this conversation? press x to confirm · esc cancels"),
		)
	}
	return fmt.Sprintf("%-*s  %-*s  %s",
		agentW, m.st.Muted.Render(truncate(agentLabel, agentW)),
		timeW, m.st.Muted.Render(when),
		truncate(preview, previewBudget),
	)
}

// HelpBarProps returns the screen-specific key hints for
// Conversations. Replaces the legacy inline hint line that used to
// hang under the body.
func (m conversationsModel) HelpBarProps(width int) components.HelpBarProps {
	hStatus := "hidden"
	if m.showHeadless {
		hStatus = "shown"
	}
	return components.HelpBarProps{
		Hints: []components.KeyHint{
			{Key: "?", Label: "help", Priority: 10},
			{Key: "q", Label: "quit", Priority: 10},
			{Key: "enter", Label: "resume", Priority: 8},
			{Key: "x", Label: "delete", Priority: 6},
			{Key: "tab", Label: "sections", Priority: 5},
			{Key: "H", Label: "headless: " + hStatus, Priority: 4},
			{Key: "r", Label: "refresh", Priority: 3},
			{Key: "1-7", Label: "screens", Priority: 2},
		},
		Width: width,
	}
}

func conversationAgentLabel(id agent.ID) string {
	switch id {
	case agent.IDClaude:
		return "claude"
	case agent.IDCodex:
		return "[codex]"
	case agent.IDCursor:
		return "[cursor]"
	case agent.IDAntigravity:
		return "[agy]"
	default:
		return string(id)
	}
}

func (m conversationsModel) emptySectionText(def conversationAgentSectionDef) string {
	if m.projectFilter != "" {
		return "No conversations for " + def.Label + " matching filter."
	}
	return "No conversations for " + def.Label + "."
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
	if c.IsHeadless() {
		// Show *which* headless mode the row is — "SDK" for Claude
		// `sdk-cli`, "exec" for Codex `codex_exec`. A user who
		// opted-in to seeing headless rows shouldn't have to guess
		// which automation flavour they're about to resume.
		label := "headless"
		switch c.Entrypoint {
		case "sdk-cli":
			label = "headless / SDK"
		case "codex_exec":
			label = "headless / exec"
		}
		lines = append(lines, st.Muted.Render("Mode       ")+st.StatusError.Render(label))
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

func (m conversationsModel) renderEmptyDetail(def conversationAgentSectionDef, width, height int) string {
	lines := []string{
		m.st.Emphasis.Render(def.Label),
		"",
		m.st.Muted.Render(m.emptySectionText(def)),
	}
	if m.projectFilter != "" {
		lines = append(lines, "", m.st.Key.Render("esc")+"  clear project filter")
	}
	_ = width
	_ = height
	return strings.Join(lines, "\n")
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
