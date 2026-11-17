package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

func (m notesModel) Update(msg tea.Msg) (notesModel, tea.Cmd) {
	// Async results and spinner ticks first: they carry data, not
	// input, so no modal below may swallow them. The picker and the
	// info panel used to return early for every message, dropping a
	// listing that landed while one was open.
	switch msg := msg.(type) {
	case spinner.TickMsg:
		if !m.loading {
			return m, nil
		}
		var cmd tea.Cmd
		m.loadingSpinner, cmd = m.loadingSpinner.Update(msg)
		return m, cmd
	case notesEntriesLoadedMsg:
		return m.applyEntries(msg)
	case notesPreviewLoadedMsg:
		return m.applyPreview(msg), nil
	case notesSearchResultMsg:
		return m.applySearchResult(msg)
	}

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
				return m, m.refreshPreview()
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
		if msg.X < leftW || !m.previewShown() {
			// List-only layout (phone width): the whole screen is
			// the list, so every wheel event scrolls it.
			rowCount := m.listLen()
			if rowCount == 0 {
				return m, nil
			}
			var pvCmd tea.Cmd
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				if m.cursor > 0 {
					m.cursor--
					pvCmd = m.refreshPreview()
				}
			case tea.MouseButtonWheelDown:
				if m.cursor < rowCount-1 {
					m.cursor++
					pvCmd = m.refreshPreview()
				}
			}
			return m, pvCmd
		}
		var cmd tea.Cmd
		m.preview, cmd = m.preview.Update(msg)
		return m, cmd
	case newNoteSubmitMsg:
		m.newNoteForm = nil
		cmd, created := m.createAndOpenNote(msg.Filename, msg.Title)
		if created != "" {
			m.pendingReveal = created
		}
		return m, cmd
	case newNoteCancelMsg:
		m.newNoteForm = nil
		return m, nil
	case noteInfoOpenMsg:
		// The info overlay stats the file on local disk; for a remote
		// device's note that would show "no such file" or, worse, an
		// unrelated local file at the same path.
		if m.activeIsRemote() {
			return m, func() tea.Msg {
				return toastMsg{
					Text:  tr("remote notes are read-only — preview only"),
					Kind:  toastInfo,
					Until: time.Now().Add(4 * time.Second),
				}
			}
		}
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
		delete(m.entriesCache, m.cacheKey(m.project))
		// The note under the cursor may have just been edited ($EDITOR
		// returned, or `r`). Forget the cached body so the refresh after
		// the reload re-reads it — refreshPreview skips the fetch when
		// the selection is unchanged and a body is already held.
		m.previewRel = ""
		m.previewSrc = ""
		m.loading = true
		return m, tea.Batch(m.loadEntriesCmd(*m.project), m.loadingSpinner.Tick)
	case tea.KeyMsg:
		// Global Notes keys (don't depend on which pane has focus).
		switch msg.String() {
		case "H":
			// Cycle the active device. No-op (with a hint) when only this
			// machine is reachable.
			cmd, switched := m.cycleDevice()
			if !switched {
				return m, func() tea.Msg {
					return toastMsg{
						Text:  tr("no other devices reachable"),
						Kind:  toastInfo,
						Until: time.Now().Add(3 * time.Second),
					}
				}
			}
			return m, cmd
		case "n":
			if m.project == nil {
				return m, func() tea.Msg {
					return toastMsg{
						Text:  tr("select a project first (press p)"),
						Kind:  toastInfo,
						Until: time.Now().Add(3 * time.Second),
					}
				}
			}
			if m.activeIsRemote() {
				return m, func() tea.Msg {
					return toastMsg{
						Text:  fmt.Sprintf(tr("remote notes are read-only — create on %s directly"), hostLabel(m.deviceName, m.hosts)),
						Kind:  toastInfo,
						Until: time.Now().Add(4 * time.Second),
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
				return m, m.refreshPreview()
			}
		case "p", " ":
			// Both `p` and space open the project picker. Space is
			// the easier muscle-memory key (mirrors many file
			// managers), `p` stays for discoverability and HelpBar
			// listing.
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
			if len(m.projects) == 0 {
				// Projects haven't loaded yet (a fresh launch with a
				// slow host in the refresh) or the list was empty:
				// ask for them now. The picker shows it's loading and
				// fills in when the list lands (SetProjects). `p` used
				// to do nothing here, although the empty Notes screen
				// says to press it.
				m.projectsLoading = true
				return m, func() tea.Msg { return notesProjectsWantedMsg{} }
			}
			return m, nil
		case "tab":
			// Toggle which pane receives navigation keys. List focus
			// → preview focus → list focus. While the preview is
			// focused, j/k/arrows scroll the document; while the list
			// is focused, they change the selected file. At phone
			// width there is no preview pane to focus.
			if m.focus == focusList && m.previewShown() {
				m.focus = focusPreview
			} else {
				m.focus = focusList
			}
			return m, nil
		case "right", "l":
			// In the file tree, right/l expands the folder under the
			// cursor (or drills into an already-expanded one); on a
			// file row it falls through to moving focus into the
			// preview pane (the legacy behaviour). During an active
			// search the list is flat, so right just moves to preview.
			// `l` mirrors vim's right-motion key. Intercepted at the
			// global level so the viewport's own right-key handling
			// doesn't swallow it.
			if m.focus == focusList {
				if m.hasActiveSearch() {
					if m.previewShown() {
						m.focus = focusPreview
					}
					return m, nil
				}
				return m, m.handleRight()
			}
		case "left", "h":
			// Left/h from the preview pane moves focus back to the
			// file list (mirrors tab). In the tree it collapses the
			// folder under the cursor, or jumps out to the enclosing
			// parent folder header.
			if m.focus == focusPreview {
				m.focus = focusList
				return m, nil
			}
			if m.focus == focusList && !m.hasActiveSearch() {
				return m, m.handleLeft()
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
					return m, m.refreshPreview()
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
					return m, m.refreshPreview()
				}
				return m, nil
			case keyMatches(msg, m.km.EditInEd), keyMatches(msg, m.km.Enter):
				// enter and e both open the selected file in $EDITOR.
				// Editing only works on local notes — a remote note's
				// file lives on another machine's disk. The preview pane
				// still shows remote content; we just can't open $EDITOR
				// on it.
				if m.activeIsRemote() {
					return m, func() tea.Msg {
						return toastMsg{
							Text:  tr("remote notes are read-only — preview only"),
							Kind:  toastInfo,
							Until: time.Now().Add(4 * time.Second),
						}
					}
				}
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

// applyEntries lands a project listing (notesEntriesLoadedMsg).
func (m notesModel) applyEntries(msg notesEntriesLoadedMsg) (notesModel, tea.Cmd) {
	// Discard stale results — the user may have switched device or
	// project between the Cmd dispatching and the fetch returning.
	if m.project == nil || msg.Path != m.project.Path || msg.Host != projectHost(*m.project) {
		return m, nil
	}
	m.loading = false
	if msg.Err != "" {
		// Remote device unreachable: show the error, don't fall back
		// to local notes (which would misrepresent whose notes these
		// are). Leave the cache untouched so a later retry refetches.
		m.deviceErr = msg.Err
		m.entries = nil
		m.cursor = 0
		return m, m.refreshPreview()
	}
	m.deviceErr = ""
	// A re-list of the project already on screen (the editor closed
	// after `e` / `n`, or `r`) keeps the reader's place: the folders they
	// opened stay open and the cursor stays on the same file or folder.
	// Resetting the folds and keeping the cursor *index* collapsed the
	// tree and left the cursor on an unrelated row (often a folder, so
	// the preview read "No selection").
	reload := m.entries != nil
	prevSel, hadSel := m.selectedRow()
	prevKey := ""
	if hadSel {
		prevKey = m.rowKey(prevSel)
	}
	m.entries = msg.Entries
	m.entriesCache[m.cacheKey(m.project)] = msg.Entries
	if !reload {
		m.applyDefaultFolds()
	}
	switch {
	case m.pendingReveal != "":
		// A note just created with `n`: open its folder and select it.
		m.revealNote(m.pendingReveal)
		m.pendingReveal = ""
	case prevKey != "":
		m.selectRowKey(prevKey)
	}
	m.clampCursor()
	return m, m.refreshPreview()
}
