package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/scaffold"
	"github.com/skzv/ccmux/internal/tui/components"
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

	// hosts is the live reachable-peer list, fed in from App on every
	// sessionsLoadedMsg. Snapshot into the form at "n"-press time so
	// the picker shows what the user was looking at.
	hosts []hostStatus

	// defaultAgent — resolved cfg.Agents.Default, pushed by App on
	// config load/reload. Selects the new-project form's agent picker
	// at open time. Empty falls back to the first installed agent.
	defaultAgent string

	// agentCommands are setup-pinned executable paths for agents that
	// may not be on this process's PATH, such as npm CLIs under nvm.
	agentCommands agent.Commands

	// Filter state. When filterActive is true, keystrokes feed the
	// textinput and the list view shows only projects whose name
	// matches the filter (case-insensitive substring). Cursor is
	// always an index into the *visible* (filtered) list, so
	// Selected() reflects what the user sees.
	filter       textinput.Model
	filterActive bool

	// loaded flips true on the first projectsLoadedMsg, regardless of
	// whether the discovered slice was empty. Lets the renderer tell
	// "no projects found yet" (spinner) from "scan finished, the
	// projects root genuinely has none" (empty-state placeholder).
	loaded bool

	// spin animates while the projects refresh is in flight. Started
	// in newProjects so the very first frame already has motion.
	spin spinner.Model
}

func newProjects(st styles.Styles, km Keymap) projectsModel {
	ti := textinput.New()
	ti.Placeholder = "type to filter…"
	ti.Prompt = "/ "
	ti.CharLimit = 64
	ti.Width = 40
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(st.Semantic.Accent)
	return projectsModel{st: st, km: km, filter: ti, spin: sp}
}

func (m *projectsModel) SetProjects(p []project.Project) {
	m.projects = p
	m.loaded = true
	m.clampCursor()
}

// SetDefaultAgent is the App-side hook that pushes cfg.Agents.Default
// into the projects model so the next "new project" form opens with
// the user's preferred agent pre-selected. Pushed on startup and on
// configReloadMsg (after the user edits Settings or config.toml).
func (m *projectsModel) SetDefaultAgent(a string) {
	m.defaultAgent = a
}

// SetAgentCommands is called by App on startup/config reload so the
// new-project picker can include setup-pinned agent executables even
// when their bare binary names are not on this process's PATH.
func (m *projectsModel) SetAgentCommands(commands agent.Commands) {
	m.agentCommands = commands
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

// Init kicks the spinner so the very first frame animates while the
// initial refreshProjectsCmd is in flight.
func (m projectsModel) Init() tea.Cmd { return m.spin.Tick }

func (m projectsModel) Update(msg tea.Msg) (projectsModel, tea.Cmd) {
	// Spinner advances on its own tick. Forwarding only the spinner
	// message (not all messages) keeps the spinner self-driving
	// without polluting the rest of the model's Update with batch
	// commands every keystroke.
	if _, ok := msg.(spinner.TickMsg); ok {
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
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
			f := newNewProjectForm(m.st, m.hosts, m.defaultAgent, m.agentCommands)
			m.form = &f
			return m, tea.Batch(textInputBlink())
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

// switchAgentCmd is the projects-detail-pane "a" action. Writes the
// sidecar, signals the in-memory list to update, and emits a success
// toast. The toast renders at the top of the frame (see
// renderTopToast in app.go) so it doesn't compete with the bottom
// HelpBar for the user's attention.
//
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
					Text:  p.Name + ": agent → " + string(next) + " (next session uses this)",
					Kind:  toastSuccess,
					Until: time.Now().Add(5 * time.Second),
				}
			},
		)()
	}
}

func (m projectsModel) View(width, height int) string {
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
	// 50/50 split. The right (detail) pane carries the absolute path
	// and the chip line `[git] [CLAUDE] [docs/]`; both wrap on
	// narrower allocations like the previous 1/3 split.
	leftW := width / 2
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
	// Pane chrome reservation: border (2) + Padding(0,1) (2) = 4 cells
	// eaten before content. The components row-decorator owns 2 more
	// on the left for the accent bar (selection treatment).
	inner := width - 4
	header := m.st.Emphasis.Render("Projects")
	if !narrow {
		header += "  " + m.st.Muted.Render(fmt.Sprintf("(%d)", len(m.projects)))
	}
	if len(m.projects) == 0 {
		var body string
		if !m.loaded {
			body = lipgloss.JoinVertical(lipgloss.Left,
				header,
				"",
				m.spin.View()+" "+m.st.Muted.Render("Discovering projects…"),
			)
		} else {
			body = lipgloss.JoinVertical(lipgloss.Left,
				header,
				"",
				m.st.Muted.Render("No projects found under your projects root."),
				"",
				"Press "+m.st.Key.Render("n")+" to scaffold a new one.",
			)
		}
		return m.st.Pane.Width(width - 2).Height(height - 2).Render(body)
	}

	vis := m.visibleProjects()
	rows := []string{header}
	// Agent-color legend. One line directly under the title so the
	// user can correlate the per-row dots to the agent that runs
	// each project. Only emitted when the list has rows — the
	// legend is meaningless against an empty pane.
	rows = append(rows, renderAgentLegend(m.st))
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
		selected := i == m.cursor
		// The dot encodes the project's *agent* (claude, codex,
		// antigravity, cursor). Host identity is preserved in the
		// `on <host>` subheader above and in the detail pane.
		// Pressing `a` cycles the agent on the focused row and the
		// dot's color follows on the next frame.
		dot := m.st.AgentAccent(p.Agent).Render("•")
		content := dot + " " + p.Name + renderScaffoldChips(m.st, p, selected)
		line := components.RenderListRow(m.st, content, selected, inner)
		rows = append(rows, line)
	}
	return m.st.PaneFocused.Width(width - 2).Height(height - 2).Render(strings.Join(rows, "\n"))
}

// HelpBarProps returns the screen-specific key hints for Projects.
// Priorities order the collapse: ? help and q quit always survive;
// the filter and scaffold hints come next; navigation lands last.
func (m projectsModel) HelpBarProps(width int) components.HelpBarProps {
	return components.HelpBarProps{
		Hints: []components.KeyHint{
			{Key: "?", Label: "help", Priority: 10},
			{Key: "q", Label: "quit", Priority: 10},
			{Key: "enter", Label: "attach", Priority: 8},
			{Key: "n", Label: "new", Priority: 7},
			{Key: "i", Label: "info", Priority: 7},
			{Key: "/", Label: "filter", Priority: 6},
			{Key: "a", Label: "switch agent", Priority: 5},
			{Key: "c", Label: "conversations", Priority: 4},
			{Key: "r", Label: "refresh", Priority: 3},
			{Key: "1-7", Label: "screens", Priority: 2},
		},
		Width: width,
	}
}

func (m projectsModel) renderDetail(width, height int) string {
	sel := m.Selected()
	if sel == nil {
		return m.st.Pane.Width(width - 2).Height(height - 2).Render(m.st.Muted.Render("No selection."))
	}
	p := *sel
	host := projectHost(p)
	// Resolve the agent's display name through the registry so a future
	// rename of ID strings doesn't break the detail pane.
	agentDisplay := agent.ByID(p.Agent).DisplayName()
	detected := renderScaffoldChips(m.st, p, false)
	if detected == "" {
		detected = m.st.Muted.Render("(none)")
	} else {
		detected = strings.TrimLeft(detected, " ")
	}
	lines := []string{
		m.st.Emphasis.Render(p.Name) + "   " + m.st.HostColor(host).Render("● "+host),
		m.st.Muted.Render(summarizePath(p.Path)),
		"",
		"session   " + m.st.Emphasis.Render(p.SessionName()),
		"agent     " + m.st.AgentAccent(p.Agent).Render("• ") + m.st.Emphasis.Render(agentDisplay),
		"detected  " + detected,
		"",
		m.st.Key.Render("a") + " " + m.st.Muted.Render("switch agent") + "   " +
			m.st.Key.Render("i") + " " + m.st.Muted.Render("full project info"),
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

// renderAgentLegend renders the per-agent color key shown at the top
// of the Projects list. It enumerates agent.All() so a future agent
// addition appears in the legend without touching this file. Format:
// `agents:  ● claude  ● codex  ● antigravity  ● cursor`. The dots are
// rendered through Styles.AgentAccent so the legend and per-row dots
// always agree on the same color for the same agent.
func renderAgentLegend(st styles.Styles) string {
	parts := []string{st.Muted.Render("agents:")}
	for _, a := range agent.All() {
		dot := st.AgentAccent(a.ID()).Render("•")
		// Lowercase agent ID (not DisplayName) — the IDs are short
		// enough to fit on one line in a 60-cell pane, and they're
		// already the canonical strings the rest of the codebase
		// uses (the `.ccmux/agent` sidecar, the CLI, the toast).
		parts = append(parts, dot+" "+st.Muted.Render(string(a.ID())))
	}
	return strings.Join(parts, "  ")
}

// renderScaffoldChips renders the bracketed `[git] [CLAUDE] [docs/]`
// chips for the project row. Selected rows use the accent foreground;
// off-row stays muted. Returns "" when the project has no scaffolding
// flags so callers don't render a leading gap for nothing.
func renderScaffoldChips(st styles.Styles, p project.Project, selected bool) string {
	labels := []string{}
	if p.HasGit {
		labels = append(labels, "[git]")
	}
	if p.HasCM {
		labels = append(labels, "[CLAUDE]")
	}
	if p.HasAgents {
		labels = append(labels, "[AGENTS]")
	}
	if p.HasDocs {
		labels = append(labels, "[docs/]")
	}
	if len(labels) == 0 {
		return ""
	}
	style := st.Muted
	if selected {
		style = lipgloss.NewStyle().Foreground(st.Semantic.Accent)
	}
	return "   " + style.Render(strings.Join(labels, " "))
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
		cfg, _ := config.Load()
		opts := scaffold.Options{
			Name: submit.Name,
			// Place the new project under the configured projects root,
			// exactly as the daemon's createProject does. Without an
			// explicit Dir, scaffold.PrepareDir falls back to
			// filepath.Abs(Name), which resolves against the TUI
			// process's working directory (typically $HOME) — so a
			// project created from the Projects screen would land in ~
			// instead of ~/Projects.
			Dir:      localProjectDir(cfg, submit.Name),
			Agent:    submit.Agent,
			Commands: cfg.AgentCommands(),
		}
		session, err := scaffold.StartSession(context.Background(), opts)
		if err != nil {
			return toastMsg{Text: "new project: " + err.Error(), Kind: toastError, Until: time.Now().Add(6 * time.Second)}
		}
		return projectSessionReadyMsg{Session: session, Project: submit.Name}
	}
}

// localProjectDir resolves the absolute directory a locally-created
// project lands in: under the configured projects root, identical to
// what the daemon's createProject computes. Extracted so the path
// resolution is unit-testable without standing up tmux (StartSession
// shells out). The historical bug this guards against: leaving Dir
// empty let scaffold.PrepareDir fall back to filepath.Abs(Name),
// which resolves against the TUI's working directory ($HOME) and
// dropped new projects in ~ instead of ~/Projects.
func localProjectDir(cfg config.Config, name string) string {
	return filepath.Join(project.ResolveRoot(cfg.Projects.Root), name)
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
