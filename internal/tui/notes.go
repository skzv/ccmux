package tui

import (
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
// Glamour-rendered preview pane. Project context comes from whichever
// project was last focused on the Projects screen.
type notesModel struct {
	st       styles.Styles
	km       Keymap
	project  *project.Project
	entries  []notes.Entry
	cursor   int
	preview  viewport.Model
	rendered string
	editor   string // $EDITOR (or fallback)

	// new-note picker state
	picking bool
	pickIdx int
}

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
	m.loadEntries()
	m.refreshPreview()
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
	// The picker overlay swallows keys when active.
	if m.picking {
		if km, ok := msg.(tea.KeyMsg); ok {
			switch km.String() {
			case "esc":
				m.picking = false
				return m, nil
			case "a":
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

	switch msg := msg.(type) {
	case notesReloadMsg:
		m.loadEntries()
		m.refreshPreview()
		return m, nil
	case tea.KeyMsg:
		switch {
		case keyMatches(msg, m.km.Up):
			if m.cursor > 0 {
				m.cursor--
				m.refreshPreview()
			}
		case keyMatches(msg, m.km.Down):
			if m.cursor < len(m.entries)-1 {
				m.cursor++
				m.refreshPreview()
			}
		case keyMatches(msg, m.km.NewItem):
			if m.project != nil {
				m.picking = true
			}
		case keyMatches(msg, m.km.EditInEd):
			if e := m.selected(); e != nil {
				return m, openInEditor(m.editor, e.Path)
			}
		}
		// Preview scrolling
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
			m.st.Muted.Render("Pick a project on the Projects tab (press " + m.st.Key.Render("3") + ") first."),
			"",
			"The Notes tab is scoped to one project's docs/ tree at a time.",
		}, "\n")
		return m.st.Pane.Width(width - 2).Height(height - 2).Render(body)
	}
	if m.picking {
		return m.renderPicker(width, height)
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
	header := m.st.Emphasis.Render(m.project.Name + " / docs")
	hint := m.st.Muted.Render("n: new   e: edit   j/k: nav")
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
	m.preview.Width = width - 4
	m.preview.Height = height - 4
	if e := m.selected(); e != nil {
		title := m.st.Emphasis.Render(e.Display)
		path := m.st.Muted.Render(e.Rel)
		header := title + "   " + path
		body := lipgloss.JoinVertical(lipgloss.Left, header, "", m.preview.View())
		return m.st.Pane.Width(width - 2).Height(height - 2).Render(body)
	}
	return m.st.Pane.Width(width - 2).Height(height - 2).Render(m.st.Muted.Render("No selection."))
}

func (m notesModel) renderListOnly(width, height int) string {
	// Narrow layout: just the list, full width.
	return m.renderList(width, height)
}

func (m notesModel) renderPicker(width, height int) string {
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
