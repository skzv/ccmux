package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/tui/styles"
)

// notesModel is the Notes tab (per-project docs/ browser with rendered preview).
// v0.1 scaffolds the screen with the intended layout; populating the tree
// and wiring up Glamour live in the next ticket.
type notesModel struct {
	st styles.Styles
	km Keymap
}

func newNotes(st styles.Styles, km Keymap) notesModel {
	return notesModel{st: st, km: km}
}

func (m notesModel) Update(msg tea.Msg) (notesModel, tea.Cmd) {
	return m, nil
}

func (m notesModel) View(width, height int) string {
	lines := []string{
		m.st.Emphasis.Render("Notes"),
		"",
		m.st.Muted.Render("Per-project docs/ browser with Glamour-rendered preview."),
		"",
		"Coming next:",
		"  • tree view of docs/01_Specs, docs/02_Architecture, docs/03_Agent_Logs",
		"  • " + m.st.Key.Render("n") + " new note (agent log / spec / ADR), auto-templated",
		"  • " + m.st.Key.Render("e") + " edit in $EDITOR",
		"  • " + m.st.Key.Render("/") + " ripgrep search",
		"  • " + m.st.Key.Render("o") + " open in Obsidian (macOS desktop, if installed)",
		"",
		m.st.Subtitle.Render("Plain markdown in docs/ is the source of truth. No required sync."),
	}
	body := strings.Join(lines, "\n")
	return m.st.Pane.Width(width - 2).Height(height).Render(body)
}
