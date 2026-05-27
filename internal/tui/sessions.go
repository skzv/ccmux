package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tmux"
	"github.com/skzv/ccmux/internal/tmuxchrome"
	"github.com/skzv/ccmux/internal/tui/components"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// sessionsModel is the full session list with a details pane.
// Under narrow terminals (< 120 cols), a condensed detail is shown.
type sessionsModel struct {
	st       styles.Styles
	km       Keymap
	sessions []daemon.SessionState
	cursor   int

	// `n` opens a modal form for spawning a bare (project-less)
	// session on any device. nil when the modal isn't showing.
	form *newSessionFormModel

	// `R` opens an inline rename form for the selected session.
	// nil when not showing.
	renameForm *renameFormModel

	// Cached so the form's device picker has the same set as the
	// Projects screen. Pushed by App on every sessionsLoadedMsg.
	hosts []hostStatus

	// Resolved sessions.default_dir for the form's placeholder.
	// Pushed by App on config load / reload.
	defaultDir string

	// Resolved sessions.default_agent — selects the form's agent
	// picker default at open time. Pushed by App on config load /
	// reload; empty falls back to the first installed agent.
	defaultAgent string

	// agentCommands are setup-pinned executable paths for agents that
	// may not be on this process's PATH, such as npm CLIs under nvm.
	agentCommands agent.Commands
}

func newSessions(st styles.Styles, km Keymap) sessionsModel {
	return sessionsModel{st: st, km: km}
}

func (m *sessionsModel) SetSessions(ss []daemon.SessionState) {
	// Preserve cursor by session name across refreshes. Auto-polling
	// fires every 2s; without this the cursor index silently shifts to
	// a different session whenever the list order changes (e.g. a
	// session becomes attached, gets renamed, or a new one is created
	// that sorts ahead of the current selection).
	var selectedName string
	if m.cursor >= 0 && m.cursor < len(m.sessions) {
		selectedName = m.sessions[m.cursor].Name
	}
	m.sessions = ss
	if selectedName != "" {
		for i, s := range ss {
			if s.Name == selectedName {
				m.cursor = i
				return
			}
		}
	}
	if m.cursor >= len(ss) {
		m.cursor = max0(len(ss) - 1)
	}
}

// Selected returns the currently-highlighted session, or nil.
func (m sessionsModel) Selected() *daemon.SessionState {
	if m.cursor < 0 || m.cursor >= len(m.sessions) {
		return nil
	}
	s := m.sessions[m.cursor]
	return &s
}

// SetHosts is called by App on every sessionsLoadedMsg so the
// new-session form's device picker reflects what's reachable right
// now. Same shape as projectsModel.SetHosts.
func (m *sessionsModel) SetHosts(h []hostStatus) {
	m.hosts = h
}

// SetDefaultDir is called by App on startup + configReloadMsg. The
// form's workdir placeholder reflects whatever sessions.default_dir
// is configured.
func (m *sessionsModel) SetDefaultDir(d string) {
	m.defaultDir = d
}

// SetDefaultAgent is called by App on startup + configReloadMsg. The
// new-session form's agent picker pre-selects this value on open.
func (m *sessionsModel) SetDefaultAgent(a string) {
	m.defaultAgent = a
}

// SetAgentCommands is called by App on startup/config reload so the
// new-session picker can include setup-pinned agent executables even
// when their bare binary names are not on this process's PATH.
func (m *sessionsModel) SetAgentCommands(commands agent.Commands) {
	m.agentCommands = commands
}

func (m sessionsModel) Update(msg tea.Msg) (sessionsModel, tea.Cmd) {
	// Rename modal: route everything through the rename form except its
	// own finalizer messages, which App handles.
	if m.renameForm != nil {
		switch msg := msg.(type) {
		case renameSessionCancelMsg:
			m.renameForm = nil
			return m, nil
		case renameSessionSubmitMsg:
			m.renameForm = nil
			return m, func() tea.Msg { return msg }
		}
		f, cmd := m.renameForm.Update(msg)
		m.renameForm = &f
		return m, cmd
	}

	// Modal mode: route everything through the form except its own
	// finalizer messages, which the App handles.
	if m.form != nil {
		switch msg := msg.(type) {
		case newBareSessionCancelMsg:
			m.form = nil
			return m, nil
		case newBareSessionSubmitMsg:
			m.form = nil
			// App handles the dispatch (local vs remote, attach).
			// We forward the message untouched.
			return m, func() tea.Msg { return msg }
		}
		f, cmd := m.form.Update(msg)
		m.form = &f
		return m, cmd
	}

	if km, ok := msg.(tea.KeyMsg); ok {
		switch {
		case keyMatches(km, m.km.NewItem):
			// Open the new-session form.
			f := newNewSessionForm(m.st, m.hosts, m.defaultDir, m.defaultAgent, m.agentCommands)
			m.form = &f
			return m, textinput.Blink
		case keyMatches(km, m.km.Rename):
			if sel := m.Selected(); sel != nil {
				f := newRenameForm(m.st, sel.Name)
				m.renameForm = &f
				return m, textinput.Blink
			}
		case keyMatches(km, m.km.Up):
			if m.cursor > 0 {
				m.cursor--
			}
		case keyMatches(km, m.km.Down):
			if m.cursor < len(m.sessions)-1 {
				m.cursor++
			}
		}
	}
	return m, nil
}

// View renders the sessions list with a detail pane for the selected
// row stacked beneath it. `narrow` is the terminal's narrow state (a
// phone) — when set the detail collapses to a condensed form. It is
// passed in rather than derived from `width`, which on a monitor is
// only this component's column and is itself below the breakpoint.
func (m sessionsModel) View(width, height int, narrow bool) string {
	if m.renameForm != nil {
		formW := minInt(80, width-4)
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, m.renameForm.View(formW))
	}
	// Modal form overlay — mirrors how projectsModel renders its
	// new-project modal: dimmed list behind, centered form on top.
	if m.form != nil {
		formW := minInt(80, width-4)
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, m.form.View(formW))
	}
	// List on top, detail for the selected row below. The detail keeps
	// the full key cheatsheet when the terminal is wide and collapses
	// to a condensed form on a phone.
	detail := m.renderDetail(width, narrow)
	listH := height - lipgloss.Height(detail)
	if listH < 3 {
		listH = 3
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		m.renderList(width, listH),
		detail,
	)
}

func (m sessionsModel) renderList(width, height int) string {
	// Pane chrome reservation: 2 cells of border + 2 cells of
	// Padding(0,1) = 4 cells eaten before content. The list itself
	// owns 2 more on the left (accent bar + space) for the
	// components.List selection treatment, so each row's content
	// fits in (width - 6) cells.
	inner := width - 4
	header := m.st.Emphasis.Render("Sessions") + "  " + m.sessionsCount()
	if len(m.sessions) == 0 {
		body := lipgloss.JoinVertical(lipgloss.Left,
			header,
			"",
			m.st.Muted.Render("No sessions yet."),
			"",
			"Press "+m.st.Key.Render("3")+" to open Projects and create one.",
		)
		return m.st.Pane.Width(width - 2).Height(height - 2).Render(body)
	}
	rowInner := inner - 2
	list := components.List(m.st, components.ListProps[daemon.SessionState]{
		Items: m.sessions,
		Render: func(s daemon.SessionState) components.ListItem {
			return components.ListItem{Primary: renderSessionLine(m.st, s, rowInner)}
		},
		Cursor: m.cursor,
		Width:  inner,
	})
	body := lipgloss.JoinVertical(lipgloss.Left, header, "", list)
	return m.st.PaneFocused.Width(width - 2).Height(height - 2).Render(body)
}

// renderDetail draws the Sessions detail pane for the selected row.
// `narrow` is the terminal's narrow state (a phone): when set the
// pane collapses further via renderDetailNarrow.
//
// The wide layout keeps the T0/T1 identity facts (name, host,
// project, state, path, attached, last-changed) and drops the T2
// reference material (created time, window count, the multi-line
// key cheatsheet, the detach-sequence instructions). The dropped
// content is covered by the HelpBar at the bottom of the screen
// and the `?` help overlay, so it doesn't disappear — it just stops
// competing with the dashboard tiles for vertical real estate.
func (m sessionsModel) renderDetail(width int, narrow bool) string {
	sel := m.Selected()
	if sel == nil {
		return m.st.Pane.Width(width - 2).MaxWidth(width).Render(m.st.Muted.Render("No session selected."))
	}
	if narrow {
		return m.renderDetailNarrow(*sel, width)
	}
	attachedLine := fmt.Sprintf("attached %s", m.st.Muted.Render("no"))
	if sel.Attached {
		attachedLine = "attached " + lipgloss.NewStyle().Foreground(m.st.P.Mauve).Bold(true).Render("⊙ yes")
	}
	subtitle := fmt.Sprintf("on %s", sel.Host)
	if sel.Project != "" {
		subtitle += " · " + sel.Project
	}
	lines := []string{
		m.st.Emphasis.Render(sel.Name),
		m.st.Muted.Render(subtitle),
		"",
		fmt.Sprintf("state    %s %s", stateGlyph(m.st, sel.State), sel.State),
		fmt.Sprintf("path     %s", truncate(summarizePath(sel.Path), width-12)),
		attachedLine,
		// "changed" duplicates the age the sessions list already shows
		// on the row itself, so the detail pane carries "created"
		// instead — the one timestamp the list doesn't surface.
		fmt.Sprintf("created  %s", relTime(sel.Created)),
	}
	return m.st.Pane.Width(width - 2).MaxWidth(width).Render(strings.Join(lines, "\n"))
}

// renderDetailNarrow is the condensed Sessions detail for narrow
// terminals: T0 identity (name / host / state / project) plus the T1
// attached state and a one-line detach reminder. The path, window
// count, timestamps, and the full key cheatsheet (all T2) are dropped
// — the wide layout and the CLI still carry them.
func (m sessionsModel) renderDetailNarrow(sel daemon.SessionState, width int) string {
	attached := "no"
	if sel.Attached {
		attached = lipgloss.NewStyle().Foreground(m.st.P.Mauve).Bold(true).Render("⊙ yes")
	}
	lines := []string{
		m.st.Emphasis.Render(sel.Name),
		m.st.Muted.Render("on " + sel.Host),
		"",
		fmt.Sprintf("state     %s %s", stateGlyph(m.st, sel.State), sel.State),
		fmt.Sprintf("project   %s", sel.Project),
		fmt.Sprintf("attached  %s", attached),
		m.st.Muted.Render("detach: press " + detectedPrefix() + " then d"),
	}
	return m.st.Pane.Width(width - 2).MaxWidth(width).Render(strings.Join(lines, "\n"))
}

// detectedPrefix returns the user's tmux prefix key as a pretty string
// (e.g. "Ctrl-b" — or "Ctrl-a" if they remapped). Called inline by the
// detail pane render; tmux show-options is fast enough at the cadence
// the TUI re-renders.
func detectedPrefix() string {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	return tmuxchrome.DetectedPrefix(ctx)
}

// sessionsCount renders the parenthetical breakdown shown in the
// Sessions pane title: total count plus one segment per non-empty
// state, each colored to match the session-state glyph used in the
// list rows (active=green, idle=sky, waiting=yellow). E.g.:
//
//	(3 · 1 active · 1 idle · 1 waiting)
//
// Replaces the standalone "Session summary" tile that previously
// lived in the dashboard's right column — same information, one
// row instead of seven.
func (m sessionsModel) sessionsCount() string {
	total := len(m.sessions)
	if total == 0 {
		return m.st.Muted.Render("(0)")
	}
	var active, idle, waiting int
	for _, s := range m.sessions {
		switch s.State {
		case "active":
			active++
		case "idle":
			idle++
		case "needs_input":
			waiting++
		}
	}
	parts := []string{m.st.Muted.Render(fmt.Sprintf("%d", total))}
	if active > 0 {
		parts = append(parts, m.st.StatusGood.Render(fmt.Sprintf("%d active", active)))
	}
	if idle > 0 {
		parts = append(parts, m.st.StateIdle.Render(fmt.Sprintf("%d idle", idle)))
	}
	if waiting > 0 {
		parts = append(parts, m.st.StateNeedsInput.Render(fmt.Sprintf("%d waiting", waiting)))
	}
	return m.st.Muted.Render("(") + strings.Join(parts, m.st.Muted.Render(" · ")) + m.st.Muted.Render(")")
}

// killSessionCmd runs `tmux kill-session -t <name>` and reports the result
// via sessionKilledMsg, which the app uses to trigger a refresh.
var killSessionCmd = func(name string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		err := tmux.Kill(ctx, name)
		return sessionKilledMsg{Name: name, Err: err}
	}
}

func relTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return humanDuration(time.Since(t)) + " ago"
}

func truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return s[:n-1] + "…"
}

func max0(x int) int {
	if x < 0 {
		return 0
	}
	return x
}
