// Package tui is the Bubble Tea root model and screen router for ccmux.
package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/skzv/ccmux/internal/claude"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tmux"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// Screen identifies which top-level screen is currently focused.
type Screen int

const (
	ScreenDashboard Screen = iota
	ScreenSessions
	ScreenProjects
	ScreenNotes
	ScreenClaude
	ScreenSettings
)

func (s Screen) String() string {
	return []string{"Dashboard", "Sessions", "Projects", "Notes", "Claude", "Settings"}[s]
}

// App is the root Bubble Tea model.
type App struct {
	cfg     config.Config
	styles  styles.Styles
	keys    Keymap
	version string

	width, height int

	screen   Screen
	sessions []daemon.SessionState
	projects []project.Project
	hosts    []hostStatus

	dashboard dashboardModel
	sessionsM sessionsModel
	projectsM projectsModel
	notes     notesModel
	claudeM   claudeModel
	settings  settingsModel

	toast      string
	toastKind  toastKind
	toastUntil time.Time

	lastRefresh  time.Time
	daemonOnline bool
}

// New constructs the root model.
func New(cfg config.Config, version string) App {
	st := styles.Default()
	km := DefaultKeymap()
	return App{
		cfg:       cfg,
		styles:    st,
		keys:      km,
		version:   version,
		screen:    ScreenDashboard,
		dashboard: newDashboard(st, km),
		sessionsM: newSessions(st, km),
		projectsM: newProjects(st, km),
		notes:     newNotes(st, km),
		claudeM:   newClaude(st, km),
		settings:  newSettings(st, km, cfg, version),
	}
}

// Init is called once at startup.
func (a App) Init() tea.Cmd {
	return tea.Batch(
		a.refreshSessionsCmd(),
		a.refreshProjectsCmd(),
		tickEvery(2*time.Second),
	)
}

// Update routes messages to the active screen and handles global keys.
func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		return a, nil

	case tickMsg:
		return a, tea.Batch(a.refreshSessionsCmd(), tickEvery(2*time.Second))

	case sessionsLoadedMsg:
		a.lastRefresh = msg.At
		a.sessions = msg.Sessions
		a.hosts = msg.Hosts
		a.daemonOnline = daemonOnline(msg.Hosts)
		a.dashboard.SetSessions(a.sessions)
		a.sessionsM.SetSessions(a.sessions)
		if msg.Err != nil {
			a.setToast(toastError, "refresh: "+msg.Err.Error(), 5*time.Second)
		}
		return a, nil

	case projectsLoadedMsg:
		if msg.Err == nil {
			a.projects = msg.Projects
			a.projectsM.SetProjects(a.projects)
		}
		return a, nil

	case toastMsg:
		a.setToast(msg.Kind, msg.Text, time.Until(msg.Until))
		return a, nil

	case projectSessionReadyMsg:
		// New project is scaffolded and its tmux session is running with
		// the initial prompt sent. Now attach. tea.ExecProcess suspends
		// the TUI for the duration of tmux attach, and resumes when the
		// user detaches.
		c := exec.Command("tmux", "attach-session", "-d", "-t", msg.Session)
		return a, tea.ExecProcess(c, func(err error) tea.Msg {
			return refreshAfterDetachMsg{}
		})

	case sessionKilledMsg:
		if msg.Err != nil {
			a.setToast(toastError, "kill failed: "+msg.Err.Error(), 5*time.Second)
		} else {
			a.setToast(toastSuccess, "killed "+msg.Name, 3*time.Second)
		}
		return a, a.refreshSessionsCmd()

	case refreshAfterDetachMsg:
		// Returning from tmux attach.
		return a, tea.Batch(a.refreshSessionsCmd(), a.refreshProjectsCmd())

	case tea.KeyMsg:
		// If projects screen has its modal open, route through it. We
		// intentionally still allow global Quit (ctrl+c).
		if a.screen == ScreenProjects && a.projectsM.form != nil {
			if msg.String() == "ctrl+c" {
				return a, tea.Quit
			}
			var cmd tea.Cmd
			a.projectsM, cmd = a.projectsM.Update(msg)
			return a, cmd
		}

		switch {
		case keyMatches(msg, a.keys.Quit):
			return a, tea.Quit
		case keyMatches(msg, a.keys.Dashboard):
			a.screen = ScreenDashboard
			return a, nil
		case keyMatches(msg, a.keys.Sessions):
			a.screen = ScreenSessions
			return a, nil
		case keyMatches(msg, a.keys.Projects):
			a.screen = ScreenProjects
			return a, nil
		case keyMatches(msg, a.keys.Notes):
			a.screen = ScreenNotes
			return a, nil
		case keyMatches(msg, a.keys.Claude):
			a.screen = ScreenClaude
			return a, nil
		case keyMatches(msg, a.keys.Settings):
			a.screen = ScreenSettings
			return a, nil
		case keyMatches(msg, a.keys.Refresh):
			return a, a.refreshSessionsCmd()
		case keyMatches(msg, a.keys.Enter) && a.screen == ScreenSessions:
			return a, a.attachSelectedSession()
		case keyMatches(msg, a.keys.Enter) && a.screen == ScreenProjects:
			return a, a.attachOrCreateForSelectedProject()
		}
	}

	// Forward to the active screen.
	var cmd tea.Cmd
	switch a.screen {
	case ScreenDashboard:
		a.dashboard, cmd = a.dashboard.Update(msg)
	case ScreenSessions:
		a.sessionsM, cmd = a.sessionsM.Update(msg)
	case ScreenProjects:
		a.projectsM, cmd = a.projectsM.Update(msg)
	case ScreenNotes:
		a.notes, cmd = a.notes.Update(msg)
	case ScreenClaude:
		a.claudeM, cmd = a.claudeM.Update(msg)
	case ScreenSettings:
		a.settings, cmd = a.settings.Update(msg)
	}
	return a, cmd
}

// View renders the whole UI.
func (a App) View() string {
	if a.width == 0 {
		return "loading…"
	}

	header := a.renderHeader()
	statusBar := a.renderStatusBar()
	footer := a.renderFooter()

	// Reserve 3 lines: header(1) + statusBar(1) + footer(1).
	// Every screen receives the full remaining height and is responsible
	// for sizing its own panes so the total comes out right (Pane.Height(h)
	// includes borders).
	bodyHeight := a.height - 3
	if bodyHeight < 5 {
		bodyHeight = 5
	}

	var body string
	switch a.screen {
	case ScreenDashboard:
		body = a.dashboard.View(a.width, bodyHeight)
	case ScreenSessions:
		body = a.sessionsM.View(a.width, bodyHeight)
	case ScreenProjects:
		body = a.projectsM.View(a.width, bodyHeight)
	case ScreenNotes:
		body = a.notes.View(a.width, bodyHeight)
	case ScreenClaude:
		body = a.claudeM.View(a.width, bodyHeight)
	case ScreenSettings:
		body = a.settings.View(a.width, bodyHeight)
	}

	return lipgloss.JoinVertical(lipgloss.Left, header, body, statusBar, footer)
}

// renderHeader is the top-of-screen tab strip. On narrow terminals the
// tab labels collapse to just their number; the full strip never wraps.
func (a App) renderHeader() string {
	tabs := []Screen{ScreenDashboard, ScreenSessions, ScreenProjects, ScreenNotes, ScreenClaude, ScreenSettings}
	parts := []string{a.styles.Title.Render(" ccmux ")}
	narrow := isNarrow(a.width)
	for i, t := range tabs {
		var label string
		if narrow {
			// Just the number when space is tight.
			label = fmt.Sprintf(" %d ", i+1)
			if t == a.screen {
				label = fmt.Sprintf("[%d %s]", i+1, t.String()[:1])
			}
		} else {
			label = fmt.Sprintf("[%d] %s", i+1, t.String())
			label = " " + label + " "
		}
		if t == a.screen {
			parts = append(parts, a.styles.Emphasis.Render(label))
		} else {
			parts = append(parts, a.styles.Muted.Render(label))
		}
	}
	line := lipgloss.NewStyle().Background(a.styles.P.BGAlt).Render(strings.Join(parts, ""))
	return forceSingleLine(line, a.width)
}

// renderStatusBar is the bottom-most informational strip. Forced to 1 line.
// On narrow terminals the right-side details are dropped first.
func (a App) renderStatusBar() string {
	host, _ := os.Hostname()
	left := a.styles.HostColor("local").Render("● " + shortHostname(host))

	daemonChip := a.styles.StatusError.Render("⚠ offline")
	if a.daemonOnline {
		daemonChip = a.styles.StatusGood.Render("✓ daemon")
	}

	dangerBanner := ""
	if a.cfg.Sleep.DangerousKeepAwakeOnBattery {
		dangerBanner = a.styles.StatusDanger.Render("⚠ BATT") + " "
	}

	leftBlock := left + "  " + dangerBanner + daemonChip

	refreshed := "—"
	if !a.lastRefresh.IsZero() {
		refreshed = a.lastRefresh.Format("15:04:05")
	}
	count := fmt.Sprintf("%d sess", len(a.sessions))
	right := a.styles.Muted.Render(fmt.Sprintf("%s • %s", count, refreshed))

	// Compute available space for the right block. If the left side
	// already overflows, we just render the left side and skip right
	// rather than letting PlaceHorizontal misbehave with negative width.
	leftW := lipgloss.Width(leftBlock)
	rightW := lipgloss.Width(right)
	body := leftBlock
	if a.width-leftW-rightW >= 2 {
		spacer := strings.Repeat(" ", a.width-leftW-rightW)
		body = leftBlock + spacer + right
	}
	line := a.styles.StatusBar.Render(body)
	return forceSingleLine(line, a.width)
}

// renderFooter is the help line. Single-row. Toast takes precedence.
func (a App) renderFooter() string {
	if a.toast != "" && time.Now().Before(a.toastUntil) {
		base := a.styles.Toast
		switch a.toastKind {
		case toastError:
			base = lipgloss.NewStyle().Background(a.styles.P.Red).Foreground(a.styles.P.BG).Padding(0, 1)
		case toastSuccess:
			base = lipgloss.NewStyle().Background(a.styles.P.Green).Foreground(a.styles.P.BG).Padding(0, 1)
		case toastWarning:
			base = lipgloss.NewStyle().Background(a.styles.P.Yellow).Foreground(a.styles.P.BG).Padding(0, 1)
		}
		return forceSingleLine(base.Render(a.toast), a.width)
	}
	hint := "1-6 screens • n new • x kill • r refresh • ? help • q quit"
	return forceSingleLine(a.styles.Muted.Render(hint), a.width)
}

// forceSingleLine guarantees the rendered string is exactly one line tall
// and at most `width` *display* cells wide. Uses ansi.Truncate so styled
// content (ANSI escape sequences) is preserved correctly — a plain
// rune-slice would happily chop a sequence in half and corrupt the
// terminal state.
func forceSingleLine(s string, width int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	return ansi.Truncate(s, width, "…")
}

// shortHostname strips trailing ".local" / ".lan" / tailnet suffix for the
// status bar so "sputnik.mini.skz.dev" becomes "sputnik".
func shortHostname(h string) string {
	if i := strings.IndexByte(h, '.'); i > 0 {
		return h[:i]
	}
	return h
}

func (a *App) setToast(kind toastKind, text string, ttl time.Duration) {
	a.toast = text
	a.toastKind = kind
	if ttl <= 0 {
		ttl = 3 * time.Second
	}
	a.toastUntil = time.Now().Add(ttl)
}

// refreshSessionsCmd fetches sessions from local ccmuxd and every configured
// remote host. Falls back to direct tmux call when the local daemon is down.
func (a App) refreshSessionsCmd() tea.Cmd {
	hosts := a.cfg.Hosts
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var (
			sessions []daemon.SessionState
			hs       []hostStatus
			err      error
		)

		local, lerr := daemon.LocalClient()
		if lerr == nil {
			ss, e := local.Sessions(ctx)
			if e == nil {
				for i := range ss {
					ss[i].Host = "local"
				}
				sessions = append(sessions, ss...)
				h, _ := local.Health(ctx)
				hs = append(hs, hostStatus{Name: "local", Address: local.Addr(), OK: h.OK, Sessions: h.Sessions, SleepMode: h.SleepMode})
			} else {
				direct, e2 := fallbackDirectTmux(ctx)
				if e2 == nil {
					sessions = append(sessions, direct...)
					hs = append(hs, hostStatus{Name: "local", Address: "tmux (no daemon)", OK: false})
				} else {
					err = fmt.Errorf("local: %w", e2)
				}
			}
		}

		for _, h := range hosts {
			addr := h.Address
			if h.Port == 0 {
				addr += ":7474"
			} else {
				addr += fmt.Sprintf(":%d", h.Port)
			}
			cli := daemon.RemoteClient(addr)
			ss, e := cli.Sessions(ctx)
			st := hostStatus{Name: h.Name, Address: addr}
			if e == nil {
				st.OK = true
				st.Sessions = len(ss)
				for i := range ss {
					ss[i].Host = h.Name
				}
				sessions = append(sessions, ss...)
			} else {
				st.Err = e
			}
			hs = append(hs, st)
		}

		sort.SliceStable(sessions, func(i, j int) bool {
			pi := statePriority(sessions[i].State)
			pj := statePriority(sessions[j].State)
			if pi != pj {
				return pi < pj
			}
			if sessions[i].Host != sessions[j].Host {
				return sessions[i].Host < sessions[j].Host
			}
			return sessions[i].Name < sessions[j].Name
		})

		return sessionsLoadedMsg{Sessions: sessions, Hosts: hs, Err: err, At: time.Now()}
	}
}

func (a App) refreshProjectsCmd() tea.Cmd {
	root := a.cfg.Projects.Root
	return func() tea.Msg {
		ps, err := project.Discover(root)
		return projectsLoadedMsg{Projects: ps, Err: err}
	}
}

// attachSelectedSession is Enter on Sessions screen.
func (a App) attachSelectedSession() tea.Cmd {
	sel := a.sessionsM.Selected()
	if sel == nil {
		return nil
	}
	if sel.Host == "" || sel.Host == "local" {
		return tea.ExecProcess(
			exec.Command("tmux", "attach-session", "-d", "-t", sel.Name),
			func(err error) tea.Msg {
				if err != nil {
					return toastMsg{Text: "tmux: " + err.Error(), Kind: toastError, Until: time.Now().Add(5 * time.Second)}
				}
				return refreshAfterDetachMsg{}
			},
		)
	}
	var h *config.Host
	for i := range a.cfg.Hosts {
		if a.cfg.Hosts[i].Name == sel.Host {
			h = &a.cfg.Hosts[i]
			break
		}
	}
	if h == nil {
		return func() tea.Msg {
			return toastMsg{Text: "no host config for " + sel.Host, Kind: toastError, Until: time.Now().Add(5 * time.Second)}
		}
	}
	target := h.Address
	if h.User != "" {
		target = h.User + "@" + h.Address
	}
	bin := "mosh"
	if !h.Mosh {
		bin = "ssh"
	}
	return tea.ExecProcess(
		exec.Command(bin, target, "--", "tmux", "attach-session", "-d", "-t", sel.Name),
		func(err error) tea.Msg {
			return refreshAfterDetachMsg{}
		},
	)
}

// attachOrCreateForSelectedProject is Enter on Projects screen: attach to
// the project's existing Claude session, or create one + attach.
func (a App) attachOrCreateForSelectedProject() tea.Cmd {
	if len(a.projects) == 0 || a.projectsM.cursor < 0 || a.projectsM.cursor >= len(a.projects) {
		return nil
	}
	p := a.projects[a.projectsM.cursor]
	session := p.SessionName()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		has, _ := tmux.Has(ctx, session)
		if !has {
			if err := tmux.New(ctx, session, p.Path, `claude --continue || claude || zsh`); err != nil {
				return toastMsg{Text: "start session: " + err.Error(), Kind: toastError, Until: time.Now().Add(5 * time.Second)}
			}
		}
		return projectSessionReadyMsg{Session: session}
	}
}

func fallbackDirectTmux(ctx context.Context) ([]daemon.SessionState, error) {
	tss, err := tmux.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]daemon.SessionState, 0, len(tss))
	for _, ts := range tss {
		out = append(out, daemon.SessionState{
			Name: ts.Name, Host: "local", Path: ts.Path,
			Attached: ts.Attached, Windows: ts.Windows,
			Created: ts.Created, LastChange: ts.LastAttach,
			State: string(claude.StateUnknown),
		})
	}
	return out, nil
}

func daemonOnline(hs []hostStatus) bool {
	for _, h := range hs {
		if h.Name == "local" && h.OK {
			return true
		}
	}
	return false
}

func statePriority(s string) int {
	switch s {
	case string(claude.StateNeedsInput):
		return 0
	case string(claude.StateActive):
		return 1
	case string(claude.StateIdle):
		return 2
	case string(claude.StateError):
		return 3
	default:
		return 4
	}
}

// refreshAfterDetachMsg fires after the TUI resumes from tmux attach;
// triggers fresh data load so the screen is current.
type refreshAfterDetachMsg struct{}
