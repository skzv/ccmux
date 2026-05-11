package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/tui/styles"
)

// claudeModel is the Claude Code config screen.
// v0.1 scaffolds the screen and lists what it will manage.
type claudeModel struct {
	st styles.Styles
	km Keymap
}

func newClaude(st styles.Styles, km Keymap) claudeModel {
	return claudeModel{st: st, km: km}
}

func (m claudeModel) Update(msg tea.Msg) (claudeModel, tea.Cmd) {
	return m, nil
}

func (m claudeModel) View(width, height int) string {
	lines := []string{
		m.st.Emphasis.Render("Claude Code Configuration"),
		"",
		"Manages " + m.st.Muted.Render("~/.claude/") + " and per-project " + m.st.Muted.Render(".claude/") + " — without you opening JSON files.",
		"",
		m.st.Subtitle.Render("Sections (coming next)"),
		"  • " + m.st.Key.Render("M") + "  Model — pick default (Opus 4.7 / Sonnet 4.6 / Haiku 4.5 / opusplan / custom). Global or per-project.",
		"  • " + m.st.Key.Render("C") + "  CLAUDE.md — view/edit global and per-project agent instructions.",
		"  • " + m.st.Key.Render("S") + "  Slash command aliases (~/.claude/commands/) — list, preview, create from template.",
		"  • " + m.st.Key.Render("K") + "  Skills (~/.claude/skills/) — manage installed skills.",
		"  • " + m.st.Key.Render("P") + "  Permissions — allow/deny lists for tool use.",
		"  • " + m.st.Key.Render("H") + "  Hooks — pre/post-tool, on Stop, on UserPromptSubmit.",
		"  • " + m.st.Key.Render("X") + "  MCP servers — add, remove, toggle. Common servers pre-listed.",
		"  • " + m.st.Key.Render("E") + "  Effective config — merged view (global > project > local > env) for debugging.",
		"",
		m.st.Subtitle.Render("Every write is backed up first. Roll back via " + m.st.Key.Render("u") + "."),
	}
	body := strings.Join(lines, "\n")
	return m.st.Pane.Width(width - 2).Height(height).Render(body)
}
