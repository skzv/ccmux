package tui

import (
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
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

	// expanded is the folder-fold state for the file tree: a folder's
	// direct children are rendered only when expanded[Dir] is true.
	// Folders default to collapsed (the zero map), so opening a project
	// with a deep notes tree shows just the top-level folder headers
	// plus any root-level files. `cursor` indexes the *visible* rows
	// (see visibleRows), so a collapsed folder's children are neither
	// rendered nor reachable. Root-level files (Dir == "") are always
	// visible.
	expanded map[string]bool

	// expandByDefault seeds `expanded` with every folder when a project
	// loads, so the tree opens fully expanded. Set from
	// config.Notes.ExpandFolders / `ccmux --expand-notes`. Default false
	// (collapsed).
	expandByDefault bool
	preview         viewport.Model
	previewSrc      string // raw markdown bytes; glamour-rendered into the viewport whenever the source or size changes
	previewRel      string // rel-path of the file backing previewSrc (so we don't re-read the same file)
	editor          string

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

	// project picker. projectsLoading marks a picker opened before the
	// project list arrived (it asked the App for one).
	pickingProject  bool
	projCursor      int
	projectsLoading bool

	// search state. `/` opens a query box; typing populates it; Enter
	// runs Vault.Search and the result rows take over the list. While
	// searching, cursor indexes into searchResults rather than entries.
	// Esc clears search and restores the normal listing.
	searching     bool
	searchInput   textinput.Model
	searchResults []notes.SearchHit
	searchQuery   string
	searchPartial bool // the search timed out; searchResults is what it found by then

	// new-note form. When non-nil the modal owns input; submit emits
	// newNoteSubmitMsg, esc emits newNoteCancelMsg.
	newNoteForm *newNoteFormModel

	// pendingReveal is the vault-relative path of a note just created
	// with `n`. The listing that lands after the editor closes opens its
	// folder and puts the cursor on it.
	pendingReveal string

	// loadingSpinner animates the "Loading notes…" placeholder so a
	// slow walk surfaces as motion rather than a static line. Only
	// rendered while m.loading is true.
	loadingSpinner spinner.Model

	// noteInfo is the `i` overlay's state. When open is true the
	// overlay paints over the notes layout; the App routes `i`/esc
	// to close it.
	noteInfo noteInfoOverlay

	// Cross-device state. hosts is the latest host-health list (pushed
	// from the App via SetHosts); deviceName is the active device's
	// routing key ("local", a configured name, or a discovered endpoint); deviceErr
	// holds the last remote fetch error so the screen can show "device
	// unreachable" rather than silently empty. See notes_device.go.
	hosts      []hostStatus
	deviceName string
	deviceErr  string
}

// capturesInput reports whether this screen has a modal/text-entry
// state that must swallow global single-key handlers. OR'd into
// App.modalCapturingText — extend this when adding a new modal to
// the Notes screen.
func (m notesModel) capturesInput() bool {
	return m.searching || m.newNoteForm != nil || m.noteInfo.open || m.pickingProject
}

// notesFocus tracks which pane receives navigation keys.
type notesFocus int

const (
	focusList notesFocus = iota
	focusPreview
)

// noteRowKind tags a row in the visible file tree as either a folder
// header (navigable + collapsible) or a file (openable + previewable).
type noteRowKind int

const (
	rowFolder noteRowKind = iota
	rowFile
)

// noteRow is one entry in the flattened, currently-visible file tree.
// The cursor indexes a []noteRow (built by visibleRows), so it can
// land on folder headers — that's what makes them togglable. For a
// file row, entryIdx points back into m.entries; for a folder row it's
// -1 and `dir` is the folder's slash-separated path.
type noteRow struct {
	kind     noteRowKind
	dir      string // folder path (rowFolder) or the file's containing Dir (rowFile)
	entryIdx int    // index into m.entries for rowFile; -1 for rowFolder
}

func newNotes(st styles.Styles, km Keymap) notesModel {
	vp := viewport.New(80, 20)
	ti := textinput.New()
	ti.Prompt = "/ "
	ti.Placeholder = tr("search this project's notes…")
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
		deviceName:     localDeviceLabel,
		expanded:       make(map[string]bool),
	}
}

// SetExpandFolders sets whether the folder tree opens fully expanded
// (true) or collapsed (false, the default). Wired from
// config.Notes.ExpandFolders and the `--expand-notes` run flag. Takes
// effect on the next project load.
func (m *notesModel) SetExpandFolders(expand bool) {
	m.expandByDefault = expand
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
		m.expanded = make(map[string]bool)
		m.previewSrc = ""
		m.previewRel = ""
		m.preview.SetContent("")
		m.loading = false
		m.clearSearch()
		return nil
	}
	// Same project: nothing to do — unless its listing never landed
	// (still loading, nothing listed), in which case load it again
	// rather than leave the screen spinning on a lost result.
	if m.project != nil && m.project.Path == p.Path && projectHost(*m.project) == projectHost(*p) &&
		!(m.loading && m.entries == nil) {
		return nil
	}
	m.project = p
	// Search hits belong to the project they were run against. Keeping
	// them across a switch left project A's results on screen while
	// project B was selected — and Enter opened A's file.
	m.clearSearch()
	// Keep the active device in sync with the project the App handed us
	// (e.g. opening a remote project from the Projects screen switches
	// the Notes device to that host). The toggle can still move it.
	m.deviceName = projectHost(*p)
	m.deviceErr = ""
	m.cursor = 0
	m.focus = focusList
	if cached, ok := m.entriesCache[m.cacheKey(p)]; ok {
		m.entries = cached
		m.loading = false
		m.applyDefaultFolds()
		return m.refreshPreview()
	}
	m.entries = nil
	m.expanded = make(map[string]bool)
	m.loading = true
	pvCmd := m.refreshPreview()
	return tea.Batch(m.loadEntriesCmd(*p), m.loadingSpinner.Tick, pvCmd)
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
	// Shrinking below the split-layout width hides the preview; don't
	// leave keyboard focus stranded on a pane that isn't drawn.
	if !m.previewShown() {
		m.focus = focusList
	}
	pw, ph := m.previewPaneSize()
	m.preview.Width = pw
	m.preview.Height = ph
	if m.previewSrc != "" {
		m.preview.SetContent(m.renderPreviewContent(pw))
	}
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

// ProjectsLoadFailed ends a picker's wait for the project list (see
// notesProjectsWantedMsg) when the load failed: it shows the empty
// state instead of "loading" for good.
func (m *notesModel) ProjectsLoadFailed() { m.projectsLoading = false }

// SetProjects pushes the full discovered-projects list to the screen so
// the project picker (`p` key) can offer all of them, not just the one
// selected on the Projects tab.
func (m *notesModel) SetProjects(ps []project.Project) {
	m.projects = ps
	m.projectsLoading = false
	if m.projCursor >= len(ps) {
		m.projCursor = 0
	}
}

// loadEntriesCmd walks the project tree off the UI goroutine and posts
// notesEntriesLoadedMsg. The path is echoed so the Update handler can
// discard stale results when the user has already switched projects.
func (m notesModel) loadEntriesCmd(p project.Project) tea.Cmd {
	label := projectHost(p)
	path := p.Path
	if label == localDeviceLabel {
		return func() tea.Msg {
			entries, err := notes.Open(path).List()
			if err != nil {
				entries = nil
			}
			return notesEntriesLoadedMsg{Host: label, Path: path, Entries: entries}
		}
	}
	// Remote: fetch over the tailnet. An unresolvable address means the
	// device went offline — surface it as an error rather than reading
	// the (non-existent) remote path off the local disk.
	addr, ok := m.remoteAddr(label)
	if !ok {
		host := hostLabel(label, m.hosts)
		return func() tea.Msg {
			return notesEntriesLoadedMsg{Host: label, Path: path, Err: "device " + host + " is unreachable"}
		}
	}
	return remoteNotesCmd(addr, label, path, p.Name)
}
