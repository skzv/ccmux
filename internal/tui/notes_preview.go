package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/notes"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// notesPreviewMinWidth is the terminal width at which View switches
// from the list-only layout to the list + preview split.
const notesPreviewMinWidth = 100

// previewShown reports whether View renders the preview pane at the
// current terminal width. Below notesPreviewMinWidth (phone widths) the
// screen is list-only, so focus must never move to the invisible
// preview: j/k would scroll it while the list looked frozen. A zero
// width means no WindowSizeMsg has arrived yet; treat that as shown
// (the pre-size default layout).
func (m notesModel) previewShown() bool {
	return m.termWidth == 0 || m.termWidth >= notesPreviewMinWidth
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

// refreshPreview loads the selected note's markdown bytes into
// previewSrc and renders them into the viewport at the current
// column width. Both the source string and the rendered viewport
// content are stored on the model so a later viewport.Update (scroll
// keypress) has something to operate on — without persistent
// SetContent the focused-pane j/k keys would no-op against an
// empty viewport.
// refreshPreview returns a tea.Cmd because a remote note body must be
// fetched off the UI goroutine (it arrives as notesPreviewLoadedMsg).
// Local notes are still read synchronously and the returned Cmd is nil.
func (m *notesModel) refreshPreview() tea.Cmd {
	if m.project == nil {
		m.previewSrc = ""
		m.previewRel = ""
		m.preview.SetContent("")
		m.preview.GotoTop()
		return nil
	}
	rel := ""
	if m.hasActiveSearch() {
		if m.cursor >= 0 && m.cursor < len(m.searchResults) {
			rel = m.searchResults[m.cursor].Rel
		}
	} else if e := m.selectedEntry(); e != nil {
		// Only file rows have a preview; a folder header under the
		// cursor clears the pane.
		rel = e.Rel
	}
	if rel == "" {
		m.previewSrc = ""
		m.previewRel = ""
		m.preview.SetContent("")
		return nil
	}
	pw, ph := m.previewPaneSize()
	m.preview.Width = pw
	m.preview.Height = ph
	// Skip the fetch when the selection didn't move (rel unchanged) and
	// the viewport already holds rendered content sized for the column.
	if rel == m.previewRel && m.previewSrc != "" {
		return nil
	}

	label := projectHost(*m.project)
	if label != localDeviceLabel {
		// Remote: show a placeholder and fetch the body asynchronously.
		m.previewRel = rel
		m.previewSrc = ""
		m.preview.SetContent(m.st.Muted.Render(tr("Loading…")))
		m.preview.GotoTop()
		addr, ok := m.remoteAddr(label)
		if !ok {
			m.previewSrc = m.st.StatusError.Render(fmt.Sprintf(tr("device %s is unreachable"), hostLabel(label, m.hosts)))
			m.preview.SetContent(m.previewSrc)
			return nil
		}
		return remoteNoteContentCmd(addr, label, m.project.Path, m.project.Name, rel)
	}

	// Local: read off the UI goroutine too. The remote branch above was
	// made async deliberately; the local read was left synchronous, so a
	// large markdown file or a slow / network-mounted home directory
	// blocked the whole Bubble Tea render loop on every cursor move.
	// Show a placeholder and load asynchronously, reusing the same
	// notesPreviewLoadedMsg path (and its staleness guard) as remote.
	m.previewRel = rel
	m.previewSrc = ""
	m.preview.SetContent(m.st.Muted.Render(tr("Loading…")))
	m.preview.GotoTop()
	return localNoteContentCmd(m.project.Path, rel)
}

// localNoteContentCmd reads a local note body off the UI goroutine and
// delivers it via the same notesPreviewLoadedMsg the remote path uses,
// so the handler's staleness check (cursor moved / project switched
// since dispatch) covers local reads too. Keeps a slow disk read from
// freezing the render loop.
func localNoteContentCmd(path, rel string) tea.Cmd {
	return func() tea.Msg {
		data, err := notes.Open(path).Read(rel)
		if err != nil {
			return notesPreviewLoadedMsg{Host: localDeviceLabel, Path: path, Rel: rel, Err: err.Error()}
		}
		return notesPreviewLoadedMsg{Host: localDeviceLabel, Path: path, Rel: rel, Content: string(data)}
	}
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
	out, rerr := r.Render(markdownForPreview(m.previewSrc))
	if rerr != nil {
		return m.previewSrc
	}
	return out
}

// SetStyles updates the Notes screen's styles and re-renders the active
// markdown preview with the new Glamour palette.
func (m *notesModel) SetStyles(st styles.Styles) {
	m.st = st
	if m.previewSrc != "" {
		pw, _ := m.previewPaneSize()
		m.preview.SetContent(m.renderPreviewContent(pw))
	}
}

// applyPreview lands a note body (notesPreviewLoadedMsg).
func (m notesModel) applyPreview(msg notesPreviewLoadedMsg) notesModel {
	// Drop a stale fetch: the cursor (and thus previewRel) may have
	// moved, or the user switched project, since we dispatched.
	if m.project == nil || msg.Path != m.project.Path || msg.Rel != m.previewRel {
		return m
	}
	if msg.Err != "" {
		m.previewSrc = m.st.StatusError.Render(msg.Err)
		m.preview.SetContent(m.previewSrc)
		m.preview.GotoTop()
		return m
	}
	m.previewSrc = msg.Content
	pw, _ := m.previewPaneSize()
	m.preview.SetContent(m.renderPreviewContent(pw))
	m.preview.GotoTop()
	return m
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
			focusMark = " " + m.st.Emphasis.Render(tr("◀ scrolling"))
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
		// The hint names what tab and j/k do right now: it used to say
		// "tab: focus list" even while the list had focus.
		keys := tr("tab: focus preview · j/k: pick a note")
		if m.focus == focusPreview {
			keys = tr("tab: focus list · j/k: scroll")
		}
		scrollHint := m.st.Muted.Render(fmt.Sprintf("%s   %d%%", keys, pct))
		body := lipgloss.JoinVertical(lipgloss.Left, header, separator, m.preview.View(), "", scrollHint)
		paneStyle := m.st.Pane
		if m.focus == focusPreview {
			paneStyle = m.st.PaneFocused
		}
		return paneStyle.Width(width - 2).Height(height - 2).Render(body)
	}
	return m.st.Pane.Width(width - 2).Height(height - 2).Render(m.st.Muted.Render(tr("No selection.")))
}
