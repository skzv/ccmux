package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

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

const conversationColumnGap = 3

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

	// spinner animates next to the loading placeholder while the
	// initial walk (and any refresh) is in flight. Bubbles wants the
	// spinner.Tick command running to advance frames; the model owns
	// the state so it survives across View invocations.
	spinner spinner.Model

	// statsCache memoizes the per-conversation message count so the
	// detail pane can render "messages N" without re-walking the
	// transcript on every render. Populated by SetMessageCount when a
	// conversationStatsLoadedMsg lands. -1 is a sentinel for "the
	// walker errored on this transcript" so we don't re-fire forever.
	statsCache map[string]int

	// banner is the (pre-rendered) toast string the App injects when a
	// notification fires while the Conversations wide layout is on
	// screen. The detail pane surfaces it at the top of the side
	// pane instead of the global footer — closer to the action that
	// produced it (e.g. a delete on the focused row). Empty when no
	// toast is active or when the layout is narrow.
	banner string

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
	sp := spinner.New()
	// Meter renders a bar-style sweep ("▱▱▱" → "▰▰▰" → "▰▱▱") — closer
	// to a progress bar than a dot, which reads as "ccmux is working"
	// at a glance instead of a small twitch in the corner.
	sp.Spinner = spinner.Meter
	sp.Style = lipgloss.NewStyle().Foreground(st.Semantic.Primary).Bold(true)
	m := conversationsModel{
		st:         st,
		km:         km,
		spinner:    sp,
		statsCache: map[string]int{},
	}
	m.ensureSectionCursors()
	return m
}

// SetMessageCount records the lazy-loaded message-count result for one
// conversation. The detail pane reads from the cache; callers that
// drive the cache hit/miss check use LoadStatsCmd. A count of -1 means
// the walker errored — we keep it cached so we don't re-fire forever.
func (m *conversationsModel) SetMessageCount(id string, count int) {
	if m.statsCache == nil {
		m.statsCache = map[string]int{}
	}
	m.statsCache[id] = count
}

// LoadStatsCmd returns a Cmd that walks the selected conversation's
// transcript to count its messages, or nil when the count is already
// cached (or there's no selection). The App routes the result back
// through SetMessageCount via conversationStatsLoadedMsg. Cheap
// short-circuit on cache hit so it's safe to call after every cursor
// move.
func (m conversationsModel) LoadStatsCmd() tea.Cmd {
	sel := m.Selected()
	if sel == nil {
		return nil
	}
	if _, ok := m.statsCache[sel.ID]; ok {
		return nil
	}
	c := *sel
	return func() tea.Msg {
		n, err := conversations.CountMessages(c)
		return conversationStatsLoadedMsg{ID: c.ID, Count: n, Err: err}
	}
}

// SpinnerTickCmd returns the initial Tick command that drives the
// loading spinner's animation. The App fires this when entering the
// Conversations screen so the spinner has a frame source.
func (m conversationsModel) SpinnerTickCmd() tea.Cmd {
	return m.spinner.Tick
}

// SetBanner pushes a pre-rendered toast string into the detail pane.
// App calls this each frame on the Conversations screen — empty
// string clears the banner; non-empty replaces it. The toast lives in
// the side panel so the user sees the result of the action (delete,
// refresh) right where they were focused, not at the screen footer.
func (m *conversationsModel) SetBanner(s string) {
	m.banner = s
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
	// Spinner Tick advances the loading-spinner animation while the
	// initial walk (or a refresh) is in flight. We forward every
	// spinner.TickMsg so the spinner stays responsive even when the
	// user isn't pressing keys.
	if _, ok := msg.(spinner.TickMsg); ok {
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	sections := m.sections()
	// Selection-ID snapshot: navigation moves below may shift it, in
	// which case we fire the lazy stats load for the new row at the
	// bottom. Captured early so the switch arms below don't see a
	// stale value through Selected().
	prevSelID := ""
	if sel := m.Selected(); sel != nil {
		prevSelID = sel.ID
	}
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
	// If navigation actually moved the selection, fire the lazy stats
	// load for the new row. Skip when the selection is unchanged so
	// non-nav keys (like a no-op `x` on an empty list) keep their
	// previous nil-cmd contract.
	newSelID := ""
	if sel := m.Selected(); sel != nil {
		newSelID = sel.ID
	}
	if newSelID != prevSelID {
		return m, m.LoadStatsCmd()
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
		body := m.renderLoading(width-4, height-6)
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
	contentW := width - 4
	if contentW < 1 {
		contentW = 1
	}

	// Narrow: list only — drop the detail pane (T2) and the inline
	// hint line (T2). Shares the one TUI breakpoint via isNarrow.
	if isNarrow(width) {
		return st.Pane.Width(width - 2).Height(height - 2).Render(
			lipgloss.JoinVertical(lipgloss.Left, header, "", nav, "", m.renderList(sections, contentW, contentH)),
		)
	}

	// Wide: two framed columns — list on the left, detail on the
	// right — matching the Projects/Notes tab treatment.
	detailW := width / 2
	listW := width - detailW - conversationColumnGap
	if detailW < 1 {
		detailW = 1
	}
	list := m.renderListPanel(sections, listW, height)
	var detail string
	if sel := m.Selected(); sel != nil {
		detail = m.renderDetailPanel(*sel, detailW, height)
	} else {
		detail = m.renderEmptyDetailPanel(m.focusedSectionDef(), detailW, height)
	}
	return constrainBlockWidth(
		lipgloss.JoinHorizontal(lipgloss.Top, list, strings.Repeat(" ", conversationColumnGap), detail),
		width,
	)
}

func (m conversationsModel) renderListPanel(sections []conversationAgentSection, width, height int) string {
	header := m.st.Title.Render("Conversations")
	if m.projectFilter != "" {
		header = lipgloss.JoinHorizontal(lipgloss.Top, header,
			"  "+m.st.Muted.Render("filter: "+m.projectFilter+"  (esc to clear)"))
	}
	innerW := width - 4
	if innerW < 1 {
		innerW = 1
	}
	listH := height - 8
	if listH < 1 {
		listH = 1
	}
	body := lipgloss.JoinVertical(lipgloss.Left,
		header,
		"",
		m.renderAgentNav(sections),
		"",
		m.renderList(sections, innerW, listH),
	)
	return m.st.PaneFocused.Width(width - 2).Height(height - 2).Render(body)
}

func (m conversationsModel) renderDetailPanel(c conversations.Conversation, width, height int) string {
	innerW := width - 4
	if innerW < 1 {
		innerW = 1
	}
	body := m.renderDetail(c, innerW, height-4)
	body = m.prependBanner(body, innerW)
	return m.st.Pane.Width(width - 2).Height(height - 2).Render(body)
}

func (m conversationsModel) renderEmptyDetailPanel(def conversationAgentSectionDef, width, height int) string {
	innerW := width - 4
	if innerW < 1 {
		innerW = 1
	}
	body := m.renderEmptyDetail(def, innerW, height-4)
	body = m.prependBanner(body, innerW)
	return m.st.Pane.Width(width - 2).Height(height - 2).Render(body)
}

// prependBanner pastes the active toast banner above the side-pane
// body. Width-clamped to the inner pane width so a long toast can't
// blow past the panel border. No-op when banner is empty.
func (m conversationsModel) prependBanner(body string, width int) string {
	if m.banner == "" {
		return body
	}
	banner := lipgloss.NewStyle().Width(width).Render(m.banner)
	return lipgloss.JoinVertical(lipgloss.Left, banner, "", body)
}

// renderLoading produces the centered "scanning transcripts" block
// that fills the pane while the walker is in flight. The bar spinner
// + the per-agent dot column communicates "ccmux is reaching into
// each agent's directory" — much louder than a one-line muted hint.
func (m conversationsModel) renderLoading(width, height int) string {
	st := m.st
	heading := st.Title.Render("Scanning transcripts")
	bar := m.spinner.View()
	headLine := lipgloss.JoinHorizontal(lipgloss.Top, bar, "  ", heading)

	// Per-agent dot legend — each ● wears the agent's accent so the
	// user sees which sources ccmux is touching. The roots map to the
	// directories the walkers actually open; future agents land here
	// by extension.
	roots := map[agent.ID]string{
		agent.IDClaude:      "~/.claude/projects",
		agent.IDCodex:       "~/.codex/sessions",
		agent.IDCursor:      "~/.cursor/projects",
		agent.IDAntigravity: "~/.gemini",
	}
	var legend []string
	for _, def := range conversationAgentSections {
		dot := st.AgentAccent(def.Agent).Bold(true).Render("●")
		legend = append(legend,
			"  "+dot+"  "+
				st.Type.Body.Render(strings.ToLower(def.Label))+
				"  "+st.Muted.Render(roots[def.Agent]),
		)
	}
	block := lipgloss.JoinVertical(lipgloss.Left,
		headLine,
		"",
		strings.Join(legend, "\n"),
	)

	if height < lipgloss.Height(block) {
		height = lipgloss.Height(block)
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, block)
}

func (m conversationsModel) renderAgentNav(sections []conversationAgentSection) string {
	parts := make([]string, 0, len(sections))
	for i, section := range sections {
		label := fmt.Sprintf("%s %d", section.def.Label, len(section.conversations))
		if i == m.activeSection {
			// Active section heading carries the agent's accent colour
			// (Claude=mauve, Codex=sky, Antigravity=peach, Cursor=teal)
			// via the design-system helper.
			parts = append(parts, m.st.AgentAccent(section.def.Agent).Bold(true).Render("▸ "+label))
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
// conversation row (agent label, timestamp, preview / armed-delete
// chip) sized to fit `width` cells. The agent label column carries the
// agent's accent colour via the design-system helper; the rest of the
// row stays in the default / muted foreground so the colour doesn't
// dominate Codex-heavy or Antigravity-heavy lists. Selection treatment
// is applied by the caller via components.RenderListRow.
func (m conversationsModel) renderConversationRowContent(c conversations.Conversation, width int) string {
	const (
		agentW = 7
		timeW  = 6
	)
	agentLabel := truncateDisplay(conversationAgentLabel(c.Agent), agentW)
	agentCol := m.st.AgentAccent(c.Agent).Render(agentLabel)
	when := relativeTimeShort(c.LastActivity)
	whenCol := m.st.Muted.Render(truncateDisplay(when, timeW))
	prefix := agentCol + "  " + whenCol + "  "
	remaining := width - lipgloss.Width(prefix)
	if remaining < 1 {
		return truncateDisplay(prefix, width)
	}
	preview := c.Preview
	if preview == "" {
		preview = "(" + c.Project + ")"
	}
	// Armed-for-delete row: render a bracketed danger chip at the
	// row's trailing edge so the agent label + timestamp + a
	// truncated preview stay visible. The user still sees which row
	// they armed; the chip says what `x` will do next.
	if c.ID == m.pendingDelete {
		chip := m.st.StatusError.Render("[delete? x to confirm · esc]")
		chipW := lipgloss.Width(chip)
		if chipW >= remaining {
			return truncateDisplay(prefix+chip, width)
		}
		previewBudget := remaining - chipW - 2
		if previewBudget < 0 {
			previewBudget = 0
		}
		previewText := truncateDisplay(preview, previewBudget)
		gap := remaining - lipgloss.Width(previewText) - chipW
		if gap < 1 {
			gap = 1
		}
		return truncateDisplay(prefix+previewText+strings.Repeat(" ", gap)+chip, width)
	}
	return truncateDisplay(prefix+truncateDisplay(preview, remaining), width)
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
			{Key: "p", Label: "preview", Priority: 7},
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
		return "codex"
	case agent.IDCursor:
		return "cursor"
	case agent.IDAntigravity:
		return "agy"
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
// The layout is intentionally flat and compressed: agent name as the
// accent heading, the project path beneath it as the only identifier
// (the agent's UUID is debugging-only and not human-readable), then a
// small column of facts, the first-prompt recap, and the action keys.
//
// Metadata sources:
//   - last active  — Conversation.LastActivity, rendered as a long
//     relative form ("5 hours ago", "yesterday").
//   - messages     — lazy-loaded via LoadStatsCmd; "…" while the walker
//     is still running, omitted on walker error.
//   - mode         — only when c.IsHeadless(), so the user knows
//     they're about to resume a sdk / exec run.
//   - first prompt — Conversation.Preview, the first ~100 chars of
//     the conversation's first user message.
func (m conversationsModel) renderDetail(c conversations.Conversation, width, height int) string {
	st := m.st
	indent := strings.Repeat(" ", st.Spacing.MD)

	lines := []string{
		st.AgentAccent(c.Agent).Bold(true).Render(string(c.Agent)),
		st.Muted.Render(wrapDetailText(displayPath(c.Project), width)),
		"",
	}

	const labelW = 12
	lines = append(lines, indent+st.Muted.Render(padLabel("last active", labelW))+"  "+relativeTimeLong(c.LastActivity))
	if count, ok := m.statsCache[c.ID]; ok && count >= 0 {
		lines = append(lines, indent+st.Muted.Render(padLabel("messages", labelW))+"  "+fmt.Sprintf("%d", count))
	} else if !ok {
		lines = append(lines, indent+st.Muted.Render(padLabel("messages", labelW))+"  "+st.Muted.Render("…"))
	}
	if c.IsHeadless() {
		label := "headless"
		switch c.Entrypoint {
		case "sdk-cli":
			label = "headless / SDK"
		case "codex_exec":
			label = "headless / exec"
		}
		lines = append(lines, indent+st.Muted.Render(padLabel("mode", labelW))+"  "+st.StatusError.Render(label))
	}

	if c.Preview != "" {
		previewW := width - lipgloss.Width(indent)
		if previewW < 1 {
			previewW = 1
		}
		lines = append(lines, "", st.Muted.Render("First prompt"), indentBlock(wrapDetailText(c.Preview, previewW), indent))
	}

	// No keybind hints in the side pane — the screen-wide HelpBar at
	// the bottom already advertises enter / p / x. The armed-delete
	// case is communicated by the chip on the row itself.

	_ = height
	return constrainBlockWidth(strings.Join(lines, "\n"), width)
}

// padLabel right-pads a label to a fixed width so the value column
// in the detail pane lines up. Plain spaces (not lipgloss padding) so
// the result composes cleanly with Render() calls.
func padLabel(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

// displayPath collapses $HOME → "~" so the detail pane reads
// "~/Projects/auth-redesign" instead of "/Users/skz/Projects/...".
// Falls back to the literal path when $HOME isn't set or doesn't
// prefix the project path. Empty paths surface as "(unknown)".
func displayPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "(unknown)"
	}
	home := homeDirForDisplay()
	if home == "" || !strings.HasPrefix(p, home) {
		return p
	}
	rest := strings.TrimPrefix(p, home)
	if rest == "" {
		return "~"
	}
	if !strings.HasPrefix(rest, "/") {
		return p
	}
	return "~" + rest
}

// homeDirForDisplay is a thin os.UserHomeDir wrapper; tests stub via a
// hook so display tests don't depend on the real $HOME of the process.
var homeDirForDisplay = func() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
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

// relativeTimeLong is the human-readable form used in the detail
// pane: "5h ago", "3 days ago", "yesterday", "just now". The detail
// pane has the room for words; list rows still use the compact
// relativeTimeShort form so the timestamp column stays narrow.
func relativeTimeLong(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		n := int(d.Minutes())
		if n == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", n)
	case d < 24*time.Hour:
		n := int(d.Hours())
		if n == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", n)
	case d < 2*24*time.Hour:
		return "yesterday"
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	default:
		return t.Format("Jan 02")
	}
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

func wrapDetailText(s string, width int) string {
	if width <= 0 {
		return s
	}
	return ansi.Wrap(s, width, "/._")
}

func truncateDisplay(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return ansi.Truncate(s, width, "…")
}

func indentBlock(s, indent string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = indent + line
		}
	}
	return strings.Join(lines, "\n")
}

func constrainBlockWidth(s string, width int) string {
	if width <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if lipgloss.Width(line) > width {
			lines[i] = ansi.Hardwrap(line, width, false)
		}
	}
	return strings.Join(lines, "\n")
}
