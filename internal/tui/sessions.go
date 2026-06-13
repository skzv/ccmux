package tui

import (
	"context"
	"fmt"
	"sort"
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

// sortByAttention re-orders the sessions slice in-place by descending
// attention priority (most-urgent first), breaking ties by Name. The
// priority comes from agent.AttentionPriority — see its docstring for
// the ranking. Stable so an unchanged list stays put across renders.
func sortByAttention(ss []daemon.SessionState) {
	sort.SliceStable(ss, func(i, j int) bool {
		pi := agent.AttentionPriority(agent.State(ss[i].State), ss[i].Seen)
		pj := agent.AttentionPriority(agent.State(ss[j].State), ss[j].Seen)
		if pi != pj {
			return pi > pj
		}
		return ss[i].Name < ss[j].Name
	})
}

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

	// showPreview gates the side-by-side preview pane. Toggled with
	// `p`. When on, the View splits the screen horizontally — list on
	// the left, the selected session's recent pane content on the
	// right — and a tick refreshes the capture every second. Off by
	// default; ccmux's principle is "tmux is the multiplexer", so the
	// preview is opt-in read-only chrome, not a step toward a built-in
	// split.
	showPreview bool
	// previewSession is the name of the session whose capture is
	// cached in `preview`. Lets us detect a cursor move and trigger an
	// immediate re-capture without waiting for the tick.
	previewSession string
	preview        string
	previewLoading bool
	previewErr     string
}

// previewLoadedMsg carries the result of one tmux capture-pane call
// for the preview pane. Routed by App into sessionsModel via the same
// path as other session-screen messages.
type previewLoadedMsg struct {
	Session string
	Content string
	Err     error
}

// previewTickMsg fires once a second when the preview pane is on. The
// handler issues a capture command and schedules the next tick. When
// `p` toggles the preview off, in-flight ticks are dropped (the
// handler checks showPreview before scheduling the next one) so we
// don't keep ticking forever.
type previewTickMsg struct{}

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
	// Re-sort by attention priority so a session waiting on the user
	// (needs_input / done-but-unreviewed / error) surfaces above
	// still-working rows. Stable secondary key on Name keeps the order
	// deterministic when several rows share a priority, so repeated
	// renders don't flicker the same set of names.
	sortByAttention(ss)
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

	// Preview pane messages flow into the model regardless of the key
	// path below — they're scheduled by `p`'s toggle and replenished by
	// each tick, so they arrive asynchronously.
	switch msg := msg.(type) {
	case previewLoadedMsg:
		// Stale loads from a previous selection lose to the current one:
		// if the user moved the cursor while the capture was in flight,
		// drop the old result silently rather than flashing wrong content.
		sel := m.Selected()
		if sel == nil || msg.Session != sel.Name {
			m.previewLoading = false
			return m, nil
		}
		m.previewLoading = false
		if msg.Err != nil {
			m.previewErr = msg.Err.Error()
			m.preview = ""
		} else {
			m.previewErr = ""
			m.preview = msg.Content
		}
		m.previewSession = msg.Session
		return m, nil
	case previewTickMsg:
		// Tick fired — if the user toggled preview off in the meantime,
		// drop it (no capture, no next tick). Otherwise refresh and
		// schedule the next tick.
		if !m.showPreview {
			return m, nil
		}
		if sel := m.Selected(); sel != nil {
			return m, tea.Batch(capturePreviewCmd(*sel), previewTickCmd())
		}
		return m, previewTickCmd()
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
		case keyMatches(km, m.km.Preview):
			m.showPreview = !m.showPreview
			if m.showPreview {
				// Turning preview on: capture immediately AND start the
				// tick. Batching produces one Cmd; Bubble Tea fans it
				// out internally. The first capture fills the pane
				// without waiting a full second; the tick keeps it warm.
				if sel := m.Selected(); sel != nil {
					m.previewLoading = true
					return m, tea.Batch(capturePreviewCmd(*sel), previewTickCmd())
				}
				return m, previewTickCmd()
			}
			// Turning preview off: clear cached content so a future
			// re-toggle doesn't flash stale text from the previous
			// selection. The in-flight tick will see !showPreview and
			// stop scheduling itself.
			m.preview = ""
			m.previewErr = ""
			m.previewLoading = false
			m.previewSession = ""
			return m, nil
		case keyMatches(km, m.km.Up):
			if m.cursor > 0 {
				m.cursor--
				return m, m.maybeRefreshPreview()
			}
		case keyMatches(km, m.km.Down):
			if m.cursor < len(m.sessions)-1 {
				m.cursor++
				return m, m.maybeRefreshPreview()
			}
		}
	}
	return m, nil
}

// maybeRefreshPreview returns a capture command when the preview is on
// and the cursor just moved to a different session — so the right pane
// updates instantly instead of waiting for the next tick. Returns nil
// when preview is off (most cases) so the dispatch loop does no work.
func (m *sessionsModel) maybeRefreshPreview() tea.Cmd {
	if !m.showPreview {
		return nil
	}
	sel := m.Selected()
	if sel == nil {
		return nil
	}
	m.previewLoading = true
	return capturePreviewCmd(*sel)
}

// capturePreviewCmd reads the last N lines of the named session's
// active pane. Local sessions go through tmux directly; remote sessions
// route through the daemon client at the matching host (the daemon
// already exposes `/v1/sessions/{name}/preview` for the mobile clients
// and the MCP server).
//
// Variable rather than const so the test can stub it without a live
// tmux. The wired-in production capture is the closure below.
var capturePreviewCmd = func(s daemon.SessionState) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		const lines = 30
		// Local host: capture directly via tmux. Remote: fall through
		// to the daemon client lookup. The "local" / "" check matches
		// daemon.SessionState.Host's convention (empty = local).
		if s.Host == "" || s.Host == "local" {
			out, err := tmux.CapturePane(ctx, s.Name, lines)
			return previewLoadedMsg{Session: s.Name, Content: out, Err: err}
		}
		// Remote: the daemon at the peer's address has a Preview method
		// (same path the mobile clients and ccmux-mcp use). The host
		// label here is the friendly name; the address resolution lives
		// in the App's host registry, which the sessions model doesn't
		// have. Until we plumb it in, mark the remote preview as
		// unsupported — most users want the preview for "the agent on
		// my mini," which IS local from the mini's TUI, so this is
		// a small gap. Tracked in docs/01_Specs/01_Feature_Catalog.md.
		return previewLoadedMsg{Session: s.Name, Err: errRemotePreviewNotWired}
	}
}

// previewTickCmd schedules the next preview refresh. One second is the
// sweet spot — fast enough that "watch the agent think" feels live, slow
// enough that we're not hammering tmux at 60Hz.
var previewTickCmd = func() tea.Cmd {
	return tea.Tick(1*time.Second, func(_ time.Time) tea.Msg {
		return previewTickMsg{}
	})
}

// errRemotePreviewNotWired is the sentinel for "you toggled preview on
// a remote session but we haven't wired the cross-host capture yet."
// Surfaces as the message body in the preview pane so the user sees
// why it's empty rather than a generic "no content."
var errRemotePreviewNotWired = remotePreviewErr{}

type remotePreviewErr struct{}

func (remotePreviewErr) Error() string {
	return "preview for remote sessions not yet supported — attach with Enter to view live content"
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
	// Preview pane mode: list on the left, the selected session's
	// recent pane content on the right. The bottom detail pane is
	// dropped to give the preview vertical real estate; the row in the
	// list already carries the state / agent / age, so the detail is
	// the least-load-bearing piece. Narrow terminals (a phone) fall
	// back to the default stacked layout — a horizontal split makes no
	// sense at <120 cols.
	if m.showPreview && !narrow && width >= 80 {
		// 55/45 split favoring the list — wide enough to keep the
		// session-line layout intact, narrow enough that the preview
		// still feels like "the right half." A literal 50/50 was
		// tested but truncated the path columns on the row at 120 cols.
		listW := (width * 55) / 100
		previewW := width - listW
		return lipgloss.JoinHorizontal(lipgloss.Top,
			m.renderList(listW, height),
			m.renderPreview(previewW, height),
		)
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

// renderPreview draws the side preview pane: the selected session's
// recent pane content, refreshed once a second. Shows a one-line
// status row (session name + cached/loading/error state) on top of the
// captured text.
func (m sessionsModel) renderPreview(width, height int) string {
	sel := m.Selected()
	if sel == nil {
		return m.st.Pane.Width(width - 2).Height(height - 2).Render(
			m.st.Muted.Render("No session selected."),
		)
	}
	header := m.st.Emphasis.Render(sel.Name)
	switch {
	case m.previewErr != "":
		// Show the err verbatim — the remote-not-supported sentinel and
		// any real tmux error both fit on a couple of lines.
		body := m.st.StateNeedsInput.Render("preview unavailable")
		body += "\n\n" + m.st.Muted.Render(m.previewErr)
		return m.st.Pane.Width(width - 2).Height(height - 2).Render(
			lipgloss.JoinVertical(lipgloss.Left, header, "", body),
		)
	case m.previewLoading && m.preview == "":
		return m.st.Pane.Width(width - 2).Height(height - 2).Render(
			lipgloss.JoinVertical(lipgloss.Left, header, "", m.st.Muted.Render("capturing…")),
		)
	case m.preview == "":
		return m.st.Pane.Width(width - 2).Height(height - 2).Render(
			lipgloss.JoinVertical(lipgloss.Left, header, "", m.st.Muted.Render("(empty pane)")),
		)
	}
	// Show the most-recent capture, trimmed to fit. Trailing blank
	// lines (tmux pads to pane height) get stripped so the preview
	// settles at the bottom instead of starting halfway down the box.
	body := strings.TrimRight(m.preview, "\n")
	return m.st.Pane.Width(width - 2).Height(height - 2).Render(
		lipgloss.JoinVertical(lipgloss.Left, header, "", body),
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
			"Press "+m.st.Key.Render(screenKey(ScreenProjects))+" to open Projects and create one.",
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
