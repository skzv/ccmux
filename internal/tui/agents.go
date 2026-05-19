package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/agent"
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
	}
	return m, nil
}

func (m agentsModel) View(width, height int) string {
	header := m.renderSubtabs()
	// Account for the header row + a blank line between header and body.
	bodyHeight := height - 2
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	var body string
	switch m.active {
	case agent.IDClaude:
		body = m.claude.View(width, bodyHeight)
	case agent.IDCodex:
		body = m.codex.View(width, bodyHeight)
	case agent.IDAntigravity:
		body = m.antigravity.View(width, bodyHeight)
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, body)
}

// renderSubtabs draws the [◆ Claude]  Codex  Antigravity row. Active tab
// gets the emphasis style + a diamond marker so it reads as the
// current selection even in a screen reader; inactive tabs are
// muted.
func (m agentsModel) renderSubtabs() string {
	parts := []string{}
	for _, a := range agent.All() {
		label := a.DisplayName()
		if a.ID() == m.active {
			parts = append(parts, m.st.Emphasis.Render("◆ "+label))
		} else {
			parts = append(parts, m.st.Muted.Render("  "+label))
		}
	}
	hint := "   " + m.st.Muted.Render("(tab / h·l: switch agent)")
	return strings.Join(parts, "   ") + hint
}

// nextAgentSubtab cycles sub-tabs in agent.All() order. Wraps at the
// ends so tab from Antigravity lands on Claude.
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
