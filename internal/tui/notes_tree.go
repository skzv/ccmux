package tui

import (
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/notes"
)

// rowKey identifies a tree row across re-lists: entry indices shift when
// files are added or removed, paths don't.
func (m notesModel) rowKey(r noteRow) string {
	if r.kind == rowFolder {
		return "d:" + r.dir
	}
	if r.entryIdx >= 0 && r.entryIdx < len(m.entries) {
		return "f:" + m.entries[r.entryIdx].Rel
	}
	return ""
}

// selectRowKey moves the cursor to the visible row with the given
// rowKey; a no-op when it's gone (the file was deleted meanwhile), which
// leaves the clamped cursor where it was.
func (m *notesModel) selectRowKey(key string) {
	for i, r := range m.visibleRows() {
		if m.rowKey(r) == key {
			m.cursor = i
			return
		}
	}
}

// revealNote expands every folder above the note at rel and selects it.
func (m *notesModel) revealNote(rel string) {
	if m.hasActiveSearch() {
		return
	}
	for d := dirOfRel(rel); d != ""; d = parentDir(d) {
		m.expanded[d] = true
	}
	m.selectRowKey("f:" + rel)
}

// dirOfRel is the folder part of a slash-separated vault-relative path
// ("" for a root-level note), matching notes.Entry.Dir.
func dirOfRel(rel string) string {
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		return rel[:i]
	}
	return ""
}

// selected returns the entry backing the preview pane: the file under
// the cursor in tree mode (nil when the cursor is on a folder header),
// or a synthetic entry carrying the search hit's path/rel in search
// mode. renderPreview only reads Path/Rel off it.
func (m notesModel) selected() *notes.Entry {
	if m.hasActiveSearch() {
		if m.cursor >= 0 && m.cursor < len(m.searchResults) {
			h := m.searchResults[m.cursor]
			return &notes.Entry{Path: h.Path, Rel: h.Rel}
		}
		return nil
	}
	return m.selectedEntry()
}

// listLen returns the number of rows the user can navigate over —
// search results when a query is active, otherwise the currently
// visible tree rows (collapsed folders' children excluded).
func (m notesModel) listLen() int {
	if m.hasActiveSearch() {
		return len(m.searchResults)
	}
	return len(m.visibleRows())
}

// selectedPath returns the absolute path of the row under the cursor,
// honoring whether we're showing search hits or the file tree. Returns
// "" when the cursor is out of bounds or sits on a folder header.
func (m notesModel) selectedPath() string {
	if m.hasActiveSearch() {
		if m.cursor < 0 || m.cursor >= len(m.searchResults) {
			return ""
		}
		return m.searchResults[m.cursor].Path
	}
	if e := m.selectedEntry(); e != nil {
		return e.Path
	}
	return ""
}

// selectedRow returns the visible tree row under the cursor. It reports
// false in search mode (the flat hit list isn't a tree) and on an
// out-of-bounds cursor.
func (m notesModel) selectedRow() (noteRow, bool) {
	if m.hasActiveSearch() {
		return noteRow{}, false
	}
	vis := m.visibleRows()
	if m.cursor < 0 || m.cursor >= len(vis) {
		return noteRow{}, false
	}
	return vis[m.cursor], true
}

// selectedEntry returns the file entry under the cursor, or nil when
// the cursor is on a folder header (or in search mode / out of bounds).
func (m notesModel) selectedEntry() *notes.Entry {
	r, ok := m.selectedRow()
	if !ok || r.kind != rowFile || r.entryIdx < 0 || r.entryIdx >= len(m.entries) {
		return nil
	}
	e := m.entries[r.entryIdx]
	return &e
}

// clampCursor keeps the cursor within the current navigable-row bounds.
// Called after any change that can shrink the visible list (load, fold
// collapse, search exit) so the cursor never points past the end.
func (m *notesModel) clampCursor() {
	n := m.listLen()
	if n == 0 {
		m.cursor = 0
		return
	}
	if m.cursor >= n {
		m.cursor = n - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// applyDefaultFolds (re)initializes the fold state for the loaded
// entries: everything collapsed, unless expandByDefault is set, in
// which case every folder (including synthesized ancestors) is
// expanded.
func (m *notesModel) applyDefaultFolds() {
	m.expanded = make(map[string]bool)
	if !m.expandByDefault {
		return
	}
	for _, d := range m.allFolderDirs() {
		m.expanded[d] = true
	}
}

// allFolderDirs returns every folder path implied by the entries,
// including intermediate ancestors that hold no direct files (e.g.
// "docs" when only "docs/01_Specs/x.md" exists).
func (m notesModel) allFolderDirs() []string {
	set := map[string]bool{}
	for _, e := range m.entries {
		for d := e.Dir; d != ""; d = parentDir(d) {
			set[d] = true
		}
	}
	out := make([]string, 0, len(set))
	for d := range set {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// visibleRows flattens the entry list into the rows currently on
// screen: root-level files first (always visible), then a depth-first
// walk of the folder tree that descends into a folder only when it's
// expanded. The cursor indexes the returned slice.
func (m notesModel) visibleRows() []noteRow {
	filesByDir := map[string][]int{}
	folderSet := map[string]bool{}
	for i, e := range m.entries {
		filesByDir[e.Dir] = append(filesByDir[e.Dir], i)
		for d := e.Dir; d != ""; d = parentDir(d) {
			folderSet[d] = true
		}
	}
	childFolders := map[string][]string{}
	for d := range folderSet {
		p := parentDir(d)
		childFolders[p] = append(childFolders[p], d)
	}
	for p := range childFolders {
		sort.Strings(childFolders[p])
	}

	var rows []noteRow
	var walk func(dir string)
	walk = func(dir string) {
		// Files directly in this folder come before its sub-folders,
		// matching the entry sort (Dir-then-Rel) and the old grouping.
		for _, idx := range filesByDir[dir] {
			rows = append(rows, noteRow{kind: rowFile, dir: dir, entryIdx: idx})
		}
		for _, child := range childFolders[dir] {
			rows = append(rows, noteRow{kind: rowFolder, dir: child, entryIdx: -1})
			if m.expanded[child] {
				walk(child)
			}
		}
	}
	walk("") // root is always expanded; its files are always visible
	return rows
}

// handleRight implements right/l in the file tree: expand a collapsed
// folder, drill into an already-expanded one, or (on a file row) move
// focus to the preview pane.
func (m *notesModel) handleRight() tea.Cmd {
	r, ok := m.selectedRow()
	if !ok {
		if m.previewShown() {
			m.focus = focusPreview
		}
		return nil
	}
	if r.kind == rowFolder {
		if !m.expanded[r.dir] {
			m.expanded[r.dir] = true
			return nil
		}
		// Already expanded → step onto its first child (the row
		// immediately after the header, since walk emits children
		// right after an expanded folder).
		if m.cursor+1 < m.listLen() {
			m.cursor++
			return m.refreshPreview()
		}
		return nil
	}
	if m.previewShown() {
		m.focus = focusPreview
	}
	return nil
}

// handleLeft implements left/h in the file tree: collapse the expanded
// folder under the cursor, or jump out to the enclosing parent folder
// header (from a file row or a collapsed folder). A no-op at the top
// level with nothing to collapse.
func (m *notesModel) handleLeft() tea.Cmd {
	r, ok := m.selectedRow()
	if !ok {
		return nil
	}
	if r.kind == rowFolder && m.expanded[r.dir] {
		m.collapseFolder(r.dir)
		return m.refreshPreview()
	}
	// File row → its containing folder; collapsed folder → its parent.
	targetDir := r.dir
	if r.kind == rowFolder {
		targetDir = parentDir(r.dir)
	}
	if targetDir == "" {
		return nil
	}
	vis := m.visibleRows()
	for i, row := range vis {
		if row.kind == rowFolder && row.dir == targetDir {
			m.cursor = i
			return m.refreshPreview()
		}
	}
	return nil
}

// collapseFolder hides a folder's children. If the cursor was on one of
// those (now-hidden) descendants, it moves up to the folder's header so
// the selection never lands on an invisible row; otherwise it just
// re-clamps.
func (m *notesModel) collapseFolder(dir string) {
	r, ok := m.selectedRow()
	moveToHeader := ok && descendantOf(r, dir)
	m.expanded[dir] = false
	if moveToHeader {
		vis := m.visibleRows()
		for i, row := range vis {
			if row.kind == rowFolder && row.dir == dir {
				m.cursor = i
				break
			}
		}
		return
	}
	m.clampCursor()
}

// descendantOf reports whether row r lives inside folder dir's subtree
// (a nested folder/file, or a file directly in dir). The folder dir's
// own header row is not its own descendant.
func descendantOf(r noteRow, dir string) bool {
	if dir == "" {
		return false
	}
	if strings.HasPrefix(r.dir, dir+"/") {
		return true
	}
	return r.kind == rowFile && r.dir == dir
}

// parentDir returns the parent folder of a slash-separated folder path,
// or "" (the root) for a top-level folder.
func parentDir(dir string) string {
	if i := strings.LastIndex(dir, "/"); i >= 0 {
		return dir[:i]
	}
	return ""
}
