package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/tui/components"
)

// HelpBarProps returns the screen-specific key hints for Notes.
// Replaces the legacy inline hint row that used to sit under the
// project name in renderList. `n` is now wired (creates a note in
// the project and opens $EDITOR); `i` opens the note-info overlay.
func (m notesModel) HelpBarProps(width int) components.HelpBarProps {
	return components.HelpBarProps{
		Hints: []components.KeyHint{
			{Key: "?", Label: tr("help"), Priority: 10},
			{Key: "q", Label: tr("quit"), Priority: 10},
			{Key: "enter", Label: tr("open"), Priority: 8},
			{Key: "→/←", Label: tr("expand/collapse"), Priority: 8},
			{Key: "n", Label: tr("new note"), Priority: 8},
			{Key: "e", Label: tr("edit"), Priority: 7},
			{Key: "/", Label: tr("search"), Priority: 6},
			{Key: "i", Label: tr("info"), Priority: 6},
			{Key: "p / space", Label: tr("switch project"), Priority: 5},
			{Key: "H", Label: tr("device"), Priority: 5},
			{Key: "tab", Label: tr("focus preview"), Priority: 4},
			{Key: "1-7", Label: tr("screens"), Priority: 2},
		},
		Width: width,
	}
}

func (m notesModel) View(width, height int) string {
	// The picker comes first: it is how a Notes screen with no project
	// gets one. Drawn after the no-project placeholder, it was open but
	// invisible there, silently swallowing every key but esc.
	if m.pickingProject {
		return m.renderProjectPicker(width, height)
	}
	if m.project == nil {
		bodyLines := []string{
			m.st.Emphasis.Render(tr("Notes")),
			"",
			m.st.Muted.Render(tr("No project selected.")),
			"",
			fmt.Sprintf(tr("Press %s here to pick one, or %s to go to the Projects tab."),
				m.st.Key.Render("p"), m.st.Key.Render(screenKey(ScreenProjects))),
		}
		if len(m.selectableDeviceLabels()) > 1 {
			bodyLines = append(bodyLines,
				"",
				tr("Viewing device: ")+m.st.HostColor(m.deviceName).Render(hostLabel(m.deviceName, m.hosts))+
					fmt.Sprintf(tr("  — press %s to switch device."), m.st.Key.Render("H")),
			)
		}
		return m.st.Pane.Width(width - 2).Height(height - 2).Render(strings.Join(bodyLines, "\n"))
	}
	if m.newNoteForm != nil {
		modalW := minInt(80, width-4)
		// Through the pointer, so the fitted input width persists into
		// the form's Update (see FitTo).
		m.newNoteForm.FitTo(modalW)
		modal := m.newNoteForm.View(modalW)
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
	}
	if m.noteInfo.open {
		return m.renderNoteInfoOverlay(width, height)
	}
	// Notes uses a tighter narrow threshold than the rest of the
	// TUI: at 100+ cols the 1/3 + 2/3 split is still readable
	// (preview gets ~66 cols), so we'd rather show the preview
	// than collapse to list-only. Zoomed README GIFs (~116 cols)
	// in particular need this — the previous isNarrow(120) cutoff
	// hid the preview in every demo.
	if width < notesPreviewMinWidth {
		return m.renderListOnly(width, height)
	}
	leftW := width / 3
	rightW := width - leftW - 1
	return lipgloss.JoinHorizontal(lipgloss.Top,
		m.renderList(leftW, height, false),
		" ",
		m.renderPreview(rightW, height),
	)
}

// renderList draws the note list. `narrow` is the terminal's narrow
// state (not derived from `width`, which in wide mode is only the
// left sub-pane): on narrow the T2 key-hint line is dropped.
func (m notesModel) renderList(width, height int, narrow bool) string {
	// This input survives Settings language changes; translate at render time.
	m.searchInput.Placeholder = tr("search this project's notes…")
	focusMark := ""
	if m.focus == focusList {
		focusMark = m.st.Emphasis.Render(" ◀")
	}
	header := m.st.Emphasis.Render(m.project.Name) + focusMark
	lines := []string{header, m.deviceHeaderLine()}
	if m.deviceErr != "" {
		lines = append(lines, m.st.StatusError.Render("⚠ "+m.deviceErr))
	}

	// Search box / search-results banner.
	if m.searching && m.searchInput.Focused() {
		lines = append(lines, "", m.searchInput.View())
	} else if m.hasActiveSearch() {
		lines = append(lines, "",
			m.st.Emphasis.Render(fmt.Sprintf("search: %q", m.searchQuery)),
			m.st.Muted.Render(fmt.Sprintf("%d hit(s) — esc clears, enter opens", len(m.searchResults))),
		)
		if m.searchPartial {
			lines = append(lines, m.st.StatusWarning.Render("⚠ "+tr("search timed out — partial results")))
		}
	}
	lines = append(lines, "")

	rows, cursorRow := m.noteRows(width)
	if len(rows) == 0 {
		var empty string
		switch {
		case m.loading:
			empty = m.loadingSpinner.View() + m.st.Muted.Render(tr(" scanning project for markdown…"))
		case m.hasActiveSearch():
			empty = m.st.Muted.Render(tr("(no matches)"))
		default:
			empty = m.st.Muted.Render(tr("(no markdown files yet — press n to create one)"))
		}
		lines = append(lines, empty)
		return m.listPaneStyle().Width(width - 2).Height(height - 2).Render(strings.Join(lines, "\n"))
	}

	// Window the (potentially long) file list to whatever vertical room
	// is left, keeping the cursor row on screen. -1 reserves the line
	// the "N more" hint occupies.
	budget := height - 2 - len(lines) - 1
	if budget < 1 {
		budget = 1
	}
	visible, above, below := windowLines(rows, cursorRow, budget)
	lines = append(lines, visible...)
	if above > 0 || below > 0 {
		lines = append(lines, m.st.Muted.Render(scrollHintText(above, below)))
	}
	return m.listPaneStyle().Width(width - 2).Height(height - 2).Render(strings.Join(lines, "\n"))
}

// listPaneStyle returns the pane chrome for the left (list) column.
// Focused only when m.focus == focusList so the highlighted border
// follows the user's active pane — tabbing right unfocuses this
// pane and the border drops back to the muted Pane treatment.
func (m notesModel) listPaneStyle() lipgloss.Style {
	if m.focus == focusList {
		return m.st.PaneFocused
	}
	return m.st.Pane
}

// noteRows builds the scrollable region of the Notes list — the
// folder-grouped file list, or the flat search-hit list when a query
// is active. It returns the rendered rows and the index of the row
// under the cursor (-1 when the region is empty), so renderList can
// window the rows around it.
func (m notesModel) noteRows(width int) (rows []string, cursorRow int) {
	cursorRow = -1
	// Row width matches the pane interior (width - 4 for border + Padding(0,1)).
	rowW := width - 4
	if m.hasActiveSearch() {
		for i, h := range m.searchResults {
			chip := m.st.Muted.Render(fmt.Sprintf("[%s:%d]", h.Rel, h.LineNum))
			snippet := truncateSearchSnippet(h.Snippet, width-14)
			content := chip + "  " + snippet
			if i == m.cursor {
				cursorRow = len(rows)
			}
			rows = append(rows, components.RenderListRow(m.st, content, i == m.cursor, rowW))
		}
		return rows, cursorRow
	}
	// Sub-section indent scales with tree depth so docs/01_Specs/ sits
	// visually beneath docs/, not flush-left with it. The left pane is
	// only ~width/3 cells wide so we use the tight SM step (1 cell) —
	// the hierarchy reads clearly without eating the visible filename
	// column. Top-level folders sit at indent 0; root-level files are
	// shown bare at the top. A folder header carries a fold glyph (▸
	// collapsed, ▾ expanded). Folder and file rows both pass through
	// RenderListRow so the selected row gets the unified accent bar.
	step := m.st.Spacing.SM
	for i, r := range m.visibleRows() {
		sel := i == m.cursor
		if sel {
			cursorRow = len(rows)
		}
		if r.kind == rowFolder {
			level := strings.Count(r.dir, "/") // top-level folder == 0
			indent := strings.Repeat(" ", level*step)
			glyph := "▸ "
			if m.expanded[r.dir] {
				glyph = "▾ "
			}
			content := m.st.Subtitle.Render(glyph + folderHeader(r.dir))
			bodyW := rowW - level*step
			if bodyW < 1 {
				bodyW = 1
			}
			rows = append(rows, indent+components.RenderListRow(m.st, content, sel, bodyW))
			continue
		}
		// File row: root files at indent 0, files in a folder one step
		// deeper than the folder header.
		level := 0
		if r.dir != "" {
			level = strings.Count(r.dir, "/") + 1
		}
		indent := strings.Repeat(" ", level*step)
		bodyW := rowW - level*step
		if bodyW < 1 {
			bodyW = 1
		}
		rows = append(rows, indent+components.RenderListRow(m.st, m.entries[r.entryIdx].Display, sel, bodyW))
	}
	return rows, cursorRow
}

// folderHeader renders the group header for a folder of notes. The
// project root (Dir == "") is labelled explicitly so root-level
// files like README.md don't sit under a blank heading. For nested
// folders the header shows only the last path segment (`docs/01_Specs`
// → `01_Specs/`) — the depth-based indent conveys where the folder
// sits, so repeating the parent path on every header is redundant
// and eats column space in the narrow left pane.
func folderHeader(dir string) string {
	if dir == "" {
		return "(project root)"
	}
	last := dir
	if i := strings.LastIndex(dir, "/"); i >= 0 {
		last = dir[i+1:]
	}
	return last + "/"
}

// windowLines returns the slice of `rows` to display given a vertical
// `budget`, keeping the `cursor` row visible, plus the count of rows
// hidden above and below the window. When everything fits it returns
// the full slice with zero hidden counts.
func windowLines(rows []string, cursor, budget int) (visible []string, above, below int) {
	n := len(rows)
	if budget < 1 {
		budget = 1
	}
	if n <= budget {
		return rows, 0, 0
	}
	start := 0
	if cursor >= 0 {
		start = cursor - budget/2
	}
	if start < 0 {
		start = 0
	}
	if start+budget > n {
		start = n - budget
	}
	end := start + budget
	return rows[start:end], start, n - end
}

// scrollHintText is the one-line "N more" affordance shown below a
// windowed list when rows are hidden above and/or below.
func scrollHintText(above, below int) string {
	switch {
	case above > 0 && below > 0:
		return fmt.Sprintf("↑ %d more   ↓ %d more", above, below)
	case above > 0:
		return fmt.Sprintf("↑ %d more", above)
	default:
		return fmt.Sprintf("↓ %d more", below)
	}
}

func (m notesModel) renderListOnly(width, height int) string {
	// Narrow layout: just the list, full width, no preview pane.
	return m.renderList(width, height, true)
}

// renderProjectPicker is the project-switcher modal — list of every
// discovered project, with a cursor.
func (m notesModel) renderProjectPicker(width, height int) string {
	lines := []string{
		m.st.Emphasis.Render(tr("Switch project")),
		m.st.Subtitle.Render(tr("Notes context follows your selection.")),
		"",
	}
	maxVisible := height - 8
	if maxVisible < 5 {
		maxVisible = 5
	}
	start := 0
	if m.projCursor > maxVisible-3 {
		start = m.projCursor - (maxVisible - 3)
	}
	end := start + maxVisible
	if end > len(m.projects) {
		end = len(m.projects)
	}
	for i := start; i < end; i++ {
		p := m.projects[i]
		lines = append(lines, components.RenderListRow(m.st, p.Name, i == m.projCursor, minInt(70, width-4)-2))
	}
	if len(m.projects) == 0 {
		if m.projectsLoading {
			lines = append(lines, m.st.Muted.Render(tr("loading projects…")))
		} else {
			lines = append(lines, m.st.Muted.Render(tr("No projects found — create one on the Projects tab.")))
		}
	}
	if end < len(m.projects) {
		lines = append(lines, m.st.Muted.Render(fmt.Sprintf("  … %d more (scroll with j/k)", len(m.projects)-end)))
	}
	lines = append(lines, "", m.st.Muted.Render(tr("↑↓ or j/k: navigate   enter: open   esc: cancel")))
	modal := m.st.PaneFocused.Width(minInt(70, width-4)).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
}
