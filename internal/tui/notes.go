package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/notes"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tui/components"
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
	previewSrc   string // raw markdown bytes; glamour-rendered into the viewport whenever the source or size changes
	previewRel   string // rel-path of the file backing previewSrc (so we don't re-read the same file)
	editor       string

	// termWidth / termHeight cache the last WindowSizeMsg that
	// reached the App. We need them in refreshPreview to wrap
	// Glamour to the actual right-column width, and in
	// renderPreview to size the viewport persistently. Without
	// this the viewport's Width stays at its New(80, 20) default
	// in the stored model, which both wraps prose incorrectly and
	// breaks tab+j/k scrolling (the viewport has no persistent
	// content to scroll over).
	termWidth  int
	termHeight int

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

	// new-note form. When non-nil the modal owns input; submit emits
	// newNoteSubmitMsg, esc emits newNoteCancelMsg.
	newNoteForm *newNoteFormModel

	// loadingSpinner animates the "Loading notes…" placeholder so a
	// slow walk surfaces as motion rather than a static line. Only
	// rendered while m.loading is true.
	loadingSpinner spinner.Model

	// noteInfo is the `i` overlay's state. When open is true the
	// overlay paints over the notes layout; the App routes `i`/esc
	// to close it.
	noteInfo noteInfoOverlay
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
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(st.Semantic.Accent)
	return notesModel{
		st:             st,
		km:             km,
		preview:        vp,
		editor:         pickEditor(),
		searchInput:    ti,
		entriesCache:   make(map[string][]notes.Entry),
		loadingSpinner: sp,
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
		m.previewSrc = ""
		m.previewRel = ""
		m.preview.SetContent("")
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
	return tea.Batch(m.loadEntriesCmd(p.Path), m.loadingSpinner.Tick)
}

// SetSize records the terminal size so refreshPreview can render
// Glamour at the actual right-column width. The App should call
// this on every tea.WindowSizeMsg. When the column width changes
// and a note is already loaded, the preview is re-rendered so the
// scrollable viewport always holds content sized to the visible
// pane.
func (m *notesModel) SetSize(w, h int) {
	if w == m.termWidth && h == m.termHeight {
		return
	}
	m.termWidth = w
	m.termHeight = h
	pw, ph := m.previewPaneSize()
	m.preview.Width = pw
	m.preview.Height = ph
	if m.previewSrc != "" {
		m.preview.SetContent(m.renderPreviewContent(pw))
	}
}

// previewPaneSize returns (viewportWidth, viewportHeight) for the
// right-side preview given the cached terminal size. Mirrors the
// arithmetic in View / renderPreview: right column = total -
// left/3 - 1; viewport interior = column - 4 (border + padding);
// viewport reserves 6 inner lines for the header + scroll hint.
func (m notesModel) previewPaneSize() (int, int) {
	tw, th := m.termWidth, m.termHeight
	if tw < 20 {
		tw = 20
	}
	if th < 10 {
		th = 10
	}
	leftW := tw / 3
	rightW := tw - leftW - 1
	pw := rightW - 4
	if pw < 10 {
		pw = 10
	}
	ph := th - 6
	if ph < 3 {
		ph = 3
	}
	return pw, ph
}

// projectRoot returns the absolute path of the focused project, or
// "" when no project is set. Used by the note-info overlay to build
// relative-path display strings.
func (m notesModel) projectRoot() string {
	if m.project == nil {
		return ""
	}
	return m.project.Path
}

// createAndOpenNote writes the new file under the project root and
// opens it in $EDITOR. The body is `# {title}\n\n` when a title is
// supplied; otherwise the file is created empty. Errors (collision,
// permission, etc.) surface as toasts; success chains into
// openInEditor, which itself dispatches notesReloadMsg on editor
// close so the new file shows up in the list.
func (m notesModel) createAndOpenNote(filename, title string) tea.Cmd {
	if m.project == nil {
		return func() tea.Msg {
			return toastMsg{Text: "no project selected", Kind: toastError, Until: time.Now().Add(5 * time.Second)}
		}
	}
	full := filepath.Join(m.project.Path, filepath.FromSlash(filename))
	if _, err := os.Stat(full); err == nil {
		return func() tea.Msg {
			return toastMsg{Text: "file already exists: " + filename, Kind: toastError, Until: time.Now().Add(5 * time.Second)}
		}
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		msg := "mkdir failed: " + err.Error()
		return func() tea.Msg {
			return toastMsg{Text: msg, Kind: toastError, Until: time.Now().Add(5 * time.Second)}
		}
	}
	body := ""
	if title != "" {
		body = "# " + title + "\n\n"
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		msg := "write failed: " + err.Error()
		return func() tea.Msg {
			return toastMsg{Text: msg, Kind: toastError, Until: time.Now().Add(5 * time.Second)}
		}
	}
	return openInEditor(m.editor, full)
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

// refreshPreview loads the selected note's markdown bytes into
// previewSrc and renders them into the viewport at the current
// column width. Both the source string and the rendered viewport
// content are stored on the model so a later viewport.Update (scroll
// keypress) has something to operate on — without persistent
// SetContent the focused-pane j/k keys would no-op against an
// empty viewport.
func (m *notesModel) refreshPreview() {
	if m.project == nil {
		m.previewSrc = ""
		m.previewRel = ""
		m.preview.SetContent("")
		m.preview.GotoTop()
		return
	}
	rel := ""
	if m.hasActiveSearch() {
		if m.cursor >= 0 && m.cursor < len(m.searchResults) {
			rel = m.searchResults[m.cursor].Rel
		}
	} else {
		if len(m.entries) > 0 && m.cursor >= 0 && m.cursor < len(m.entries) {
			rel = m.entries[m.cursor].Rel
		}
	}
	if rel == "" {
		m.previewSrc = ""
		m.previewRel = ""
		m.preview.SetContent("")
		return
	}
	pw, ph := m.previewPaneSize()
	m.preview.Width = pw
	m.preview.Height = ph
	// Skip the disk read when the selection didn't move (cursor +
	// project unchanged) and the viewport already holds rendered
	// content sized for the current column.
	if rel == m.previewRel && m.previewSrc != "" {
		return
	}
	vault := notes.Open(m.project.Path)
	data, err := vault.Read(rel)
	if err != nil {
		m.previewSrc = m.st.StatusError.Render(err.Error())
		m.previewRel = rel
		m.preview.SetContent(m.previewSrc)
		return
	}
	m.previewSrc = string(data)
	m.previewRel = rel
	m.preview.SetContent(m.renderPreviewContent(pw))
	m.preview.GotoTop()
}

// renderPreviewContent runs Glamour at `wrap` cells wide and returns
// the rendered output ready to feed into the viewport. Falls back to
// the raw markdown when the renderer fails to build.
func (m notesModel) renderPreviewContent(wrap int) string {
	if m.previewSrc == "" {
		return ""
	}
	if wrap < 10 {
		wrap = 10
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(styles.GlamourStyle(m.st)),
		glamour.WithWordWrap(wrap),
	)
	if err != nil {
		return m.previewSrc
	}
	out, rerr := r.Render(m.previewSrc)
	if rerr != nil {
		return m.previewSrc
	}
	return out
}

func (m notesModel) Update(msg tea.Msg) (notesModel, tea.Cmd) {
	// New-note form takes priority — when open it owns input until
	// Enter (submit) or Esc (cancel) closes it via newNoteSubmitMsg /
	// newNoteCancelMsg.
	if m.newNoteForm != nil {
		if _, ok := msg.(tea.KeyMsg); ok {
			form, cmd := m.newNoteForm.Update(msg)
			m.newNoteForm = &form
			return m, cmd
		}
	}
	// Note-info overlay takes input next — it accepts `i`/`esc` to
	// close and consumes nothing else.
	if m.noteInfo.open {
		if km, ok := msg.(tea.KeyMsg); ok {
			switch km.String() {
			case "i", "esc":
				m.noteInfo = noteInfoOverlay{}
			}
		}
		return m, nil
	}
	// Project picker modal.
	if m.pickingProject {
		var cmd tea.Cmd
		if km, ok := msg.(tea.KeyMsg); ok {
			switch km.String() {
			case "esc":
				m.pickingProject = false
			case "up", "k":
				// Wrap-around so a long project list is
				// reachable from either end.
				if n := len(m.projects); n > 0 {
					if m.projCursor <= 0 {
						m.projCursor = n - 1
					} else {
						m.projCursor--
					}
				}
			case "down", "j":
				if n := len(m.projects); n > 0 {
					if m.projCursor >= n-1 {
						m.projCursor = 0
					} else {
						m.projCursor++
					}
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
	case tea.MouseMsg:
		// Wheel events scroll whichever pane the user is hovering
		// over: x < leftW = file list (move the cursor up/down),
		// otherwise = preview viewport (forward to viewport.Update
		// which handles wheel scrolling natively). Without
		// coordinate routing the wheel would only ever scroll the
		// preview and the list would feel inert.
		if !isWheelMsg(msg) {
			return m, nil
		}
		leftW := m.termWidth / 3
		if msg.X < leftW {
			rowCount := m.listLen()
			if rowCount == 0 {
				return m, nil
			}
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				if m.cursor > 0 {
					m.cursor--
					m.refreshPreview()
				}
			case tea.MouseButtonWheelDown:
				if m.cursor < rowCount-1 {
					m.cursor++
					m.refreshPreview()
				}
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.preview, cmd = m.preview.Update(msg)
		return m, cmd
	case spinner.TickMsg:
		if !m.loading {
			return m, nil
		}
		var cmd tea.Cmd
		m.loadingSpinner, cmd = m.loadingSpinner.Update(msg)
		return m, cmd
	case newNoteSubmitMsg:
		m.newNoteForm = nil
		return m, m.createAndOpenNote(msg.Filename, msg.Title)
	case newNoteCancelMsg:
		m.newNoteForm = nil
		return m, nil
	case noteInfoOpenMsg:
		path := m.selectedPath()
		if path == "" {
			return m, nil
		}
		m.noteInfo = buildNoteInfoOverlay(path, m.projectRoot())
		return m, nil
	case notesReloadMsg:
		if m.project == nil {
			return m, nil
		}
		delete(m.entriesCache, m.project.Path)
		m.loading = true
		return m, tea.Batch(m.loadEntriesCmd(m.project.Path), m.loadingSpinner.Tick)
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
		case "n":
			if m.project == nil {
				return m, func() tea.Msg {
					return toastMsg{
						Text:  "select a project first (press p)",
						Kind:  toastInfo,
						Until: time.Now().Add(3 * time.Second),
					}
				}
			}
			form := newNewNoteForm(m.st, time.Now())
			m.newNoteForm = &form
			return m, textinput.Blink
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
		case "p", " ":
			// Both `p` and space open the project picker. Space is
			// the easier muscle-memory key (mirrors many file
			// managers), `p` stays for discoverability and HelpBar
			// listing.
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
		case "right", "l":
			// Right/l from the file list moves focus into the
			// preview pane (mirrors tab). Already there? No-op.
			// Intercepted at the global level so the viewport's
			// own right-key handling doesn't swallow it. `l` mirrors
			// vim's right-motion key.
			if m.focus == focusList {
				m.focus = focusPreview
				return m, nil
			}
		case "left", "h":
			// Left/h from the preview pane moves focus back to
			// the file list (mirrors tab). Already on the list?
			// No-op so left in the list doesn't steal a future
			// per-screen binding.
			if m.focus == focusPreview {
				m.focus = focusList
				return m, nil
			}
		}

		if m.focus == focusList {
			rowCount := m.listLen()
			switch {
			case keyMatches(msg, m.km.Up):
				// Wrap-around: up at the first row jumps to the
				// last so the user can reach the bottom of a
				// long list without holding down j.
				if rowCount > 0 {
					if m.cursor <= 0 {
						m.cursor = rowCount - 1
					} else {
						m.cursor--
					}
					m.refreshPreview()
				}
				return m, nil
			case keyMatches(msg, m.km.Down):
				// Wrap-around: down at the last row jumps to the
				// first.
				if rowCount > 0 {
					if m.cursor >= rowCount-1 {
						m.cursor = 0
					} else {
						m.cursor++
					}
					m.refreshPreview()
				}
				return m, nil
			case keyMatches(msg, m.km.EditInEd), keyMatches(msg, m.km.Enter):
				// enter and e both open the selected file in $EDITOR.
				// The previous behaviour left `enter` unhandled, which
				// the HelpBar's "enter open" hint advertised as broken.
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

// HelpBarProps returns the screen-specific key hints for Notes.
// Replaces the legacy inline hint row that used to sit under the
// project name in renderList. `n` is now wired (creates a note in
// the project and opens $EDITOR); `i` opens the note-info overlay.
func (m notesModel) HelpBarProps(width int) components.HelpBarProps {
	return components.HelpBarProps{
		Hints: []components.KeyHint{
			{Key: "?", Label: "help", Priority: 10},
			{Key: "q", Label: "quit", Priority: 10},
			{Key: "enter", Label: "open", Priority: 8},
			{Key: "n", Label: "new note", Priority: 8},
			{Key: "e", Label: "edit", Priority: 7},
			{Key: "/", Label: "search", Priority: 6},
			{Key: "i", Label: "info", Priority: 6},
			{Key: "p / space", Label: "switch project", Priority: 5},
			{Key: "tab", Label: "focus preview", Priority: 4},
			{Key: "1-7", Label: "screens", Priority: 2},
		},
		Width: width,
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
	if m.newNoteForm != nil {
		modalW := minInt(80, width-4)
		modal := m.newNoteForm.View(modalW)
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
	}
	if m.noteInfo.open {
		return m.renderNoteInfoOverlay(width, height)
	}
	if m.pickingProject {
		return m.renderProjectPicker(width, height)
	}
	// Notes uses a tighter narrow threshold than the rest of the
	// TUI: at 100+ cols the 1/3 + 2/3 split is still readable
	// (preview gets ~66 cols), so we'd rather show the preview
	// than collapse to list-only. Zoomed README GIFs (~116 cols)
	// in particular need this — the previous isNarrow(120) cutoff
	// hid the preview in every demo.
	if width < 100 {
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
			empty = m.loadingSpinner.View() + m.st.Muted.Render(" scanning project for markdown…")
		case m.hasActiveSearch():
			empty = m.st.Muted.Render("(no matches)")
		default:
			empty = m.st.Muted.Render("(no markdown files yet — press n to create one)")
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
	// Sub-section indent scales with folder depth so docs/01_Specs/
	// sits visually beneath docs/, not flush-left with it. The
	// left pane is only ~width/3 cells wide so we use the tight SM
	// step (1 cell) — the hierarchy reads clearly without eating
	// the visible filename column. Files indent one step further
	// than their containing folder; the project-root group (Dir
	// == "") sits at depth 0.
	step := m.st.Spacing.SM
	// currentDir is the folder the cursor is in — its header gets
	// the "active" treatment (Sapphire dot + bold bright FG) so
	// the user can see at a glance which group they're navigating
	// through. Other headers stay muted.
	currentDir := ""
	if m.cursor >= 0 && m.cursor < len(m.entries) {
		currentDir = m.entries[m.cursor].Dir
	}
	activeHeaderStyle := lipgloss.NewStyle().Foreground(m.st.P.FG).Bold(true)
	dotStyle := lipgloss.NewStyle().Foreground(m.st.P.Sapphire)
	lastDir := "\x00" // sentinel: no real Dir equals this
	for i, e := range m.entries {
		if e.Dir != lastDir {
			if len(rows) > 0 {
				rows = append(rows, "")
			}
			headerIndent := strings.Repeat(" ", folderDepth(e.Dir)*step)
			label := folderHeader(e.Dir)
			var headerLine string
			if e.Dir == currentDir {
				headerLine = headerIndent + dotStyle.Render("● ") + activeHeaderStyle.Render(label)
			} else {
				headerLine = headerIndent + "  " + m.st.Subtitle.Render(label)
			}
			rows = append(rows, headerLine)
			lastDir = e.Dir
		}
		if i == m.cursor {
			cursorRow = len(rows)
		}
		fileIndentN := (folderDepth(e.Dir) + 1) * step
		fileIndent := strings.Repeat(" ", fileIndentN)
		rowBodyW := rowW - fileIndentN
		if rowBodyW < 1 {
			rowBodyW = 1
		}
		rows = append(rows, fileIndent+components.RenderListRow(m.st, e.Display, i == m.cursor, rowBodyW))
	}
	return rows, cursorRow
}

// folderDepth returns the depth (number of slash-separated path
// segments) for a folder Dir. The project root (Dir == "") is depth
// 0; "docs" is depth 1; "docs/01_Specs" is depth 2.
func folderDepth(dir string) int {
	if dir == "" {
		return 0
	}
	return strings.Count(dir, "/") + 1
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
	// Reserve 3 inner lines: header (1) + blank (1) + scroll hint (1).
	// Viewport eats the rest. The viewport itself was sized + content-
	// loaded persistently in refreshPreview / SetSize so scrolling
	// works; here we just lay it into the pane.
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
		// The H1 (when present) sits at the top of the rendered
		// body, so the header above shows only the file location
		// plus the focus mark — no separate title row to duplicate
		// the H1. A muted horizontal rule sits below it as a
		// visual separator between the path and the rendered
		// content (replaces the previous filled-background block).
		header := m.st.Muted.Render(e.Rel) + focusMark
		separator := m.st.Muted.Render(strings.Repeat("─", m.preview.Width))
		// Scroll-position indicator like "  35% ↓"
		pct := int(m.preview.ScrollPercent() * 100)
		scrollHint := m.st.Muted.Render(fmt.Sprintf(
			"tab: focus list   j/k: scroll (currently focused: %s)   %d%%",
			focusLabel(m.focus), pct,
		))
		body := lipgloss.JoinVertical(lipgloss.Left, header, separator, m.preview.View(), "", scrollHint)
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
		lines = append(lines, components.RenderListRow(m.st, p.Name, i == m.projCursor, minInt(70, width-4)-2))
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
// Thin wrapper kept for callers in this file; the actual work lives
// in helpers.openEditorCmd so notes/claudeconfig/app all share one
// implementation.
func openInEditor(editor, path string) tea.Cmd {
	return openEditorCmd(editor, path, notesReloadMsg{})
}

// noteInfoOverlay is the `i`-key info modal. Closed by default; the
// app routes `i` (when no text field has focus) into a noteInfoOpenMsg
// to populate it from disk. Renders the selected note's metadata:
// absolute path, frontmatter (if any), H1 (if any), line/word counts,
// and modified time.
type noteInfoOverlay struct {
	open        bool
	absPath     string
	relPath     string
	h1          string
	frontmatter string
	lineCount   int
	wordCount   int
	modified    time.Time
	readErr     string
}

// buildNoteInfoOverlay reads `absPath` (capped at 1 MiB so an enormous
// markdown file can't hang the overlay) and pulls out the rendered
// fields. The returned overlay has open=true even when the read
// fails — the modal shows the error so the user knows what went
// wrong rather than silently no-op'ing.
func buildNoteInfoOverlay(absPath, projectRoot string) noteInfoOverlay {
	o := noteInfoOverlay{open: true, absPath: absPath}
	if projectRoot != "" {
		if rel, err := filepath.Rel(projectRoot, absPath); err == nil {
			o.relPath = filepath.ToSlash(rel)
		}
	}
	info, statErr := os.Stat(absPath)
	if statErr == nil {
		o.modified = info.ModTime()
	}
	const readCap = 1 << 20
	data, err := os.ReadFile(absPath)
	if err != nil {
		o.readErr = err.Error()
		return o
	}
	if len(data) > readCap {
		data = data[:readCap]
	}
	body := string(data)
	o.lineCount = strings.Count(body, "\n")
	if len(body) > 0 && !strings.HasSuffix(body, "\n") {
		o.lineCount++
	}
	o.wordCount = len(strings.Fields(body))
	o.frontmatter = extractFrontmatter(body)
	o.h1 = cachedH1ForOverlay(absPath, o.modified)
	return o
}

// cachedH1ForOverlay returns the leading H1 text for the file at
// `absPath`, or "" when none is present in the first 4 KiB. Mirrors
// notes.extractH1 so the overlay sees the same heading the row label
// does, including the YAML/TOML frontmatter-skip behaviour.
func cachedH1ForOverlay(absPath string, mod time.Time) string {
	f, err := os.Open(absPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, 4096)
	n, _ := f.Read(buf)
	src := string(buf[:n])
	lines := strings.Split(src, "\n")

	inFrontmatter := false
	frontDelim := ""
	firstLine := true
	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		if firstLine {
			firstLine = false
			if line == "---" {
				inFrontmatter = true
				frontDelim = "---"
				continue
			}
			if line == "+++" {
				inFrontmatter = true
				frontDelim = "+++"
				continue
			}
		}
		if inFrontmatter {
			if line == frontDelim {
				inFrontmatter = false
			}
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "# ") && !strings.HasPrefix(trimmed, "## ") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
		}
		// First non-empty, non-frontmatter line that isn't an H1 → stop looking.
		break
	}
	return ""
}

// extractFrontmatter returns the YAML/TOML frontmatter block (without
// the delimiters) when the file starts with one, otherwise "". The
// scan is bounded to the first 64 lines so a "doc that looks like
// frontmatter forever" can't blow the buffer.
func extractFrontmatter(body string) string {
	lines := strings.Split(body, "\n")
	if len(lines) < 2 {
		return ""
	}
	delim := ""
	switch lines[0] {
	case "---":
		delim = "---"
	case "+++":
		delim = "+++"
	default:
		return ""
	}
	const scanLines = 64
	end := scanLines
	if end > len(lines) {
		end = len(lines)
	}
	for i := 1; i < end; i++ {
		if lines[i] == delim {
			return strings.Join(lines[1:i], "\n")
		}
	}
	return ""
}

// renderNoteInfoOverlay draws the centered note-info modal.
func (m notesModel) renderNoteInfoOverlay(width, height int) string {
	st := m.st
	o := m.noteInfo
	lines := []string{
		st.Emphasis.Render("Note info"),
		st.Subtitle.Render("Press i or esc to close."),
		"",
	}
	if o.readErr != "" {
		lines = append(lines, st.StatusError.Render("⚠ "+o.readErr))
	}
	row := func(k, v string) string {
		return st.Muted.Render(k+": ") + v
	}
	if o.relPath != "" {
		lines = append(lines, row("relative", o.relPath))
	}
	lines = append(lines, row("path", o.absPath))
	if o.h1 != "" {
		lines = append(lines, row("title (H1)", st.Emphasis.Render(o.h1)))
	}
	lines = append(lines,
		row("lines", fmt.Sprintf("%d", o.lineCount)),
		row("words", fmt.Sprintf("%d", o.wordCount)),
	)
	if !o.modified.IsZero() {
		lines = append(lines, row("modified", o.modified.Format("2006-01-02 15:04:05")))
	}
	if o.frontmatter != "" {
		lines = append(lines, "", st.Subtitle.Render("frontmatter"))
		for _, l := range strings.Split(o.frontmatter, "\n") {
			lines = append(lines, "  "+l)
		}
	}
	modalW := minInt(96, width-4)
	body := strings.Join(lines, "\n")
	modal := st.PaneFocused.Width(modalW).Render(body)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
}
