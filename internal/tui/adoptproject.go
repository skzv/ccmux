package tui

import (
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/tui/styles"
)

// adoptProjectModel is the modal shown when the user presses `A` on the
// Projects screen. Lists every directory under the projects root that
// isn't currently recognized as a project (no .git, no CLAUDE.md, no
// .ccmux/); selecting one writes the `.ccmux/` marker so Discover starts
// surfacing it.
//
// Adoption exists because Discover gates on those three markers — useful
// most of the time, but it leaves the user stuck with a directory they
// _know_ is a project (e.g. a worktree without CLAUDE.md, an extracted
// tarball, a scratch dir they want to start managing) and no in-app way
// to register it. Before this modal, the only fix was to drop the marker
// file manually from the shell.
type adoptProjectModel struct {
	st      styles.Styles
	root    string
	orphans []string // absolute paths, basename-sorted (see project.DiscoverOrphans)
	cursor  int
}

func newAdoptProject(st styles.Styles, root string, orphans []string) adoptProjectModel {
	return adoptProjectModel{st: st, root: root, orphans: orphans}
}

func (m adoptProjectModel) Update(msg tea.Msg) (adoptProjectModel, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch km.String() {
	case "esc":
		return m, func() tea.Msg { return adoptProjectCancelMsg{} }
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.orphans)-1 {
			m.cursor++
		}
	case "enter":
		if m.cursor < 0 || m.cursor >= len(m.orphans) {
			// Empty-list case: Enter is a no-op; the user has to esc.
			return m, nil
		}
		picked := m.orphans[m.cursor]
		return m, func() tea.Msg { return adoptProjectPickMsg{Path: picked} }
	}
	return m, nil
}

func (m adoptProjectModel) View(width int) string {
	st := m.st
	lines := []string{
		st.Emphasis.Render("Adopt directory"),
		st.Subtitle.Render("Register a directory under " + m.root + " as a ccmux project."),
		st.Muted.Render("Writes a .ccmux/ marker so Discover picks it up — no other files."),
		"",
	}
	if len(m.orphans) == 0 {
		lines = append(lines,
			st.Muted.Render("No unregistered directories found."),
			"",
			st.Muted.Render("Every directory under "+m.root+" is already a project,"),
			st.Muted.Render("or no plain directories exist there."),
			"",
			st.Muted.Render("esc: close"),
		)
		return st.PaneFocused.Width(width - 2).Render(strings.Join(lines, "\n"))
	}
	for i, p := range m.orphans {
		label := "  " + filepath.Base(p)
		if i == m.cursor {
			label = st.ListItemSelected.Render("▸ " + filepath.Base(p))
		} else {
			label = st.ListItem.Render(label)
		}
		lines = append(lines, label)
	}
	lines = append(lines, "", st.Muted.Render("↑/↓: move   enter: adopt   esc: cancel"))
	return st.PaneFocused.Width(width - 2).Render(strings.Join(lines, "\n"))
}
