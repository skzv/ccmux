package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/scaffold"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// projectsModel is the Projects screen. Three states:
//   - listing: tree of projects with detail pane
//   - form != nil: modal for creating a new project
//   - menu != nil: modal listing the project's sessions + conversations
type projectsModel struct {
	st       styles.Styles
	km       Keymap
	projects []project.Project
	cursor   int
	form     *newProjectFormModel
	menu     *projectMenuModel
	adopt    *adoptProjectModel

	// projectsRoot is the configured root (cfg.Projects.Root, defaulted).
	// Cached here because the "A" key needs it to scan for orphans
	// without reaching back into App. Pushed from App on every projects
	// load — see App.refreshProjectsCmd.
	projectsRoot string

	// hosts is the live reachable-peer list, fed in from App on every
	// sessionsLoadedMsg. Snapshot into the form at "n"-press time so
	// the picker shows what the user was looking at.
	hosts []hostStatus

	// defaultAgent — resolved cfg.Agents.Default, pushed by App on
	// config load/reload. Selects the new-project form's agent picker
	// at open time. Empty falls back to the first installed agent.
	defaultAgent string

	// Filter state. When filterActive is true, keystrokes feed the
	// textinput and the list view shows only projects whose name
	// matches the filter (case-insensitive substring). Cursor is
	// always an index into the *visible* (filtered) list, so
	// Selected() reflects what the user sees.
	filter       textinput.Model
	filterActive bool
}

func newProjects(st styles.Styles, km Keymap) projectsModel {
	ti := textinput.New()
	ti.Placeholder = "type to filter…"
	ti.Prompt = "/ "
	ti.CharLimit = 64
	ti.Width = 40
	return projectsModel{st: st, km: km, filter: ti}
}

func (m *projectsModel) SetProjects(p []project.Project) {
	m.projects = p
	m.clampCursor()
}

// SetProjectsRoot caches the configured projects root so the `A`
// (adopt) key can scan it for orphan directories without reaching
// back into App. Pushed from App on every projects refresh.
func (m *projectsModel) SetProjectsRoot(root string) {
	m.projectsRoot = root
}

// SetDefaultAgent is the App-side hook that pushes cfg.Agents.Default
// into the projects model so the next "new project" form opens with
// the user's preferred agent pre-selected. Pushed on startup and on
// configReloadMsg (after the user edits Settings or config.toml).
func (m *projectsModel) SetDefaultAgent(a string) {
	m.defaultAgent = a
}

// visibleProjects returns the slice the user actually sees: the full
// list when no filter is engaged, or the case-insensitive substring
// match against project name + path when the filter has text.
// Centralized so the renderer and Selected() can't drift.
func (m projectsModel) visibleProjects() []project.Project {
	q := strings.TrimSpace(strings.ToLower(m.filter.Value()))
	if q == "" {
		return m.projects
	}
	out := make([]project.Project, 0, len(m.projects))
	for _, p := range m.projects {
		if matchesProjectFilter(p, q) {
			out = append(out, p)
		}
	}
	return out
}

// matchesProjectFilter is the predicate behind the "/" filter:
// case-insensitive substring match on the project name. Name-only
// (not path) because every project shares a parent directory, so
// path matching makes "/s" trigger on a name like "ccmux" via the
// "/Projects/" segment of the path — not what the user typed.
// Pulled out so the test can hit it directly without standing up the
// full TUI model.
func matchesProjectFilter(p project.Project, q string) bool {
	if q == "" {
		return true
	}
	return strings.Contains(strings.ToLower(p.Name), q)
}

// Selected returns the project under the cursor in the *filtered*
// view, or nil when nothing is visible. Callers must use this rather
// than indexing the unfiltered slice directly — otherwise enter on a
// filtered list attaches to the wrong project.
func (m projectsModel) Selected() *project.Project {
	vis := m.visibleProjects()
	if m.cursor < 0 || m.cursor >= len(vis) {
		return nil
	}
	p := vis[m.cursor]
	return &p
}

func (m *projectsModel) clampCursor() {
	n := len(m.visibleProjects())
	if m.cursor >= n {
		m.cursor = max0(n - 1)
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// FilterActive reports whether the filter input has focus. App uses
// this to route keystrokes to the textinput (otherwise typing the
// session-switch keys 1-7 would jump screens mid-filter).
func (m projectsModel) FilterActive() bool { return m.filterActive }

// enterFilter focuses the textinput. Idempotent.
func (m *projectsModel) enterFilter() {
	m.filterActive = true
	m.filter.Focus()
}

// exitFilter drops focus and clears the buffer so the next "/" starts
// from an empty prompt. Cursor is clamped back into the (now full)
// list so we don't render with cursor==42 against a 5-row pane.
func (m *projectsModel) exitFilter() {
	m.filterActive = false
	m.filter.Blur()
	m.filter.SetValue("")
	m.clampCursor()
}

// commitFilter blurs the textinput but keeps the filter text so the
// list stays filtered after Enter. The user can still see what they
// were searching for; pressing "/" again re-focuses, esc clears.
func (m *projectsModel) commitFilter() {
	m.filterActive = false
	m.filter.Blur()
}

// SetHosts is called from App so the "n" form can populate its device
// picker with reachable peers at form-open time.
func (m *projectsModel) SetHosts(h []hostStatus) {
	m.hosts = h
}

func (m projectsModel) Update(msg tea.Msg) (projectsModel, tea.Cmd) {
	// Adopt-orphan modal: lives alongside the menu/form modals. App
	// intercepts the pick/cancel messages before they reach here so it
	// can run project.Adopt and refresh; we forward unrecognized
	// messages to the modal so navigation keys still work.
	if m.adopt != nil {
		switch msg.(type) {
		case adoptProjectPickMsg, adoptProjectCancelMsg:
			return m, func() tea.Msg { return msg }
		}
		am, cmd := m.adopt.Update(msg)
		m.adopt = &am
		return m, cmd
	}

	// adoptProjectOpenedMsg arrives async from the disk-scan command
	// fired by the `A` key. Install the modal once orphans are in
	// hand. An error means the scan failed (missing root, perms) —
	// surface a toast instead of opening an empty modal.
	if op, ok := msg.(adoptProjectOpenedMsg); ok {
		if op.Err != nil {
			return m, func() tea.Msg {
				return toastMsg{
					Text:  "scan for orphans: " + op.Err.Error(),
					Kind:  toastError,
					Until: time.Now().Add(5 * time.Second),
				}
			}
		}
		am := newAdoptProject(m.st, op.Root, op.Orphans)
		m.adopt = &am
		return m, nil
	}

	// Menu modal: lists the project's sessions + conversations. App
	// intercepts the pick/cancel messages before they reach here, so we
	// only forward unrecognized messages to the menu's own Update.
	if m.menu != nil {
		switch msg.(type) {
		case projectMenuPickMsg, projectMenuCancelMsg:
			// App handles these at the top level; forward them up.
			return m, func() tea.Msg { return msg }
		}
		mm, cmd := m.menu.Update(msg)
		m.menu = &mm
		return m, cmd
	}

	// Modal mode: route everything to the form.
	if m.form != nil {
		switch msg := msg.(type) {
		case newProjectCancelMsg:
			m.form = nil
			return m, nil
		case newProjectSubmitMsg:
			// Drop the form, kick off create+session start as a tea.Cmd.
			m.form = nil
			return m, createProjectCmd(msg)
		}
		f, cmd := m.form.Update(msg)
		m.form = &f
		return m, cmd
	}

	if km, ok := msg.(tea.KeyMsg); ok {
		// Filter mode: arrow keys (only — not vim j/k, which are
		// part of the alphabet the user is typing) move the cursor.
		// Esc clears the filter; App handles Enter (which calls
		// Selected() + attach), so this branch never sees Enter.
		// Every other key feeds the textinput.
		if m.filterActive {
			switch km.Type {
			case tea.KeyUp:
				if m.cursor > 0 {
					m.cursor--
				}
				return m, nil
			case tea.KeyDown:
				if m.cursor < len(m.visibleProjects())-1 {
					m.cursor++
				}
				return m, nil
			}
			var cmd tea.Cmd
			m.filter, cmd = m.filter.Update(msg)
			m.clampCursor()
			return m, cmd
		}

		switch {
		case km.String() == "/":
			m.enterFilter()
			return m, textinput.Blink
		case km.String() == "c":
			// Drill-down: open the Conversations screen with this
			// project's path pre-applied as a filter. Lets the user
			// pivot from "what am I working on?" to "what have I
			// already done on it?" without losing context. Emitted as
			// a message so App owns the screen-switch — projects.go
			// has no business poking at App state directly.
			if sel := m.Selected(); sel != nil {
				return m, func() tea.Msg {
					return openConversationsForProjectMsg{Project: sel.Path}
				}
			}
		case keyMatches(km, m.km.NewItem):
			f := newNewProjectForm(m.st, m.hosts, m.defaultAgent)
			m.form = &f
			return m, tea.Batch(textInputBlink())
		case km.String() == "A":
			// Adopt: scan the projects root for orphan directories
			// (no .git/.ccmux/CLAUDE.md) and open the picker so the
			// user can register one. Disk scan is one-level-deep and
			// fast, but we hand it to a tea.Cmd anyway so a slow disk
			// or networked home dir can't freeze the UI.
			root := m.projectsRoot
			return m, scanOrphansCmd(root)
		case km.String() == "a":
			// Switch the selected project's agent. Cycles through
			// agent.All() in canonical order. Local-host projects
			// only; remote-project switching would require a daemon
			// endpoint we don't ship today (tracked under Phase 4).
			if sel := m.Selected(); sel != nil {
				if projectHost(*sel) == "local" {
					return m, switchAgentCmd(*sel)
				}
				return m, func() tea.Msg {
					return toastMsg{
						Text:  "agent switch for remote projects not yet supported",
						Kind:  toastWarning,
						Until: time.Now().Add(4 * time.Second),
					}
				}
			}
		case keyMatches(km, m.km.Up):
			if m.cursor > 0 {
				m.cursor--
			}
		case keyMatches(km, m.km.Down):
			if m.cursor < len(m.visibleProjects())-1 {
				m.cursor++
			}
		}
	}

	// React to a successful agent switch by updating the in-memory
	// project list so the detail pane reflects the change before the
	// next poll tick lands.
	if sw, ok := msg.(projectAgentSwitchedMsg); ok {
		for i, p := range m.projects {
			if p.Path == sw.Path {
				m.projects[i].Agent = sw.Agent
				break
			}
		}
	}
	return m, nil
}

// nextAgent returns the next agent in canonical order after `cur`.
// Used by the projects detail-pane switcher: pressing `a` cycles
// claude → codex → antigravity → claude.
func nextAgent(cur agent.ID) agent.ID {
	all := agent.All()
	for i, a := range all {
		if a.ID() == cur {
			return all[(i+1)%len(all)].ID()
		}
	}
	return all[0].ID()
}

// scanOrphansCmd runs project.DiscoverOrphans off the UI goroutine and
// returns an adoptProjectOpenedMsg. Errors flow through the message so
// the screen can toast them instead of silently doing nothing. An empty
// root falls back to ~/Projects to mirror the rest of the codebase
// (config.Load + scaffold both resolve the same way).
func scanOrphansCmd(root string) tea.Cmd {
	return func() tea.Msg {
		if root == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return adoptProjectOpenedMsg{Err: err}
			}
			root = filepath.Join(home, "Projects")
		}
		orphans, err := project.DiscoverOrphans(root)
		return adoptProjectOpenedMsg{Root: root, Orphans: orphans, Err: err}
	}
}

// switchAgentCmd is the projects-detail-pane "a" action. Writes the
// sidecar, emits a toast, and signals the in-memory list to update.
// Local-only — remote agent switching would need a daemon endpoint
// (POST /v1/projects/<name>/agent) that we haven't built yet.
func switchAgentCmd(p project.Project) tea.Cmd {
	return func() tea.Msg {
		next := nextAgent(p.Agent)
		if err := project.SetAgent(p.Path, next); err != nil {
			return toastMsg{
				Text:  "agent switch: " + err.Error(),
				Kind:  toastError,
				Until: time.Now().Add(5 * time.Second),
			}
		}
		return tea.Batch(
			func() tea.Msg { return projectAgentSwitchedMsg{Path: p.Path, Agent: next} },
			func() tea.Msg {
				return toastMsg{
					Text: p.Name + ": agent → " + string(next) +
						" (next session uses this agent)",
					Kind:  toastSuccess,
					Until: time.Now().Add(5 * time.Second),
				}
			},
		)()
	}
}

func (m projectsModel) View(width, height int) string {
	if m.adopt != nil {
		modalW := minInt(80, width-4)
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, m.adopt.View(modalW))
	}
	if m.menu != nil {
		menuW := minInt(80, width-4)
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, m.menu.View(menuW))
	}
	if m.form != nil {
		// Show form centered with project list dimmed behind it.
		formW := minInt(80, width-4)
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, m.form.View(formW))
	}
	if isNarrow(width) {
		return m.renderList(width, height, true)
	}
	leftW := width * 2 / 3
	rightW := width - leftW - 1
	return lipgloss.JoinHorizontal(lipgloss.Top,
		m.renderList(leftW, height, false),
		" ",
		m.renderDetail(rightW, height),
	)
}

// renderList draws the project list. `narrow` is the terminal's
// narrow state (not derived from `width`, which in wide mode is only
// the left sub-pane): on narrow the T2 key-hint is dropped.
func (m projectsModel) renderList(width, height int, narrow bool) string {
	header := m.st.Emphasis.Render("Projects")
	if !narrow {
		header += "  " + m.st.Muted.Render("(/: filter   n: new   enter: attach)")
	}
	if len(m.projects) == 0 {
		body := lipgloss.JoinVertical(lipgloss.Left,
			header,
			"",
			m.st.Muted.Render("No projects found under your projects root."),
			"",
			"Press "+m.st.Key.Render("n")+" to scaffold a new one.",
		)
		return m.st.Pane.Width(width - 2).Height(height - 2).Render(body)
	}

	vis := m.visibleProjects()
	rows := []string{header}
	if m.filterActive || m.filter.Value() != "" {
		// Filter prompt line lives beneath the header. Show the live
		// match count so the user can tell whether they need to keep
		// typing or commit.
		rows = append(rows,
			m.filter.View()+"  "+m.st.Muted.Render(fmt.Sprintf("(%d/%d)", len(vis), len(m.projects))))
	}
	rows = append(rows, "")

	if len(vis) == 0 {
		rows = append(rows,
			m.st.Muted.Render("No projects match "+m.filter.Value()+"."),
			"",
			m.st.Muted.Render("Press esc to clear the filter."),
		)
		return m.st.PaneFocused.Width(width - 2).Height(height - 2).Render(strings.Join(rows, "\n"))
	}

	// Window the project rows around the cursor so a long list never
	// scrolls the cursor out of the visible pane. Pane height passed to
	// lipgloss is `height-2`; subtract another 2 for the rounded border
	// rows, then the lines we've already pushed (header, optional filter
	// line, blank), and reserve 1 row of headroom for the "on <host>"
	// subheader that we re-emit at the top of the window.
	budget := (height - 4) - len(rows) - 1
	start, end := windowAroundCursor(m.cursor, len(vis), budget)

	currentHost := ""
	for i := start; i < end; i++ {
		p := vis[i]
		host := projectHost(p)
		if host != currentHost {
			if i > start {
				rows = append(rows, "")
			}
			rows = append(rows, m.st.Subtitle.Render("on "+host))
			currentHost = host
		}
		marks := []string{}
		if p.HasGit {
			marks = append(marks, "git")
		}
		if p.HasCM {
			marks = append(marks, "CLAUDE")
		}
		if p.HasDocs {
			marks = append(marks, "docs/")
		}
		if p.Adopted && !p.HasGit && !p.HasCM {
			// Only surface "adopted" for directories that ONLY qualify
			// via the .ccmux/ marker; on a normal project (.git or
			// CLAUDE.md present) the mark would just be noise.
			marks = append(marks, "adopted")
		}
		tail := ""
		if len(marks) > 0 {
			tail = "   " + m.st.Muted.Render(strings.Join(marks, " · "))
		}
		line := "  " + p.Name + tail
		if i == m.cursor {
			line = m.st.ListItemSelected.Render(line)
		} else {
			line = m.st.ListItem.Render(line)
		}
		rows = append(rows, line)
	}
	return m.st.PaneFocused.Width(width - 2).Height(height - 2).Render(strings.Join(rows, "\n"))
}

func (m projectsModel) renderDetail(width, height int) string {
	sel := m.Selected()
	if sel == nil {
		return m.st.Pane.Width(width - 2).Height(height - 2).Render(m.st.Muted.Render("No selection."))
	}
	p := *sel
	host := projectHost(p)
	enterDesc := "attach or create session locally"
	if host != "local" {
		enterDesc = "create session on " + host + " (via ccmuxd), then ssh-attach"
	}
	// Resolve the agent's display name through the registry so a future
	// rename of ID strings doesn't break the detail pane.
	agentDisplay := agent.ByID(p.Agent).DisplayName()
	lines := []string{
		m.st.Emphasis.Render(p.Name) + "   " + m.st.Muted.Render("on "+host),
		m.st.Muted.Render(p.Path),
		"",
		"session name  " + m.st.Emphasis.Render(p.SessionName()),
		"agent         " + m.st.Emphasis.Render(agentDisplay),
		"",
		m.st.Subtitle.Render("Keys"),
		m.st.Key.Render("enter") + "  " + enterDesc,
		m.st.Key.Render("n") + "      new project (modal form)",
		m.st.Key.Render("A") + "      adopt an existing directory under your projects root",
		m.st.Key.Render("a") + "      switch agent for this project (cycles claude→codex→antigravity; local only)",
		m.st.Key.Render("c") + "      show conversations for this project",
		m.st.Key.Render("5") + "      open Notes for this project (local only)",
	}
	return m.st.Pane.Width(width - 2).Height(height - 2).Render(strings.Join(lines, "\n"))
}

// projectHost returns the canonical host label for `p` — "local" when
// the project lives on this machine, the remote host name otherwise.
// Centralized so the renderer and the attach router can't drift.
func projectHost(p project.Project) string {
	if p.Host == "" {
		return "local"
	}
	return p.Host
}

// createProjectCmd creates a new project — its directory plus an agent
// session — and returns a projectSessionReadyMsg so app.go can dispatch
// the tmux-attach via tea.ExecProcess. It does NOT scaffold the project
// (no CLAUDE.md / docs/ / git init); ccmux only makes the directory and
// launches the agent.
//
// For a remote host pick, ccmux POSTs /v1/projects to that host's
// ccmuxd so the directory + session are created natively on the remote,
// then fires remoteSessionStartedMsg so the app exec's into ssh-attach.
func createProjectCmd(submit newProjectSubmitMsg) tea.Cmd {
	return func() tea.Msg {
		// Remote case: hand off to the remote daemon.
		if submit.Host != "" && submit.Host != "local" && submit.Address != "" {
			cli := daemon.RemoteClient(submit.Address)
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			res, err := cli.NewProject(ctx, daemon.NewProjectRequest{
				Name:  submit.Name,
				Agent: string(submit.Agent),
			})
			if err != nil {
				return toastMsg{
					Text:  "new project on " + submit.Host + ": " + err.Error(),
					Kind:  toastError,
					Until: time.Now().Add(8 * time.Second),
				}
			}
			dial := submit.DialHost
			if dial == "" {
				dial = submit.Host
			}
			return remoteSessionStartedMsg{
				SessionName: res.Session,
				DialHost:    dial,
			}
		}
		// Local case: pass the picker's chosen agent through so the
		// sidecar gets written and the launch command matches.
		opts := scaffold.Options{
			Name:  submit.Name,
			Agent: submit.Agent,
		}
		session, err := scaffold.StartSession(context.Background(), opts)
		if err != nil {
			return toastMsg{Text: "new project: " + err.Error(), Kind: toastError, Until: time.Now().Add(6 * time.Second)}
		}
		return projectSessionReadyMsg{Session: session, Project: submit.Name}
	}
}

// textInputBlink is a small wrapper around textinput.Blink so we don't have
// to import textinput in app.go.
func textInputBlink() tea.Cmd {
	// imported indirectly; the new-project form uses textinput which already
	// owns its blink scheduling. This is here for symmetry / future use.
	return nil
}

// isNarrow reports whether the terminal is too narrow for side-by-side
// layouts. The single layout breakpoint for the whole TUI: every screen
// and the chrome rows branch on this and nothing else. 120 catches the
// whole phone — iPhone portrait (~40–65 cols) and landscape (~90–110)
// both fall below it — so a real phone never gets the desktop layout.
func isNarrow(width int) bool { return width < 120 }

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
