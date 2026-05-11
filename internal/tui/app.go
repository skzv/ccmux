// Package tui is the Bubble Tea root model and screen router for ccmux.
package tui

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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

// App is the root Bubble Tea model. It owns the active screen, the global
// data caches (sessions, projects, hosts), and the layout chrome (status bar,
// help footer, toast).
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

	toast   string
	toastUntil time.Time

	loading      bool
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

// Init is called once at startup. We kick off the first refresh and the
// periodic tick.
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
			a.toast = "refresh error: " + msg.Err.Error()
			a.toastUntil = time.Now().Add(5 * time.Second)
		}
		return a, nil

	case projectsLoadedMsg:
		if msg.Err == nil {
			a.projects = msg.Projects
			a.projectsM.SetProjects(a.projects)
		}
		return a, nil

	case tea.KeyMsg:
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
	footer := a.renderFooter()
	statusBar := a.renderStatusBar()

	// Reserve lines for header (1), status bar (1), footer (1), and a blank
	// separator (1). The body fills whatever's left.
	bodyHeight := a.height - 4
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

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		body,
		statusBar,
		footer,
	)
}

// renderHeader is the top-of-screen tab strip.
func (a App) renderHeader() string {
	tabs := []Screen{ScreenDashboard, ScreenSessions, ScreenProjects, ScreenNotes, ScreenClaude, ScreenSettings}
	parts := make([]string, 0, len(tabs)+1)
	parts = append(parts, a.styles.Title.Render(" ccmux "))
	for i, t := range tabs {
		label := fmt.Sprintf("[%d] %s", i+1, t.String())
		if t == a.screen {
			parts = append(parts, a.styles.Emphasis.Render(" "+label+" "))
		} else {
			parts = append(parts, a.styles.Muted.Render(" "+label+" "))
		}
	}
	return lipgloss.NewStyle().Background(a.styles.P.BGAlt).Width(a.width).Render(strings.Join(parts, ""))
}

// renderStatusBar is the bottom-most informational strip.
func (a App) renderStatusBar() string {
	host, _ := os.Hostname()
	left := a.styles.HostColor("local").Render("● " + host)

	daemon := a.styles.StatusError.Render("⚠ daemon offline")
	if a.daemonOnline {
		daemon = a.styles.StatusGood.Render("✓ daemon online")
	}

	count := fmt.Sprintf("%d session(s)", len(a.sessions))
	refreshed := "never"
	if !a.lastRefresh.IsZero() {
		refreshed = a.lastRefresh.Format("15:04:05")
	}

	right := a.styles.Muted.Render(fmt.Sprintf("%s • refreshed %s", count, refreshed))

	mid := daemon
	if a.cfg.Sleep.DangerousKeepAwakeOnBattery {
		mid = a.styles.StatusDanger.Render("⚠ DANGEROUS BATTERY MODE") + " " + daemon
	}

	body := left + "  " + mid + lipgloss.PlaceHorizontal(
		a.width-lipgloss.Width(left)-lipgloss.Width(mid)-2,
		lipgloss.Right,
		right,
	)
	return a.styles.StatusBar.Width(a.width).Render(body)
}

// renderFooter is the help line. Single-row.
func (a App) renderFooter() string {
	hint := "1-6 screens • r refresh • ? help • q quit"
	if a.toast != "" && time.Now().Before(a.toastUntil) {
		return a.styles.Toast.Render(a.toast)
	}
	return a.styles.Muted.Render(hint)
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

		// Local.
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
				// Daemon unreachable — fall back to direct tmux.
				direct, e2 := fallbackDirectTmux(ctx)
				if e2 == nil {
					sessions = append(sessions, direct...)
					hs = append(hs, hostStatus{Name: "local", Address: "tmux (no daemon)", OK: false})
				} else {
					err = fmt.Errorf("local: %w", e2)
				}
			}
		}

		// Remotes.
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
			// Sort: needs-input first, then active, then idle, then by host then name.
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

// attachSelectedSession is what Enter does on the Sessions screen.
// For local sessions: suspend the TUI and exec `tmux attach`. For remote
// sessions: exec `mosh <host> -- tmux attach -t <session>`.
//
// Both replace the current process; on detach we exit. Re-launching ccmux
// puts the user back in the same screen.
func (a App) attachSelectedSession() tea.Cmd {
	sel := a.sessionsM.Selected()
	if sel == nil {
		return nil
	}
	if sel.Host == "" || sel.Host == "local" {
		return tea.ExecProcess(
			cmdFor("tmux", "attach-session", "-d", "-t", sel.Name),
			func(err error) tea.Msg {
				if err != nil {
					return toastMsg{Text: "tmux: " + err.Error(), Kind: toastError, Until: time.Now().Add(5 * time.Second)}
				}
				return nil
			},
		)
	}
	// Remote: find the host config to pick mosh user/address.
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
		cmdFor(bin, target, "--", "tmux", "attach-session", "-d", "-t", sel.Name),
		nil,
	)
}

// fallbackDirectTmux runs `tmux list-sessions` and converts to SessionState
// when the daemon isn't responding. We lose daemon-only fields (state,
// prompt count, idle); the dashboard shows them as "unknown."
func fallbackDirectTmux(ctx context.Context) ([]daemon.SessionState, error) {
	tss, err := tmux.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]daemon.SessionState, 0, len(tss))
	for _, ts := range tss {
		out = append(out, daemon.SessionState{
			Name:       ts.Name,
			Host:       "local",
			Path:       ts.Path,
			Attached:   ts.Attached,
			Windows:    ts.Windows,
			Created:    ts.Created,
			LastChange: ts.LastAttach,
			State:      string(claude.StateUnknown),
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
