package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/notes"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// notesModel is the Notes tab — a per-project markdown browser with a
// Glamour-rendered preview pane. It lists every .md file under the
// project (grouped by folder), not just docs/. Two key UX moves:
//
//   - Tab toggles `focus` between the file list and the preview.
//     j/k/arrows navigate WITHIN the focused pane: file rows when the
//     list is focused, document scroll when the preview is focused.
//     This is the only way to actually scroll a long markdown doc;
//     otherwise the same keys would re-select files and reset the
//     preview to top.
//
//   - `p` opens a project picker so you can hop between projects without
//     returning to the Projects tab. The list is the full set of
//     discovered projects, kept in sync via SetProjects from the App.
type notesModel struct {
	st           styles.Styles
	km           Keymap
	project      *project.Project
	projects     []project.Project
	entries      []notes.Entry
	entriesCache map[string][]notes.Entry // per-project, session-long; bypassed by notesReloadMsg
	loading      bool                     // true while a Vault.List walk is in flight
	cursor       int
	focus        notesFocus
	preview      viewport.Model
	rendered     string
	editor       string

	// project picker
	pickingProject bool
	projCursor     int

	// search state. `/` opens a query box; typing populates it; Enter
	// runs Vault.Search and the result rows take over the list. While
	// searching, cursor indexes into searchResults rather than entries.
	// Esc clears search and restores the normal listing.
	searching     bool
	searchInput   textinput.Model
	searchResults []notes.SearchHit
	searchQuery   string
}

// notesFocus tracks which pane receives navigation keys.
type notesFocus int

const (
	focusList notesFocus = iota
	focusPreview
)

func newNotes(st styles.Styles, km Keymap) notesModel {
	vp := viewport.New(80, 20)
	ti := textinput.New()
	ti.Prompt = "/ "
	ti.Placeholder = "search this project's notes…"
	ti.CharLimit = 200
	return notesModel{
		st:           st,
		km:           km,
		preview:      vp,
		editor:       pickEditor(),
		searchInput:  ti,
		entriesCache: make(map[string][]notes.Entry),
	}
}

// SetProject is called by the App when the focused project changes.
// The file listing is loaded asynchronously: SetProject returns a
// tea.Cmd that runs Vault.List off the UI goroutine, and the caller
// dispatches it. Repeat visits to the same project hit a per-session
// cache and return nil (no work). Nil is also returned when there's
// nothing to load.
func (m *notesModel) SetProject(p *project.Project) tea.Cmd {
	if p == nil {
		m.project = nil
		m.entries = nil
		m.rendered = ""
		m.loading = false
		return nil
	}
	if m.project != nil && m.project.Path == p.Path {
		return nil
	}
	m.project = p
	m.cursor = 0
	m.focus = focusList
	if cached, ok := m.entriesCache[p.Path]; ok {
		m.entries = cached
		m.loading = false
		m.refreshPreview()
		return nil
	}
	m.entries = nil
	m.loading = true
	m.refreshPreview()
	return m.loadEntriesCmd(p.Path)
}

// SetProjects pushes the full discovered-projects list to the screen so
// the project picker (`p` key) can offer all of them, not just the one
// selected on the Projects tab.
func (m *notesModel) SetProjects(ps []project.Project) {
	m.projects = ps
	if m.projCursor >= len(ps) {
		m.projCursor = 0
	}
}

// loadEntriesCmd walks the project tree off the UI goroutine and posts
// notesEntriesLoadedMsg. The path is echoed so the Update handler can
// discard stale results when the user has already switched projects.
func (m notesModel) loadEntriesCmd(path string) tea.Cmd {
	return func() tea.Msg {
		vault := notes.Open(path)
		entries, err := vault.List()
		if err != nil {
			entries = nil
		}
		return notesEntriesLoadedMsg{Path: path, Entries: entries}
	}
}

func (m *notesModel) refreshPreview() {
	if m.project == nil {
		m.rendered = ""
		m.preview.SetContent("")
		return
	}
	// Pick whichever cursor target is active.
	rel := ""
	if m.hasActiveSearch() {
		if m.cursor < 0 || m.cursor >= len(m.searchResults) {
			m.rendered = ""
			m.preview.SetContent("")
			return
		}
		rel = m.searchResults[m.cursor].Rel
	} else {
		if len(m.entries) == 0 || m.cursor < 0 || m.cursor >= len(m.entries) {
			m.rendered = ""
			m.preview.SetContent("")
			return
		}
		rel = m.entries[m.cursor].Rel
	}
	vault := notes.Open(m.project.Path)
	data, err := vault.Read(rel)
	if err != nil {
		m.rendered = m.st.StatusError.Render(err.Error())
		m.preview.SetContent(m.rendered)
		return
	}
	// Glamour rendering. Width 0 means use the renderer's default; we
	// pass the viewport width so wrapping is correct.
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(m.preview.Width-4),
	)
	if err == nil {
		out, rerr := r.Render(string(data))
		if rerr == nil {
			m.rendered = out
		} else {
			m.rendered = string(data)
		}
	} else {
		m.rendered = string(data)
	}
	m.preview.SetContent(m.rendered)
	m.preview.GotoTop()
}

func (m notesModel) Update(msg tea.Msg) (notesModel, tea.Cmd) {
	// Project picker modal.
	if m.pickingProject {
		var cmd tea.Cmd
		if km, ok := msg.(tea.KeyMsg); ok {
			switch km.String() {
			case "esc":
				m.pickingProject = false
			case "up", "k":
				if m.projCursor > 0 {
					m.projCursor--
				}
			case "down", "j":
				if m.projCursor < len(m.projects)-1 {
					m.projCursor++
				}
			case "enter":
				if m.projCursor >= 0 && m.projCursor < len(m.projects) {
					p := m.projects[m.projCursor]
					cmd = m.SetProject(&p)
				}
				m.pickingProject = false
			}
		}
		return m, cmd
	}

	// Active search-input mode: every key goes to the textinput
	// except Esc (cancel) and Enter (run query).
	if m.searching && m.searchInput.Focused() {
		if km, ok := msg.(tea.KeyMsg); ok {
			switch km.String() {
			case "esc":
				m.searching = false
				m.searchInput.Blur()
				m.searchInput.SetValue("")
				m.searchResults = nil
				m.searchQuery = ""
				m.cursor = 0
				m.refreshPreview()
				return m, nil
			case "enter":
				query := strings.TrimSpace(m.searchInput.Value())
				m.searchInput.Blur()
				if query == "" {
					m.searching = false
					return m, nil
				}
				return m, m.runSearch(query)
			}
		}
		var cmd tea.Cmd
		m.searchInput, cmd = m.searchInput.Update(msg)
		return m, cmd
	}

	switch msg := msg.(type) {
	case notesReloadMsg:
		if m.project == nil {
			return m, nil
		}
		delete(m.entriesCache, m.project.Path)
		m.loading = true
		return m, m.loadEntriesCmd(m.project.Path)
	case notesEntriesLoadedMsg:
		// Discard stale results — the user may have switched projects
		// between the Cmd dispatching and the walk returning.
		if m.project == nil || msg.Path != m.project.Path {
			return m, nil
		}
		m.entries = msg.Entries
		m.entriesCache[msg.Path] = msg.Entries
		m.loading = false
		if m.cursor >= len(m.entries) {
			m.cursor = max0(len(m.entries) - 1)
		}
		m.refreshPreview()
		return m, nil
	case notesSearchResultMsg:
		m.searchResults = msg.Hits
		m.searchQuery = msg.Query
		m.cursor = 0
		m.refreshPreview()
		return m, nil
	case tea.KeyMsg:
		// Global Notes keys (don't depend on which pane has focus).
		switch msg.String() {
		case "/":
			if m.project == nil {
				return m, nil
			}
			m.searching = true
			m.searchInput.SetValue("")
			m.searchInput.Focus()
			return m, textinput.Blink
		case "esc":
			// Esc when search results are active (but the input isn't
			// focused) clears the results and restores the file list.
			if m.hasActiveSearch() {
				m.searchResults = nil
				m.searchQuery = ""
				m.searching = false
				m.cursor = 0
				m.refreshPreview()
				return m, nil
			}
		case "p":
			if len(m.projects) > 0 {
				m.pickingProject = true
				// Position cursor on the current project if known.
				m.projCursor = 0
				if m.project != nil {
					for i, p := range m.projects {
						if p.Path == m.project.Path {
							m.projCursor = i
							break
						}
					}
				}
				return m, nil
			}
		case "tab":
			// Toggle which pane receives navigation keys. List focus
			// → preview focus → list focus. While the preview is
			// focused, j/k/arrows scroll the document; while the list
			// is focused, they change the selected file.
			if m.focus == focusList {
				m.focus = focusPreview
			} else {
				m.focus = focusList
			}
			return m, nil
		}

		if m.focus == focusList {
			rowCount := m.listLen()
			switch {
			case keyMatches(msg, m.km.Up):
				if m.cursor > 0 {
					m.cursor--
					m.refreshPreview()
				}
				return m, nil
			case keyMatches(msg, m.km.Down):
				if m.cursor < rowCount-1 {
					m.cursor++
					m.refreshPreview()
				}
				return m, nil
			case keyMatches(msg, m.km.EditInEd):
				if path := m.selectedPath(); path != "" {
					return m, openInEditor(m.editor, path)
				}
				return m, nil
			}
			return m, nil
		}

		// Preview is focused → forward to viewport for scroll.
		var cmd tea.Cmd
		m.preview, cmd = m.preview.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m notesModel) selected() *notes.Entry {
	if m.cursor < 0 || m.cursor >= len(m.entries) {
		return nil
	}
	e := m.entries[m.cursor]
	return &e
}

// hasActiveSearch reports whether the search results pane is showing
// (either because the user just submitted a query or because they
// haven't dismissed previous results yet).
func (m notesModel) hasActiveSearch() bool {
	return m.searchQuery != ""
}

// listLen returns the number of rows the user can navigate over —
// search results when a query is active, entries otherwise.
func (m notesModel) listLen() int {
	if m.hasActiveSearch() {
		return len(m.searchResults)
	}
	return len(m.entries)
}

// selectedPath returns the absolute path of the row under the cursor,
// honoring whether we're showing entries or search hits. Returns "" on
// an out-of-bounds cursor.
func (m notesModel) selectedPath() string {
	if m.hasActiveSearch() {
		if m.cursor < 0 || m.cursor >= len(m.searchResults) {
			return ""
		}
		return m.searchResults[m.cursor].Path
	}
	if m.cursor < 0 || m.cursor >= len(m.entries) {
		return ""
	}
	return m.entries[m.cursor].Path
}

// runSearch is the tea.Cmd that fires Vault.Search in the background
// and posts a notesSearchResultMsg with the hits. 3-second hard cap so
// a pathological query can't stall the TUI.
func (m notesModel) runSearch(query string) tea.Cmd {
	if m.project == nil {
		return nil
	}
	root := m.project.Path
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		v := notes.Open(root)
		hits, _ := v.Search(ctx, query, 100)
		return notesSearchResultMsg{Query: query, Hits: hits}
	}
}

func (m notesModel) View(width, height int) string {
	if m.project == nil {
		body := strings.Join([]string{
			m.st.Emphasis.Render("Notes"),
			"",
			m.st.Muted.Render("No project selected."),
			"",
			"Press " + m.st.Key.Render("p") + " here to pick one, or " + m.st.Key.Render("3") + " to go to the Projects tab.",
		}, "\n")
		return m.st.Pane.Width(width - 2).Height(height - 2).Render(body)
	}
	if m.pickingProject {
		return m.renderProjectPicker(width, height)
	}
	if isNarrow(width) {
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
	focusMark := ""
	if m.focus == focusList {
		focusMark = m.st.Emphasis.Render(" ◀")
	}
	header := m.st.Emphasis.Render(m.project.Name) + focusMark
	lines := []string{header}
	if !narrow {
		lines = append(lines, m.st.Muted.Render("p: switch project   /: search   tab: focus preview   e: edit"))
	}

	// Search box / search-results banner.
	if m.searching && m.searchInput.Focused() {
		lines = append(lines, "", m.searchInput.View())
	} else if m.hasActiveSearch() {
		lines = append(lines, "",
			m.st.Emphasis.Render(fmt.Sprintf("search: %q", m.searchQuery)),
			m.st.Muted.Render(fmt.Sprintf("%d hit(s) — esc clears, enter opens", len(m.searchResults))),
		)
	}
	lines = append(lines, "")

	rows, cursorRow := m.noteRows(width)
	if len(rows) == 0 {
		var empty string
		switch {
		case m.loading:
			empty = "Loading notes…"
		case m.hasActiveSearch():
			empty = "(no matches)"
		default:
			empty = "(empty — press n to create a note)"
		}
		lines = append(lines, m.st.Muted.Render(empty))
		return m.st.PaneFocused.Width(width - 2).Height(height - 2).Render(strings.Join(lines, "\n"))
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
	return m.st.PaneFocused.Width(width - 2).Height(height - 2).Render(strings.Join(lines, "\n"))
}

// noteRows builds the scrollable region of the Notes list — the
// folder-grouped file list, or the flat search-hit list when a query
// is active. It returns the rendered rows and the index of the row
// under the cursor (-1 when the region is empty), so renderList can
// window the rows around it.
func (m notesModel) noteRows(width int) (rows []string, cursorRow int) {
	cursorRow = -1
	if m.hasActiveSearch() {
		for i, h := range m.searchResults {
			label := fmt.Sprintf("%s:%d  %s", h.Rel, h.LineNum, truncateSearchSnippet(h.Snippet, width-12))
			if i == m.cursor {
				cursorRow = len(rows)
				label = m.st.ListItemSelected.Render(label)
			}
			rows = append(rows, "  "+label)
		}
		return rows, cursorRow
	}
	lastDir := "\x00" // sentinel: no real Dir equals this
	for i, e := range m.entries {
		if e.Dir != lastDir {
			if len(rows) > 0 {
				rows = append(rows, "")
			}
			rows = append(rows, m.st.Subtitle.Render(folderHeader(e.Dir)))
			lastDir = e.Dir
		}
		row := "  " + e.Display
		if i == m.cursor {
			cursorRow = len(rows)
			row = m.st.ListItemSelected.Render(row)
		}
		rows = append(rows, row)
	}
	return rows, cursorRow
}

// folderHeader renders the group header for a folder of notes. The
// project root (Dir == "") is labelled explicitly so root-level files
// like README.md don't sit under a blank heading.
func folderHeader(dir string) string {
	if dir == "" {
		return "(project root)"
	}
	return dir + "/"
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

// truncateSearchSnippet keeps result lines from blowing the column
// when a match line is enormous. Uses runes so multi-byte chars
// survive cleanly within the budget.
func truncateSearchSnippet(s string, n int) string {
	if n <= 1 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}

func (m notesModel) renderPreview(width, height int) string {
	// Reserve 3 inner lines: title (1) + blank (1) + scroll hint (1).
	// Viewport eats the rest.
	m.preview.Width = width - 4
	m.preview.Height = height - 6
	if m.preview.Height < 3 {
		m.preview.Height = 3
	}
	if e := m.selected(); e != nil {
		focusMark := ""
		if m.focus == focusPreview {
			focusMark = " " + m.st.Emphasis.Render("◀ scrolling")
		}
		title := m.st.Emphasis.Render(e.Display)
		path := m.st.Muted.Render(e.Rel)
		header := title + "   " + path + focusMark
		// Scroll-position indicator like "  35% ↓"
		pct := int(m.preview.ScrollPercent() * 100)
		scrollHint := m.st.Muted.Render(fmt.Sprintf(
			"tab: focus list   j/k: scroll (currently focused: %s)   %d%%",
			focusLabel(m.focus), pct,
		))
		body := lipgloss.JoinVertical(lipgloss.Left, header, "", m.preview.View(), "", scrollHint)
		paneStyle := m.st.Pane
		if m.focus == focusPreview {
			paneStyle = m.st.PaneFocused
		}
		return paneStyle.Width(width - 2).Height(height - 2).Render(body)
	}
	return m.st.Pane.Width(width - 2).Height(height - 2).Render(m.st.Muted.Render("No selection."))
}

func focusLabel(f notesFocus) string {
	if f == focusPreview {
		return "preview"
	}
	return "list"
}

func (m notesModel) renderListOnly(width, height int) string {
	// Narrow layout: just the list, full width, no preview pane.
	return m.renderList(width, height, true)
}

// renderProjectPicker is the project-switcher modal — list of every
// discovered project, with a cursor.
func (m notesModel) renderProjectPicker(width, height int) string {
	lines := []string{
		m.st.Emphasis.Render("Switch project"),
		m.st.Subtitle.Render("Notes context follows your selection."),
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
		row := "  " + p.Name
		if i == m.projCursor {
			row = m.st.ListItemSelected.Render(row)
		}
		lines = append(lines, row)
	}
	if end < len(m.projects) {
		lines = append(lines, m.st.Muted.Render(fmt.Sprintf("  … %d more (scroll with j/k)", len(m.projects)-end)))
	}
	lines = append(lines, "", m.st.Muted.Render("↑↓ or j/k: navigate   enter: open   esc: cancel"))
	modal := m.st.PaneFocused.Width(minInt(70, width-4)).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
}

// openInEditor returns a tea.Cmd that opens `path` in `editor` via
// tea.ExecProcess. The TUI is suspended for the editor's lifetime.
func openInEditor(editor, path string) tea.Cmd {
	c := exec.Command(editor, path)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		if err != nil {
			return toastMsg{Text: "editor: " + err.Error(), Kind: toastError, Until: nowPlus(5)}
		}
		return notesReloadMsg{}
	})
}

func pickEditor() string {
	for _, env := range []string{"VISUAL", "EDITOR"} {
		if v := os.Getenv(env); v != "" {
			return v
		}
	}
	for _, bin := range []string{"nvim", "vim", "nano"} {
		if _, err := exec.LookPath(bin); err == nil {
			return bin
		}
	}
	return "vi"
}
