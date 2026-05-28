package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/tui/components"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// agentsModel is the screen formerly known as "Claude". With Codex
// and Antigravity now sharing config concerns, this screen owns a
// sub-tab row at the top and delegates Update/View to the active
// per-agent sub-model.
//
// Claude is the default sub-tab on entry — every user who knew this
// screen as the Claude screen lands on the same UI. tab / shift-tab
// (or h / l) cycles between sub-tabs.
//
// Why sub-tabs rather than three top-level screens: keeping all
// agent settings under one tab pairs with the per-project "switch
// agent" model. A user who flipped a project to Codex expects its
// settings to live next to Claude's, not on a separate number key
// that pushes the existing 1–7 nav past one-digit.
type agentsModel struct {
	st     styles.Styles
	km     Keymap
	active agent.ID

	claude      claudeModel
	codex       codexConfigModel
	antigravity antigravityConfigModel
	cursor      cursorAgentModel
}

func newAgents(st styles.Styles, km Keymap) agentsModel {
	return agentsModel{
		st:          st,
		km:          km,
		active:      agent.IDClaude,
		claude:      newClaude(st, km),
		codex:       newCodexConfig(st),
		antigravity: newAntigravityConfig(st),
		cursor:      newCursorAgent(st),
	}
}

// Init returns the startup commands sub-models need to begin
// background work. The Cursor sub-tab kicks off its first SQLite
// read (and spinner) so the data is ready by the time the user
// switches to that tab.
func (m agentsModel) Init() tea.Cmd {
	return m.cursor.Init()
}

// Reload is called by App on configReloadMsg (after $EDITOR returns
// or anywhere ccmux's own config changes). Each sub-model owns its
// own reload semantics; we just fan out.
func (m *agentsModel) Reload() {
	m.claude.reload()
	m.codex.reload()
	m.antigravity.reload()
}

func (m agentsModel) Update(msg tea.Msg) (agentsModel, tea.Cmd) {
	// Sub-tab navigation. `tab`/`shift+tab`/`h`/`l` cycle between
	// the four agent sub-tabs. The embedded browser inside each sub-
	// model owns `←`/`→` to swap pane focus between list and
	// preview, and `j/k/up/down/enter` for list nav + viewport
	// scroll — so tab and the arrow keys never collide.
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "tab", "l":
			m.active = nextAgentSubtab(m.active, +1)
			return m.onSubtabSwitch()
		case "shift+tab", "h":
			m.active = nextAgentSubtab(m.active, -1)
			return m.onSubtabSwitch()
		}
	}
	// The Cursor sub-model owns its own async messages (cursorLoadedMsg,
	// spinner.TickMsg) regardless of which sub-tab is active — the
	// spinner advances and the SQLite read completes in the background
	// so the data is warm by the time the user opens the Cursor tab.
	switch msg.(type) {
	case cursorLoadedMsg, spinner.TickMsg:
		c, cmd := m.cursor.Update(msg)
		m.cursor = c
		return m, cmd
	}
	// Delegate to the active sub-model.
	switch m.active {
	case agent.IDClaude:
		c, cmd := m.claude.Update(msg)
		m.claude = c
		return m, cmd
	case agent.IDCodex:
		c, cmd := m.codex.Update(msg)
		m.codex = c
		return m, cmd
	case agent.IDAntigravity:
		g, cmd := m.antigravity.Update(msg)
		m.antigravity = g
		return m, cmd
	case agent.IDCursor:
		c, cmd := m.cursor.Update(msg)
		m.cursor = c
		return m, cmd
	case agent.IDPi, agent.IDGrok:
		// pi and grok are AGENTS.md-centric and manage their own
		// config via their CLIs — no editable surface in ccmux yet,
		// so the sub-tab is a placeholder.
		return m, nil
	}
	return m, nil
}

// onSubtabSwitch refreshes per-sub-tab background data when the user
// flips to a new sub-tab. Today only the Cursor sub-tab needs this —
// its SQLite-backed data has a 30s TTL.
func (m agentsModel) onSubtabSwitch() (agentsModel, tea.Cmd) {
	if m.active == agent.IDCursor {
		c, cmd := m.cursor.EnsureFresh()
		m.cursor = c
		return m, cmd
	}
	return m, nil
}

// HelpBarProps returns the screen-specific key hints for the Agents
// screen. The hint line is per-sub-tab: keys that only apply to the
// Claude sub-tab (model picker, effort picker, c/j edit shortcuts)
// only appear when Claude is active, so the hints match the keys
// that actually do something at the cursor.
//
// Common hints (?, q, tab, h/l, 1-7) are present on every sub-tab.
// Sub-tab specifics:
//
//   - Claude: m model, e effort, a always, y yolo, c CLAUDE.md, j settings.json.
//   - Codex / Antigravity: r effort, y yolo, e edit.
//   - Cursor: (read-only) — no per-sub-tab keys.
func (m agentsModel) HelpBarProps(width int) components.HelpBarProps {
	hints := []components.KeyHint{
		{Key: "?", Label: "help", Priority: 10},
		{Key: "q", Label: "quit", Priority: 10},
		{Key: "tab", Label: "next agent", Priority: 6},
		{Key: "h/l", Label: "switch", Priority: 5},
		{Key: "←→", Label: "pane", Priority: 5},
	}
	switch m.active {
	case agent.IDClaude:
		hints = append(hints,
			components.KeyHint{Key: "m", Label: "model", Priority: 4},
			components.KeyHint{Key: "e", Label: "effort", Priority: 4},
			components.KeyHint{Key: "a", Label: "always", Priority: 3},
			components.KeyHint{Key: "y", Label: "yolo", Priority: 3},
			components.KeyHint{Key: "c", Label: "CLAUDE.md", Priority: 3},
			components.KeyHint{Key: "j", Label: "settings.json", Priority: 3},
		)
	case agent.IDCodex, agent.IDAntigravity:
		hints = append(hints,
			components.KeyHint{Key: "r", Label: "effort", Priority: 4},
			components.KeyHint{Key: "y", Label: "yolo", Priority: 4},
			components.KeyHint{Key: "e", Label: "edit", Priority: 3},
		)
	}
	hints = append(hints, components.KeyHint{Key: "1-7", Label: "screens", Priority: 2})
	return components.HelpBarProps{Hints: hints, Width: width}
}

func (m agentsModel) View(width, height int) string {
	narrow := isNarrow(width)
	header := m.renderSubtabs(narrow)
	// The sub-tab row + the active sub-model's body share one
	// bordered Pane so the whole Agents surface reads as one
	// cohesive block. Sub-models render their inner content
	// (`ViewBody`) un-bordered; agentsModel owns the chrome.
	innerW := width - 4
	if innerW < 4 {
		innerW = 4
	}
	innerH := height - 2 - lipgloss.Height(header) - 1 // top/bottom border + blank
	if innerH < 6 {
		innerH = 6
	}
	var body string
	switch m.active {
	case agent.IDClaude:
		body = m.claude.ViewBody(innerW, innerH)
	case agent.IDCodex:
		body = m.codex.ViewBody(innerW, innerH)
	case agent.IDAntigravity:
		body = m.antigravity.ViewBody(innerW, innerH)
	case agent.IDCursor:
		body = m.cursor.ViewBody(innerW, innerH)
	case agent.IDPi:
		body = m.st.Muted.Render("pi settings are managed by the pi CLI (~/.pi + AGENTS.md).")
	case agent.IDGrok:
		body = m.st.Muted.Render("Grok settings are managed by the grok CLI (~/.grok/config.toml + AGENTS.md).")
	}
	inner := lipgloss.JoinVertical(lipgloss.Left, header, "", body)
	return m.st.Pane.Width(width - 2).Height(height - 2).MaxWidth(width).Render(inner)
}

// renderSubtabs draws the • Claude  • Codex  • Antigravity  • Cursor
// row. Each sub-tab is preceded by a `•` dot in the agent's accent
// color (the same convention every other agent-navigation surface
// uses — the Projects legend + per-row dots, the Conversations
// agent nav, and the Agents browser section headers). The active
// sub-tab keeps the accent on the label text itself + bold weight;
// inactive sub-tabs drop the label to muted so the eye lands on the
// active one. The dot stays colored on every sub-tab so all four
// agents remain visually identifiable at a glance.
func (m agentsModel) renderSubtabs(narrow bool) string {
	parts := []string{}
	for _, a := range agent.All() {
		label := a.DisplayName()
		accent := m.st.AgentAccent(a.ID())
		dot := accent.Render("•")
		if a.ID() == m.active {
			parts = append(parts, dot+" "+accent.Bold(true).Render(label))
		} else {
			parts = append(parts, dot+" "+m.st.Muted.Render(label))
		}
	}
	// The "(tab / h·l: switch agent)" hint is T2 — dropped on narrow.
	if narrow {
		return strings.Join(parts, "\n")
	}
	subtabs := strings.Join(parts, "   ")
	return subtabs + "   " + m.st.Muted.Render("(tab / h·l: switch agent)")
}

// nextAgentSubtab cycles sub-tabs in agent.All() order. Wraps at the
// ends so tab from Cursor lands on Claude.
func nextAgentSubtab(cur agent.ID, dir int) agent.ID {
	all := agent.All()
	for i, a := range all {
		if a.ID() == cur {
			next := (i + dir + len(all)) % len(all)
			return all[next].ID()
		}
	}
	return all[0].ID()
}
