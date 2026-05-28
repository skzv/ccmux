package tui

import (
	"strings"

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
}

func newAgents(st styles.Styles, km Keymap) agentsModel {
	return agentsModel{
		st:          st,
		km:          km,
		active:      agent.IDClaude,
		claude:      newClaude(st, km),
		codex:       newCodexConfig(st),
		antigravity: newAntigravityConfig(st),
	}
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
	// Sub-tab navigation comes first. We intentionally don't let h/l
	// reach the per-agent sub-models because Claude's screen doesn't
	// bind them anyway and the codex/antigravity sub-models are read-
	// mostly, so swallowing those keys is safe.
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "tab", "l":
			m.active = nextAgentSubtab(m.active, +1)
			return m, nil
		case "shift+tab", "h":
			m.active = nextAgentSubtab(m.active, -1)
			return m, nil
		}
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
	case agent.IDCursor, agent.IDPi, agent.IDGrok:
		// Cursor, pi, and grok are AGENTS.md-centric and manage their
		// own config via their CLI — no editable surface in ccmux.
		return m, nil
	}
	return m, nil
}

// HelpBarProps returns the screen-specific key hints for the Agents
// screen. The tab/shift-tab sub-tab cycling is a screen-local
// affordance so it earns a slot in the HelpBar.
func (m agentsModel) HelpBarProps(width int) components.HelpBarProps {
	return components.HelpBarProps{
		Hints: []components.KeyHint{
			{Key: "?", Label: "help", Priority: 10},
			{Key: "q", Label: "quit", Priority: 10},
			{Key: "tab", Label: "next agent", Priority: 6},
			{Key: "h/l", Label: "switch", Priority: 5},
			{Key: "e", Label: "edit", Priority: 4},
			{Key: "1-7", Label: "screens", Priority: 2},
		},
		Width: width,
	}
}

func (m agentsModel) View(width, height int) string {
	narrow := isNarrow(width)
	header := m.renderSubtabs(narrow)
	// Account for the header row + a blank line between header and body.
	bodyHeight := height - 2
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	bodyWidth := width
	if narrow && bodyWidth > 4 {
		bodyWidth -= 4
	}
	var body string
	switch m.active {
	case agent.IDClaude:
		body = m.claude.View(bodyWidth, bodyHeight)
	case agent.IDCodex:
		body = m.codex.View(bodyWidth, bodyHeight)
	case agent.IDAntigravity:
		body = m.antigravity.View(bodyWidth, bodyHeight)
	case agent.IDCursor:
		body = m.st.Muted.Render("Cursor settings are managed by Cursor CLI.")
	case agent.IDPi:
		body = m.st.Muted.Render("pi settings are managed by the pi CLI (~/.pi + AGENTS.md).")
	case agent.IDGrok:
		body = m.st.Muted.Render("Grok settings are managed by the grok CLI (~/.grok/config.toml + AGENTS.md).")
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, body)
}

// renderSubtabs draws the [◆ Claude]  Codex  Antigravity  Cursor row. Active tab
// gets the emphasis style + a diamond marker so it reads as the
// current selection even in a screen reader; inactive tabs are
// muted.
func (m agentsModel) renderSubtabs(narrow bool) string {
	parts := []string{}
	for _, a := range agent.All() {
		label := a.DisplayName()
		if a.ID() == m.active {
			parts = append(parts, m.st.Emphasis.Render("◆ "+label))
		} else {
			parts = append(parts, m.st.Muted.Render("  "+label))
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
