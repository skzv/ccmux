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
	"github.com/skzv/ccmux/internal/claudeauth"
	"github.com/skzv/ccmux/internal/claudeusage"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/moshi"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tmux"
	"github.com/skzv/ccmux/internal/tmuxchrome"
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
	toastLog   []toastEntry // small ring buffer for the help overlay

	helpOpen     bool
	lastRefresh  time.Time
	daemonOnline bool
}

// New constructs the root model.
//
// Side effect: if the user's config has subscription.tier == "api" or
// empty, and `claude auth status` reports an actual paid plan, we adopt
// the detected tier for this process's lifetime. We do NOT write the
// adopted value to disk — the user's explicit override (if they ever
// set one) always wins on next launch.
func New(cfg config.Config, version string) App {
	st := styles.Default()
	km := DefaultKeymap()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if authStat, err := claudeauth.Get(ctx); err == nil {
		detected := authStat.Tier()
		// Only adopt a detected tier when the user hasn't explicitly
		// declared one. "api" is the default-empty marker.
		if (cfg.Subscription.Tier == "" || cfg.Subscription.Tier == "api") && detected != "api" {
			cfg.Subscription.Tier = detected
		}
	}

	a := App{
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
	a.dashboard.SetConfig(cfg)
	return a
}

// Init is called once at startup.
func (a App) Init() tea.Cmd {
	return tea.Batch(
		a.refreshSessionsCmd(),
		a.refreshProjectsCmd(),
		a.refreshUsageCmd(),
		tickEvery(2*time.Second),
		usageTick(),
	)
}

// usageTick fires every 15s — claudeusage.Walk scans the transcript
// tree which can be several MB, so we don't want it on every 2s heart-
// beat. The dashboard happily shows the previous value while the next
// walk runs in the background.
func usageTick() tea.Cmd {
	return tea.Tick(15*time.Second, func(t time.Time) tea.Msg { return usageTickMsg{At: t} })
}

func (a App) refreshUsageCmd() tea.Cmd {
	return func() tea.Msg {
		// 5h matches Anthropic's subscription rolling-window. We pull
		// the full window once and let the dashboard derive sub-totals
		// for any tighter span it wants from the same Aggregate.
		agg, err := claudeusage.Walk(5 * time.Hour)
		if err != nil {
			return usageLoadedMsg{Err: err}
		}
		return usageLoadedMsg{Agg: agg}
	}
}

// Update routes messages to the active screen and handles global keys.
func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		return a, nil

	case tickMsg:
		return a, tea.Batch(a.refreshSessionsCmd(), tickEvery(2*time.Second))

	case usageTickMsg:
		return a, tea.Batch(a.refreshUsageCmd(), usageTick())

	case usageLoadedMsg:
		if msg.Err == nil && msg.Agg != nil {
			a.dashboard.SetUsage(msg.Agg)
		}
		return a, nil

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
			// Notes screen needs the full list for its project picker.
			a.notes.SetProjects(a.projects)
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

	case openEditorMsg:
		// A screen (Notes) asked the app to suspend and run $EDITOR.
		c := exec.Command(msg.Editor, msg.Path)
		return a, tea.ExecProcess(c, func(err error) tea.Msg {
			if err != nil {
				return toastMsg{Text: "editor: " + err.Error(), Kind: toastError, Until: time.Now().Add(5 * time.Second)}
			}
			return notesReloadMsg{}
		})

	case refreshAfterDetachMsg:
		// Returning from tmux attach.
		return a, tea.Batch(a.refreshSessionsCmd(), a.refreshProjectsCmd())

	case tea.KeyMsg:
		// Help overlay takes precedence — `?` or `esc` close it, every
		// other key passes through normally so muscle memory still works.
		if a.helpOpen {
			switch msg.String() {
			case "?", "esc":
				a.helpOpen = false
			}
			return a, nil
		}

		// Esc dismisses the current toast (when no modal is open). The
		// projects-screen modal handles esc itself before this code runs.
		if msg.String() == "esc" && a.toast != "" && time.Now().Before(a.toastUntil) &&
			!(a.screen == ScreenProjects && a.projectsM.form != nil) {
			a.toast = ""
			return a, nil
		}

		// `?` opens the help overlay from any screen.
		if msg.String() == "?" {
			a.helpOpen = true
			return a, nil
		}

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
			// Propagate the currently-focused project from Projects.
			if len(a.projects) > 0 && a.projectsM.cursor >= 0 && a.projectsM.cursor < len(a.projects) {
				p := a.projects[a.projectsM.cursor]
				a.notes.SetProject(&p)
			}
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

	// Measure each chrome row's actual rendered height so we never
	// budget too generously and let body content push the header off
	// the top. lipgloss.Height counts \n's + 1 so it includes any
	// invisible line breaks even if forceSingleLine didn't get them.
	chromeH := lipgloss.Height(header) + lipgloss.Height(statusBar) + lipgloss.Height(footer)
	bodyHeight := a.height - chromeH
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
	// Defensive clamp: regardless of what the screen returned, never
	// let the body exceed its budget. Screens with content that's hard
	// to size deterministically (single-pane screens whose Lipgloss
	// .Height is a minimum, viewport-based screens with internal padding)
	// can sometimes overshoot by a line; we'd rather lose a trailing
	// empty line of body than have the header scroll off the top.
	body = clampLines(body, bodyHeight)

	frame := lipgloss.JoinVertical(lipgloss.Left, header, body, statusBar, footer)
	if a.helpOpen {
		return a.renderHelpOverlay(a.width, a.height)
	}
	return frame
}

// clampLines returns the first `n` lines of `s` verbatim. Preserves the
// internal newline format. Returns `s` unchanged if it already fits.
func clampLines(s string, n int) string {
	if n <= 0 {
		return ""
	}
	count := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			count++
			if count == n {
				return s[:i]
			}
		}
	}
	return s
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
	// Surface the running binary's version on the right of the status
	// bar so screenshots of a bug immediately reveal which build was in
	// play. Dirty builds (`<sha>-dirty`) call themselves out so we
	// remember we're running uncommitted code.
	versionChip := a.styles.Muted.Render("v" + a.version)
	if strings.Contains(a.version, "dirty") {
		versionChip = a.styles.StatusWarning.Render(a.version)
	}
	right := a.styles.Muted.Render(fmt.Sprintf("%s • %s", count, refreshed)) + "  " + versionChip

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
	if kind == toastError && ttl < 8*time.Second {
		// Errors are easy to blink past — give them longer than info
		// toasts by default, even when the caller asked for a short ttl.
		ttl = 8 * time.Second
	}
	a.toastUntil = time.Now().Add(ttl)
	// Append to the ring buffer (cap 10). The help overlay shows these
	// in reverse-chronological order.
	a.toastLog = append([]toastEntry{{At: time.Now(), Kind: kind, Text: text}}, a.toastLog...)
	if len(a.toastLog) > 10 {
		a.toastLog = a.toastLog[:10]
	}
	if dbg := debugLogger(); dbg != nil {
		dbg.Printf("toast[%d] %s", kind, text)
	}
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
//
// Three behaviors:
//   1. Local session, we're NOT inside tmux → exec `tmux attach-session`,
//      Bubble Tea is suspended until the user detaches.
//   2. Local session, we ARE inside tmux ($TMUX set, e.g. when running
//      from inside the outer "ccmux" tmux session on mobile) → call
//      `tmux switch-client -t <name>` which doesn't nest sessions and
//      lets `prefix L` jump back to ccmux.
//   3. Remote session → exec `mosh <host> -- tmux attach -t <name>`.
//
// Before any of these, we apply ccmux's chrome (custom status bar) to
// the target session so the attached view shows project name + detach
// hint + Moshi reachability indicator.
func (a App) attachSelectedSession() tea.Cmd {
	sel := a.sessionsM.Selected()
	if sel == nil {
		return nil
	}

	// Apply chrome to local sessions only — we don't have a tmux socket
	// on the remote host from here.
	if sel.Host == "" || sel.Host == "local" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		mst := moshi.Detect(ctx)
		_ = tmuxchrome.Apply(ctx, sel.Name, sel.Project,
			mst.Paired && mst.Connected,
			tmuxchrome.InTmux(), // nested?
		)
	}

	if sel.Host == "" || sel.Host == "local" {
		if tmuxchrome.InTmux() {
			// Already inside tmux (the persistent outer "ccmux" session
			// when connected via Moshi). Use switch-client to avoid
			// nesting.
			c := exec.Command("tmux", "switch-client", "-t", sel.Name)
			return tea.ExecProcess(c, func(err error) tea.Msg {
				if err != nil {
					return toastMsg{Text: "tmux switch-client: " + err.Error(), Kind: toastError, Until: time.Now().Add(5 * time.Second)}
				}
				return refreshAfterDetachMsg{}
			})
		}
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
