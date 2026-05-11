package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/notes"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// notesModel is the Notes tab — a per-project docs/ browser with a
// Glamour-rendered preview pane. Two key UX moves:
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
	st       styles.Styles
	km       Keymap
	project  *project.Project
	projects []project.Project
	entries  []notes.Entry
	cursor   int
	focus    notesFocus
	preview  viewport.Model
	rendered string
	editor   string

	// new-note picker
	picking bool

	// project picker
	pickingProject bool
	projCursor     int
}

// notesFocus tracks which pane receives navigation keys.
type notesFocus int

const (
	focusList notesFocus = iota
	focusPreview
)

func newNotes(st styles.Styles, km Keymap) notesModel {
	vp := viewport.New(80, 20)
	return notesModel{
		st:      st,
		km:      km,
		preview: vp,
		editor:  pickEditor(),
	}
}

// SetProject is called by the App when the user changes the focused
// project (via the Projects screen cursor). Triggers a re-listing.
func (m *notesModel) SetProject(p *project.Project) {
	if p == nil {
		m.project = nil
		m.entries = nil
		m.rendered = ""
		return
	}
	if m.project != nil && m.project.Path == p.Path {
		return
	}
	m.project = p
	m.cursor = 0
	m.focus = focusList
	m.loadEntries()
	m.refreshPreview()
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

func (m *notesModel) loadEntries() {
	if m.project == nil {
		m.entries = nil
		return
	}
	vault := notes.Open(m.project.Path)
	entries, err := vault.List()
	if err != nil {
		m.entries = nil
		return
	}
	m.entries = entries
	if m.cursor >= len(entries) {
		m.cursor = max0(len(entries) - 1)
	}
}

func (m *notesModel) refreshPreview() {
	if m.project == nil || len(m.entries) == 0 || m.cursor < 0 || m.cursor >= len(m.entries) {
		m.rendered = ""
		m.preview.SetContent("")
		return
	}
	vault := notes.Open(m.project.Path)
	data, err := vault.Read(m.entries[m.cursor].Rel)
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
	// New-note action picker.
	if m.picking {
		if km, ok := msg.(tea.KeyMsg); ok {
			switch km.String() {
			case "esc":
				m.picking = false
				return m, nil
			case "a":
				m.picking = false
				return m, m.newAgentLogCmd()
			case "s":
				m.picking = false
				return m, m.promptForSpecCmd()
			case "d":
				m.picking = false
				return m, m.promptForADRCmd()
			}
		}
		return m, nil
	}

	// Project picker modal.
	if m.pickingProject {
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
					m.SetProject(&p)
				}
				m.pickingProject = false
			}
		}
		return m, nil
	}

	switch msg := msg.(type) {
	case notesReloadMsg:
		m.loadEntries()
		m.refreshPreview()
		return m, nil
	case tea.KeyMsg:
		// Global Notes keys (don't depend on which pane has focus).
		switch msg.String() {
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
			switch {
			case keyMatches(msg, m.km.Up):
				if m.cursor > 0 {
					m.cursor--
					m.refreshPreview()
				}
				return m, nil
			case keyMatches(msg, m.km.Down):
				if m.cursor < len(m.entries)-1 {
					m.cursor++
					m.refreshPreview()
				}
				return m, nil
			case keyMatches(msg, m.km.NewItem):
				if m.project != nil {
					m.picking = true
				}
				return m, nil
			case keyMatches(msg, m.km.EditInEd):
				if e := m.selected(); e != nil {
					return m, openInEditor(m.editor, e.Path)
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
	if m.picking {
		return m.renderActionPicker(width, height)
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
		m.renderList(leftW, height),
		" ",
		m.renderPreview(rightW, height),
	)
}

func (m notesModel) renderList(width, height int) string {
	focusMark := ""
	if m.focus == focusList {
		focusMark = m.st.Emphasis.Render(" ◀")
	}
	header := m.st.Emphasis.Render(m.project.Name+" / docs") + focusMark
	hint := m.st.Muted.Render("p: switch project   tab: focus preview   n: new   e: edit   j/k: nav")
	lines := []string{header, hint, ""}

	if len(m.entries) == 0 {
		lines = append(lines, m.st.Muted.Render("(empty — press n to create a note)"))
	} else {
		current := notes.SectionOther + 1 // sentinel "no section printed yet"
		for i, e := range m.entries {
			if e.Section != current {
				if i > 0 {
					lines = append(lines, "")
				}
				lines = append(lines, m.st.Subtitle.Render(e.Section.Label()))
				current = e.Section
			}
			row := "  " + e.Display
			if i == m.cursor {
				row = m.st.ListItemSelected.Render(row)
			}
			lines = append(lines, row)
		}
	}
	return m.st.PaneFocused.Width(width - 2).Height(height - 2).Render(strings.Join(lines, "\n"))
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
	// Narrow layout: just the list, full width.
	return m.renderList(width, height)
}

// renderActionPicker is the "new note" sub-modal (a/s/d).
func (m notesModel) renderActionPicker(width, height int) string {
	body := strings.Join([]string{
		m.st.Emphasis.Render("New note in " + m.project.Name),
		"",
		"  " + m.st.Key.Render("a") + "  Agent Log    " + m.st.Muted.Render("docs/03_Agent_Logs/YYYY-MM-DD.md"),
		"  " + m.st.Key.Render("s") + "  Spec         " + m.st.Muted.Render("docs/01_Specs/NN_<slug>.md"),
		"  " + m.st.Key.Render("d") + "  ADR          " + m.st.Muted.Render("docs/02_Architecture/NN_<slug>.md"),
		"",
		m.st.Muted.Render("esc: cancel"),
	}, "\n")
	modal := m.st.PaneFocused.Width(60).Render(body)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
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

// newAgentLogCmd creates today's Agent Log file (or opens the existing
// one) and exec's $EDITOR on it. Returns to TUI on exit.
func (m notesModel) newAgentLogCmd() tea.Cmd {
	if m.project == nil {
		return nil
	}
	editor := m.editor
	projPath := m.project.Path
	return func() tea.Msg {
		v := notes.Open(projPath)
		path, _, err := v.NewAgentLog("")
		if err != nil {
			return toastMsg{Text: "agent log: " + err.Error(), Kind: toastError, Until: nowPlus(5)}
		}
		return openEditorMsg{Editor: editor, Path: path}
	}
}

// promptForSpecCmd/promptForADRCmd: v0.1 punts on an inline title
// prompt by creating a placeholder file named "untitled_<timestamp>"
// and opening it in $EDITOR. The user renames in the editor. A proper
// title prompt would need another modal level which we'll add later.
func (m notesModel) promptForSpecCmd() tea.Cmd {
	if m.project == nil {
		return nil
	}
	editor := m.editor
	projPath := m.project.Path
	return func() tea.Msg {
		v := notes.Open(projPath)
		path, err := v.NewSpec("untitled")
		if err != nil {
			return toastMsg{Text: "new spec: " + err.Error(), Kind: toastError, Until: nowPlus(5)}
		}
		return openEditorMsg{Editor: editor, Path: path}
	}
}

func (m notesModel) promptForADRCmd() tea.Cmd {
	if m.project == nil {
		return nil
	}
	editor := m.editor
	projPath := m.project.Path
	return func() tea.Msg {
		v := notes.Open(projPath)
		path, err := v.NewADR("untitled")
		if err != nil {
			return toastMsg{Text: "new ADR: " + err.Error(), Kind: toastError, Until: nowPlus(5)}
		}
		return openEditorMsg{Editor: editor, Path: path}
	}
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

// stripUnused suppresses "imported and not used" for `strings` when the
// only use is inside method bodies we may be touching.
var _ = strings.TrimSpace
