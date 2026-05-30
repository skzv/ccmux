package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/notes"
	"github.com/skzv/ccmux/internal/project"
)

// Cross-device notes. The Notes screen can browse notes on the local
// machine or on any reachable ccmuxd peer. The active device is tracked
// as a project-host *label* ("local" or a remote host name) so it lives
// in the same namespace as project.Host (see projectHost). Where the
// notes are actually fetched from is driven by the *selected project's*
// host, which the device toggle keeps in sync — so resolving an address
// always goes through the project, never a separate cached device.

// SetHosts pushes the latest host-health list into the Notes screen so
// the device toggle (`H`) knows which peers are reachable. Mirrors the
// Sessions/Projects screens. If the active device drops off the
// selectable set (peer went offline), we fall back to local rather than
// leave the screen pointed at an unreachable device.
func (m *notesModel) SetHosts(h []hostStatus) {
	m.hosts = h
	if m.deviceName == "" {
		m.deviceName = localDeviceLabel
		return
	}
	for _, label := range m.selectableDeviceLabels() {
		if label == m.deviceName {
			return
		}
	}
	m.deviceName = localDeviceLabel
}

// localDeviceLabel is the canonical project-host label for this machine,
// matching what refreshProjectsCmd stamps onto local projects.
const localDeviceLabel = "local"

// deviceLabel returns the project-host label for a host row: "local" for
// this machine, the host Name otherwise. Keeps device identity in the
// same namespace as project.Host so the two can be matched directly.
func deviceLabel(hs hostStatus) string {
	if hs.Local {
		return localDeviceLabel
	}
	return hs.Name
}

// deviceQueryable reports whether a host row backs a daemon we can ask
// for notes. Mobile peers and not-yet-installed peers can't serve the
// notes API; an unreachable peer (OK=false) would just error. The local
// row is always queryable (we read its disk directly).
func deviceQueryable(hs hostStatus) bool {
	if hs.Local {
		return true
	}
	return hs.OK && hs.Address != "" && !hs.NeedsInstall && !hs.Mobile
}

// selectableDeviceLabels returns the labels of every queryable device,
// local first, in host order. Deduped so a peer that's both configured
// and discovered only appears once.
func (m notesModel) selectableDeviceLabels() []string {
	var labels []string
	seen := map[string]bool{}
	// Local always leads.
	for _, hs := range m.hosts {
		if hs.Local && !seen[localDeviceLabel] {
			labels = append(labels, localDeviceLabel)
			seen[localDeviceLabel] = true
		}
	}
	if !seen[localDeviceLabel] {
		// No local row yet (e.g. before first refresh) — still offer it.
		labels = append(labels, localDeviceLabel)
		seen[localDeviceLabel] = true
	}
	for _, hs := range m.hosts {
		if hs.Local || !deviceQueryable(hs) {
			continue
		}
		label := deviceLabel(hs)
		if !seen[label] {
			labels = append(labels, label)
			seen[label] = true
		}
	}
	return labels
}

// remoteAddr resolves a device label to a daemon address for
// RemoteClient, or "" when the label is local (read from disk directly).
// Returns "" with ok=false when a remote label has no reachable address.
func (m notesModel) remoteAddr(label string) (addr string, ok bool) {
	if label == localDeviceLabel {
		return "", true
	}
	for _, hs := range m.hosts {
		if !hs.Local && deviceLabel(hs) == label && hs.Address != "" {
			return hs.Address, true
		}
	}
	return "", false
}

// activeIsRemote reports whether the selected project lives on another
// device. Write-path actions (new note, edit, info) are disabled in that
// case because remote notes are read-only and the file isn't on this
// machine's disk.
func (m notesModel) activeIsRemote() bool {
	return m.project != nil && projectHost(*m.project) != localDeviceLabel
}

// projectsForLabel returns the discovered projects that live on the
// device identified by `label`. Drives the device-scoped project picker.
func (m notesModel) projectsForLabel(label string) []project.Project {
	var out []project.Project
	for _, p := range m.projects {
		if projectHost(p) == label {
			out = append(out, p)
		}
	}
	return out
}

// cacheKey namespaces the per-session entries cache by device so a local
// project and a remote project that happen to share an absolute path
// can't collide.
func (m notesModel) cacheKey(p *project.Project) string {
	if p == nil {
		return ""
	}
	return projectHost(*p) + "\x00" + p.Path
}

// entriesFromDaemon adapts the daemon's NoteEntry wire shape into the
// notes.Entry the list renderer consumes, so remote and local notes
// render through one path. Path is left empty: it's an absolute path on
// the remote host, meaningless locally, and the write-path actions that
// would use it (edit/info) are disabled for remote notes anyway.
func entriesFromDaemon(in []daemon.NoteEntry) []notes.Entry {
	out := make([]notes.Entry, 0, len(in))
	for _, e := range in {
		out = append(out, notes.Entry{
			Rel:      e.Rel,
			Dir:      e.Dir,
			Display:  e.Display,
			Modified: e.Modified,
		})
	}
	return out
}

// searchHitsFromDaemon adapts daemon.SearchHit into notes.SearchHit. As
// with entriesFromDaemon, Path stays empty for remote hits.
func searchHitsFromDaemon(in []daemon.SearchHit) []notes.SearchHit {
	out := make([]notes.SearchHit, 0, len(in))
	for _, h := range in {
		out = append(out, notes.SearchHit{
			Rel:     h.Rel,
			LineNum: h.LineNum,
			Snippet: h.Snippet,
		})
	}
	return out
}

// deviceHeaderLine renders the active-device chip under the project name,
// plus a one-key hint when more than one device is reachable.
func (m notesModel) deviceHeaderLine() string {
	label := m.deviceName
	if label == "" {
		label = localDeviceLabel
	}
	chip := m.st.HostColor(label).Render("● " + label)
	if len(m.selectableDeviceLabels()) > 1 {
		chip += "  " + m.st.Muted.Render("H: switch device")
	}
	return chip
}

// cycleDevice advances the active device to the next selectable one and
// selects that device's first project (clearing the view when it has
// none). Returns nil and leaves state untouched when only one device is
// reachable — the caller surfaces a toast in that case.
func (m *notesModel) cycleDevice() (tea.Cmd, bool) {
	labels := m.selectableDeviceLabels()
	if len(labels) <= 1 {
		return nil, false
	}
	cur := 0
	for i, l := range labels {
		if l == m.deviceName {
			cur = i
			break
		}
	}
	next := labels[(cur+1)%len(labels)]
	return m.switchToDevice(next), true
}

// switchToDevice points the screen at `label`, resets search/cursor, and
// auto-selects the device's first project (or clears everything when the
// device has no projects). Returns the load Cmd for the new selection.
func (m *notesModel) switchToDevice(label string) tea.Cmd {
	m.deviceName = label
	m.deviceErr = ""
	m.searching = false
	m.searchInput.Blur()
	m.searchInput.SetValue("")
	m.searchResults = nil
	m.searchQuery = ""
	m.cursor = 0
	m.focus = focusList

	devProjects := m.projectsForLabel(label)
	if len(devProjects) == 0 {
		m.project = nil
		m.entries = nil
		m.expanded = make(map[string]bool)
		m.loading = false
		m.previewSrc = ""
		m.previewRel = ""
		m.preview.SetContent("")
		return nil
	}
	p := devProjects[0]
	m.project = &p
	m.previewSrc = ""
	m.previewRel = ""
	m.preview.SetContent("")
	if cached, ok := m.entriesCache[m.cacheKey(&p)]; ok {
		m.entries = cached
		m.loading = false
		m.applyDefaultFolds()
		return m.refreshPreview()
	}
	m.entries = nil
	m.expanded = make(map[string]bool)
	m.loading = true
	return tea.Batch(m.loadEntriesCmd(p), m.loadingSpinner.Tick)
}

// notesRequestTimeout bounds remote note fetches so a slow or hung peer
// can't wedge the screen.
const notesRequestTimeout = 5 * time.Second

// remoteNotesCmd fetches the project's note listing from a remote daemon.
func remoteNotesCmd(addr, label, path, name string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), notesRequestTimeout)
		defer cancel()
		des, err := daemon.RemoteClient(addr).Notes(ctx, name)
		if err != nil {
			return notesEntriesLoadedMsg{Host: label, Path: path, Err: err.Error()}
		}
		return notesEntriesLoadedMsg{Host: label, Path: path, Entries: entriesFromDaemon(des)}
	}
}

// remoteNoteContentCmd fetches one note's body from a remote daemon.
func remoteNoteContentCmd(addr, label, path, name, rel string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), notesRequestTimeout)
		defer cancel()
		nc, err := daemon.RemoteClient(addr).NoteContent(ctx, name, rel)
		if err != nil {
			return notesPreviewLoadedMsg{Host: label, Path: path, Rel: rel, Err: err.Error()}
		}
		return notesPreviewLoadedMsg{Host: label, Path: path, Rel: rel, Content: nc.Content}
	}
}
