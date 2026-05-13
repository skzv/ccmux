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
	"github.com/skzv/ccmux/internal/tailnet"
	"github.com/skzv/ccmux/internal/tmux"
	"github.com/skzv/ccmux/internal/tmuxchrome"
	"github.com/skzv/ccmux/internal/tui/styles"
	"github.com/skzv/ccmux/internal/usage"
)

// Screen identifies which top-level screen is currently focused.
type Screen int

const (
	ScreenDashboard Screen = iota
	ScreenSessions
	ScreenProjects
	ScreenNotes
	ScreenAgents
	ScreenSettings
	ScreenNetwork
)

func (s Screen) String() string {
	// "Agents" was "Claude" before Codex / Gemini joined. The label
	// drives the top tab bar AND the contextual help footer, so a
	// rename here is the canonical place to keep them in sync.
	return []string{"Dashboard", "Sessions", "Projects", "Notes", "Agents", "Settings", "Network"}[s]
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
	agentsM   agentsModel
	settings  settingsModel
	network   networkModel

	toast      string
	toastKind  toastKind
	toastUntil time.Time
	toastLog   []toastEntry // small ring buffer for the help overlay

	helpOpen     bool
	tour         tourModel // first-run interactive tour; re-openable with T
	lastRefresh  time.Time
	daemonOnline bool

	// Easter egg: pressing M (shift-M) opens the Matrix overlay.
	// Consistent with T which reopens the tour.
	matrix matrixModel
}

// modalCapturingText returns true when the App is in a state where
// keystrokes are going into a text field, so the matrix easter egg
// must NOT capture them. Reported case: typing a session name like
// "matrix-experiment" in the new-session form fired the overlay.
// Listed states: the new-project / new-session form modals, the
// notes search bar, the tour, the help overlay.
func (a App) modalCapturingText() bool {
	if a.tour.Active() || a.helpOpen {
		return true
	}
	if a.projectsM.form != nil || a.sessionsM.form != nil {
		return true
	}
	if a.notes.searching {
		return true
	}
	return false
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
		agentsM:   newAgents(st, km),
		settings:  newSettings(st, km, cfg, version),
		network:   newNetwork(st, km),
		tour:      newTour(st),
		matrix:    newMatrix(),
	}
	a.dashboard.SetConfig(cfg)
	a.dashboard.SetVersion(version)
	// First-run tour: open automatically if the user hasn't completed it yet.
	if !cfg.Tour.Shown {
		a.tour.Open()
	}
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
		// the full window once for Claude (the rich panel uses every
		// field) and the same for Codex/Gemini (their summaries are
		// today always zero — stub walkers, see internal/usage).
		const window = 5 * time.Hour
		agg, claudeErr := claudeusage.Walk(window)
		codex, _ := usage.WalkCodex(window)
		gemini, _ := usage.WalkGemini(window)
		// Only surface a top-level Err on Claude failure — Codex/Gemini
		// walkers stub out today, and once real, a transient parse
		// error for one of them shouldn't blank the whole panel.
		return usageLoadedMsg{Agg: agg, Codex: codex, Gemini: gemini, Err: claudeErr}
	}
}

// Update routes messages to the active screen and handles global keys.
func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		a.matrix.SetSize(msg.Width, msg.Height)
		return a, nil

	case matrixTickMsg:
		// Drive the easter-egg animation. When the matrix has been
		// dismissed since the last frame, swallow the message without
		// scheduling another tick.
		if !a.matrix.Active() {
			return a, nil
		}
		var cmd tea.Cmd
		a.matrix, cmd = a.matrix.Update(msg)
		return a, cmd

	case tickMsg:
		return a, tea.Batch(a.refreshSessionsCmd(), tickEvery(2*time.Second))

	case usageTickMsg:
		return a, tea.Batch(a.refreshUsageCmd(), usageTick())

	case usageLoadedMsg:
		if msg.Err == nil && msg.Agg != nil {
			a.dashboard.SetUsage(msg.Agg)
		}
		// Codex and Gemini summaries are pushed unconditionally —
		// today they're always empty (stub walkers) but the dashboard
		// uses HasData to decide between "real numbers" and "install
		// hint" rendering.
		a.dashboard.SetCodexUsage(msg.Codex)
		a.dashboard.SetGeminiUsage(msg.Gemini)
		return a, nil

	case sessionsLoadedMsg:
		a.lastRefresh = msg.At
		a.sessions = msg.Sessions
		a.hosts = msg.Hosts
		a.daemonOnline = daemonOnline(msg.Hosts)
		a.dashboard.SetSessions(a.sessions)
		a.dashboard.SetHosts(a.hosts)
		a.network.SetHosts(a.hosts)
		a.projectsM.SetHosts(a.hosts)
		a.sessionsM.SetHosts(a.hosts)
		a.sessionsM.SetDefaultDir(a.cfg.Sessions.DefaultDir)
		a.dashboard.SetVersion(a.version)
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

	case remoteSessionStartedMsg:
		// Remote daemon already created the tmux session for us;
		// reuse the same ssh-into-peer path the Sessions screen uses
		// for discovered hosts (PATH prepend + login shell, etc.).
		if msg.DialHost == "" {
			a.setToast(toastError, "remote session created but no dial host known", 5*time.Second)
			return a, nil
		}
		target := msg.DialHost
		if msg.User != "" {
			target = msg.User + "@" + msg.DialHost
		}
		var c *exec.Cmd
		if msg.Mosh {
			remoteCmd := remoteTmuxAttach(msg.SessionName)
			c = exec.Command("mosh", target, "--", "bash", "-c", remoteCmd)
			if dbg := debugLogger(); dbg != nil {
				dbg.Printf("remote attach: mosh %s -- bash -c %q", target, remoteCmd)
			}
		} else {
			remoteCmd := remoteTmuxAttach(msg.SessionName)
			c = exec.Command("ssh", "-t", target, remoteCmd)
			if dbg := debugLogger(); dbg != nil {
				dbg.Printf("remote attach: ssh -t %s %q", target, remoteCmd)
			}
		}
		return a, tea.ExecProcess(c, func(err error) tea.Msg {
			return refreshAfterDetachMsg{}
		})

	case newBareSessionSubmitMsg:
		// Close the form immediately — the form's own sessionsModel.Update
		// never runs this message (the type-switch in App.Update intercepts
		// it first), so we must nil it here.
		a.sessionsM.form = nil
		// Route to the right host: local → tmux.New + localAttachCmd;
		// remote → POST /v1/sessions/bare → ssh-attach via remoteSessionStarted.
		return a, spawnBareSessionCmd(msg)

	case bareSessionReadyMsg:
		// Local-bare-session creation finished. Attach via the same
		// path the new-project flow uses, so the nested-tmux case
		// (running ccmux inside the outer ccmux on mobile) handles
		// switch-client correctly. projectLabel is the session name
		// here — bare sessions don't have a richer label.
		return a, a.localAttachCmd(msg.Session, msg.Session)

	case projectSessionReadyMsg:
		// New project is scaffolded and its tmux session is running with
		// the initial prompt sent. Route through localAttachCmd so the
		// nested-tmux case (ccmux running inside the outer ccmux session
		// on mobile) uses switch-client instead of attach-session —
		// otherwise tmux refuses the nested attach and the user just
		// stares at the Projects screen wondering why nothing happened.
		return a, a.localAttachCmd(msg.Session, msg.Project)

	case sessionKilledMsg:
		if msg.Err != nil {
			a.setToast(toastError, "kill failed: "+msg.Err.Error(), 5*time.Second)
		} else {
			a.setToast(toastSuccess, "killed "+msg.Name, 3*time.Second)
		}
		return a, a.refreshSessionsCmd()

	case openEditorMsg:
		// A screen (Notes or Settings) asked the app to suspend and run
		// $EDITOR. Route the follow-up reload by Source so the right
		// screen refreshes when control returns.
		source := msg.Source
		c := exec.Command(msg.Editor, msg.Path)
		return a, tea.ExecProcess(c, func(err error) tea.Msg {
			if err != nil {
				return toastMsg{Text: "editor: " + err.Error(), Kind: toastError, Until: time.Now().Add(5 * time.Second)}
			}
			if source == "settings" {
				return configReloadMsg{}
			}
			return notesReloadMsg{}
		})

	case configReloadMsg:
		// User finished editing ~/.config/ccmux/config.toml in $EDITOR.
		// Re-read it and push the new shape into every screen that
		// holds a cached copy. Errors surface as a toast — the previous
		// in-memory config stays in place so the TUI doesn't go blank.
		if cfg, err := config.Load(); err != nil {
			a.setToast(toastError, "reload config: "+err.Error(), 5*time.Second)
		} else {
			a.cfg = cfg
			a.settings.SetConfig(cfg)
			a.dashboard.SetConfig(cfg)
			a.setToast(toastSuccess, "config reloaded", 2*time.Second)
		}
		return a, nil

	case refreshAfterDetachMsg:
		// Returning from tmux attach.
		return a, tea.Batch(a.refreshSessionsCmd(), a.refreshProjectsCmd())

	case tea.KeyMsg:
		// Matrix easter egg takes priority — when active, the overlay
		// owns the screen until Esc dismisses it. Routed before the
		// tour so triggering the rain while the tour is on doesn't
		// produce a confused split state.
		if a.matrix.Active() {
			var cmd tea.Cmd
			a.matrix, cmd = a.matrix.Update(msg)
			return a, cmd
		}
		// M (shift-M) opens the Matrix overlay from any navigation surface,
		// mirroring T which reopens the tour. Suppressed when a text-input
		// modal has focus so a session named "My-project" doesn't hijack.
		if msg.String() == "M" && !a.modalCapturingText() {
			a.matrix.Open()
			a.matrix.SetSize(a.width, a.height)
			return a, matrixTick()
		}

		// Tour overlay takes top priority. The tour owns the screen
		// until the user finishes it or skips with esc/q. Re-openable
		// later with `T`.
		if a.tour.Active() {
			switch msg.String() {
			case "right", "enter", " ", "n":
				if !a.tour.Next() {
					// Last slide → mark complete + close.
					a.tour.Close()
					a.markTourShown()
				}
			case "left", "p":
				a.tour.Prev()
			case "esc", "q":
				a.tour.Close()
				a.markTourShown()
			}
			return a, nil
		}

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

		// `T` re-opens the first-run tour at step 0. Capital so it doesn't
		// collide with vim-style `t` someone might add to a per-screen
		// nav binding later.
		if msg.String() == "T" {
			a.tour.Open()
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

		// Same modal-routing for the Sessions tab's new-bare-session
		// form. Without this, the global Enter handler below intercepts
		// the form's submit key and attaches to whatever session the
		// cursor was on — observed as "Enter in the new-session form
		// attaches to c-ccmux instead of creating a new session".
		if a.screen == ScreenSessions && a.sessionsM.form != nil {
			if msg.String() == "ctrl+c" {
				return a, tea.Quit
			}
			var cmd tea.Cmd
			a.sessionsM, cmd = a.sessionsM.Update(msg)
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
			a.screen = ScreenAgents
			return a, nil
		case keyMatches(msg, a.keys.Settings):
			a.screen = ScreenSettings
			return a, nil
		case keyMatches(msg, a.keys.Network):
			a.screen = ScreenNetwork
			return a, nil
		case keyMatches(msg, a.keys.Refresh):
			return a, a.refreshSessionsCmd()
		case keyMatches(msg, a.keys.Enter) && a.screen == ScreenSessions:
			return a, a.attachSelectedSession()
		case keyMatches(msg, a.keys.Enter) && a.screen == ScreenProjects:
			return a, a.attachOrCreateForSelectedProject()
		case keyMatches(msg, a.keys.Enter) && a.screen == ScreenNetwork:
			if c := a.network.SSHCmd(); c != nil {
				return a, c
			}
			return a, func() tea.Msg {
				return toastMsg{Text: "nothing to ssh into for that row", Kind: toastInfo, Until: time.Now().Add(3 * time.Second)}
			}
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
	case ScreenAgents:
		a.agentsM, cmd = a.agentsM.Update(msg)
	case ScreenSettings:
		a.settings, cmd = a.settings.Update(msg)
	case ScreenNetwork:
		a.network, cmd = a.network.Update(msg)
	}
	return a, cmd
}

// View renders the whole UI.
func (a App) View() string {
	if a.width == 0 {
		return "loading…"
	}
	// Matrix easter egg takes the full screen when active — placed
	// before the chrome render so we don't waste work building the
	// header/footer just to throw them away.
	if a.matrix.Active() {
		return a.matrix.View(a.width, a.height)
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
	case ScreenAgents:
		body = a.agentsM.View(a.width, bodyHeight)
	case ScreenSettings:
		body = a.settings.View(a.width, bodyHeight)
	case ScreenNetwork:
		body = a.network.View(a.width, bodyHeight)
	}
	// Defensive clamp: regardless of what the screen returned, never
	// let the body exceed its budget. Screens with content that's hard
	// to size deterministically (single-pane screens whose Lipgloss
	// .Height is a minimum, viewport-based screens with internal padding)
	// can sometimes overshoot by a line; we'd rather lose a trailing
	// empty line of body than have the header scroll off the top.
	body = clampLines(body, bodyHeight)

	frame := lipgloss.JoinVertical(lipgloss.Left, header, body, statusBar, footer)
	// Overlay precedence: tour > help > regular frame.
	if a.tour.Active() {
		return a.tour.View(a.width, a.height)
	}
	if a.helpOpen {
		return a.renderHelpOverlay(a.width, a.height)
	}
	return frame
}

// markTourShown persists Tour.Shown=true so the tour doesn't re-fire on
// next launch. Errors are swallowed deliberately — the worst case is
// the user sees the tour twice, which is harmless. We don't want a
// config-write blip to interrupt the TUI's flow.
func (a *App) markTourShown() {
	if a.cfg.Tour.Shown && a.cfg.Tour.ShownVersion == a.version {
		return
	}
	a.cfg.Tour.Shown = true
	a.cfg.Tour.ShownVersion = a.version
	_ = config.Save(a.cfg)
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
	tabs := []Screen{ScreenDashboard, ScreenSessions, ScreenProjects, ScreenNotes, ScreenAgents, ScreenSettings, ScreenNetwork}
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

// refreshSessionsCmd fetches sessions from local ccmuxd, every
// explicitly-configured remote host, AND every tailnet peer auto-
// discovered via `tailscale status` + a /v1/health probe. Falls back
// to direct tmux call when the local daemon is down.
func (a App) refreshSessionsCmd() tea.Cmd {
	hosts := a.cfg.Hosts
	tailnetPort := a.cfg.Daemon.TailnetPort
	if tailnetPort == 0 {
		tailnetPort = 7474
	}
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
				localName := shortHostname(h.Hostname)
				if localName == "" {
					localName = "local"
				}
				hs = append(hs, hostStatus{
					Name:      localName,
					Local:     true,
					Address:   local.Addr(),
					OK:        h.OK,
					Sessions:  h.Sessions,
					SleepMode: h.SleepMode,
					Version:   h.Version,
				})
			} else {
				direct, e2 := fallbackDirectTmux(ctx)
				if e2 == nil {
					sessions = append(sessions, direct...)
					localHost, _ := os.Hostname()
					name := shortHostname(localHost)
					if name == "" {
						name = "local"
					}
					// tmux is responding — sessions came back. ccmuxd
					// is down, but the device itself is fine; mark OK
					// so the dot stays green. The "(no daemon)"
					// address is the only visible breadcrumb that
					// something's off — the user can `ccmux daemon
					// install` to fix.
					hs = append(hs, hostStatus{
						Name:    name,
						Local:   true,
						Address: "tmux (no daemon)",
						OK:      true,
					})
				} else {
					err = fmt.Errorf("local: %w", e2)
				}
			}
		}

		// Configured hosts. Tracked so we don't double-add a peer that's
		// both explicitly configured AND auto-discovered.
		seen := map[string]bool{}
		for _, h := range hosts {
			addr := h.Address
			if h.Port == 0 {
				addr += ":7474"
			} else {
				addr += fmt.Sprintf(":%d", h.Port)
			}
			seen[addr] = true
			cli := daemon.RemoteClient(addr)
			ss, e := cli.Sessions(ctx)
			st := hostStatus{
				Name:     h.Name,
				Address:  addr,
				DialHost: h.Address, // bare address without port, for ssh/mosh
				User:     h.User,
				Mosh:     h.Mosh,
			}
			if e == nil {
				st.OK = true
				st.Sessions = len(ss)
				for i := range ss {
					ss[i].Host = h.Name
				}
				sessions = append(sessions, ss...)
				if hi, hErr := cli.Health(ctx); hErr == nil {
					st.Version = hi.Version
				}
			} else {
				st.Err = e
			}
			hs = append(hs, st)
		}

		// Tailnet auto-discovery. ScanTailnet probes every online
		// non-mobile peer for ccmuxd /v1/health and partitions:
		//   - Reachable: ccmuxd answered → merge as a regular host.
		//   - NeedsInstall: peer is up but didn't answer → surface
		//     with a "ccmux not installed / running here" hint so
		//     the user knows what to do.
		// Mobile peers (iOS, iPadOS, Android) are skipped entirely
		// because the Moshi app handles them, and installing ccmux
		// there isn't an option.
		// Errors are non-fatal — discovery is convenience.
		if scan, derr := tailnet.ScanTailnet(ctx, tailnetPort); derr == nil {
			for _, d := range scan.Reachable {
				if seen[d.Address] {
					continue
				}
				seen[d.Address] = true
				cli := daemon.RemoteClient(d.Address)
				// The probe already succeeded (that's how this peer
				// ended up in Reachable). Mark OK regardless of the
				// follow-up Sessions call — a Sessions error means
				// "couldn't list sessions right now," not "host is
				// down," so we shouldn't make the dot red.
				st := hostStatus{
					Name: d.Name, Address: d.Address,
					Discovered: true, DialHost: d.DialHost,
					Version: d.Version, OK: true,
				}
				if ss, e := cli.Sessions(ctx); e == nil {
					st.Sessions = len(ss)
					for i := range ss {
						ss[i].Host = d.Name
					}
					sessions = append(sessions, ss...)
				} else {
					st.Err = e
				}
				hs = append(hs, st)
			}
			for _, p := range scan.NeedsInstall {
				addr := fmt.Sprintf("%s:%d", p.Addr, tailnetPort)
				if seen[addr] {
					continue
				}
				seen[addr] = true
				hs = append(hs, hostStatus{
					Name:         shortPeerName(p.DisplayName()),
					Address:      addr,
					Discovered:   true,
					NeedsInstall: true,
					OS:           p.OS,
					OK:           p.Online,
				})
			}
			for _, p := range scan.Mobile {
				// Mobile rows don't have an ccmuxd address; key the
				// dedupe by the tailnet IP itself so the same phone
				// doesn't show twice across refreshes.
				key := "mobile://" + p.Addr
				if seen[key] {
					continue
				}
				seen[key] = true
				hs = append(hs, hostStatus{
					Name:       shortPeerName(p.DisplayName()),
					Address:    p.Addr,
					Discovered: true,
					Mobile:     true,
					OS:         p.OS,
					OK:         p.Online,
				})
			}
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
	hosts := a.cfg.Hosts
	tailnetPort := a.cfg.Daemon.TailnetPort
	if tailnetPort == 0 {
		tailnetPort = 7474
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var all []project.Project
		// Local projects first so the merge sort keeps them
		// grouped naturally by Modified after combining.
		if ps, err := project.Discover(root); err == nil {
			for _, p := range ps {
				p.Host = "local"
				all = append(all, p)
			}
		}

		// Configured remote hosts.
		seen := map[string]bool{}
		for _, h := range hosts {
			addr := h.Address
			if h.Port == 0 {
				addr += ":7474"
			} else {
				addr += fmt.Sprintf(":%d", h.Port)
			}
			seen[addr] = true
			all = appendRemoteProjects(ctx, all, addr, h.Name)
		}

		// Auto-discovered tailnet peers. The scan inside ScanTailnet
		// runs its own concurrent probes, so this is cheap relative
		// to the per-host project fetches that follow.
		if scan, err := tailnet.ScanTailnet(ctx, tailnetPort); err == nil {
			for _, d := range scan.Reachable {
				if seen[d.Address] {
					continue
				}
				seen[d.Address] = true
				all = appendRemoteProjects(ctx, all, d.Address, d.Name)
			}
		}

		sort.SliceStable(all, func(i, j int) bool {
			hi, hj := projectHost(all[i]), projectHost(all[j])
			if hi != hj {
				if hi == "local" {
					return true
				}
				if hj == "local" {
					return false
				}
				return hi < hj
			}
			return all[i].Modified.After(all[j].Modified)
		})
		return projectsLoadedMsg{Projects: all}
	}
}

// appendRemoteProjects fetches projects from one remote ccmuxd at
// `addr` and tags each entry with `hostLabel` (the dashboard's
// friendly name for that host). Failures are silently swallowed —
// project discovery is best-effort, and a single unreachable peer
// shouldn't drop the user's local list.
func appendRemoteProjects(ctx context.Context, into []project.Project, addr, hostLabel string) []project.Project {
	cli := daemon.RemoteClient(addr)
	infos, err := cli.Projects(ctx)
	if err != nil {
		return into
	}
	for _, p := range infos {
		into = append(into, project.Project{
			Name: p.Name, Host: hostLabel, Path: p.Path,
			HasGit: p.HasGit, HasCM: p.HasCM, HasDocs: p.HasDocs,
			Modified: p.Modified,
		})
	}
	return into
}

// attachSelectedSession is Enter on Sessions screen.
//
// Three behaviors:
//  1. Local session, we're NOT inside tmux → exec `tmux attach-session`,
//     Bubble Tea is suspended until the user detaches.
//  2. Local session, we ARE inside tmux ($TMUX set, e.g. when running
//     from inside the outer "ccmux" tmux session on mobile) → call
//     `tmux switch-client -t <name>` which doesn't nest sessions and
//     lets `prefix L` jump back to ccmux.
//  3. Remote session → exec `mosh <host> -- tmux attach -t <name>`.
//
// Before any of these, we apply ccmux's chrome (custom status bar) to
// the target session so the attached view shows project name + detach
// hint + Moshi reachability indicator.
func (a App) attachSelectedSession() tea.Cmd {
	sel := a.sessionsM.Selected()
	if sel == nil {
		return nil
	}
	if dbg := debugLogger(); dbg != nil {
		names := make([]string, len(a.sessions))
		for i, s := range a.sessions {
			names[i] = s.Name
		}
		dbg.Printf("attachSelectedSession: cursor=%d name=%q all=%v",
			a.sessionsM.cursor, sel.Name, names)
	}

	// Local sessions resolved by the helper that handles the
	// nested-tmux case.
	for _, ls := range []string{"", "local"} {
		if sel.Host == ls {
			return a.localAttachCmd(sel.Name, sel.Project)
		}
	}
	// Also resolve to local when the host's name matches THIS
	// machine — auto-discovered local rows now use the hostname (e.g.
	// "sputnik") instead of the literal "local", so plain string
	// matching against "local" alone misses that case.
	if h := a.localHostStatus(); h != nil && h.Name == sel.Host {
		return a.localAttachCmd(sel.Name, sel.Project)
	}

	// Explicit cfg.Hosts entries carry full SSH/Mosh details.
	for i := range a.cfg.Hosts {
		if a.cfg.Hosts[i].Name == sel.Host {
			h := &a.cfg.Hosts[i]
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
				func(err error) tea.Msg { return refreshAfterDetachMsg{} },
			)
		}
	}

	// Auto-discovered tailnet peers don't appear in cfg.Hosts. Look
	// them up in the live a.hosts slice. Prefer the discovered
	// peer's DialHost (a MagicDNS short name) over the bare tailnet
	// IP so existing `known_hosts` entries match — dialing by IP
	// otherwise prompts the user to re-accept a fingerprint they've
	// already trusted.
	//
	// We default to ssh (not mosh) for discovered peers because mosh
	// requires `mosh-server` on the remote, which isn't guaranteed
	// just because Tailscale + ccmuxd are running. Users who want
	// mosh + roaming can pin the host with `ccmux host add --mosh`
	// to override.
	//
	// PATH handling: ssh runs the remote command via the user's
	// login shell in NON-LOGIN NON-INTERACTIVE mode, so /etc/profile
	// and ~/.zprofile/~/.zshrc don't run. Homebrew lives in
	// /opt/homebrew/bin on Apple Silicon and /usr/local/bin on
	// Intel — neither is in the default ssh PATH. We prepend both
	// inline so `tmux` resolves regardless of the remote user's
	// shell config. (An earlier `bash -lc` attempt didn't work
	// because most users put `eval $(brew shellenv)` in their zshrc,
	// not their zprofile.)
	for _, hs := range a.hosts {
		if hs.Name == sel.Host && hs.Discovered {
			dial := hs.DialHost
			if dial == "" {
				dial = dialAddrFor(hs)
			}
			if dial == "" {
				return func() tea.Msg {
					return toastMsg{Text: "no reachable address for " + sel.Host, Kind: toastError, Until: time.Now().Add(5 * time.Second)}
				}
			}
			remoteCmd := remoteTmuxAttach(sel.Name)
			cmd := exec.Command("ssh", "-t", dial, remoteCmd)
			if dbg := debugLogger(); dbg != nil {
				dbg.Printf("attach discovered: ssh -t %s %q", dial, remoteCmd)
			}
			return tea.ExecProcess(cmd, func(err error) tea.Msg {
				return refreshAfterDetachMsg{}
			})
		}
	}

	return func() tea.Msg {
		return toastMsg{Text: "no host config for " + sel.Host, Kind: toastError, Until: time.Now().Add(5 * time.Second)}
	}
}

// localHostStatus returns the hostStatus for THIS machine (if loaded
// yet). Used by attach to recognize that a session whose Host matches
// our hostname is actually local, not remote.
func (a App) localHostStatus() *hostStatus {
	for i := range a.hosts {
		if a.hosts[i].Local {
			return &a.hosts[i]
		}
	}
	return nil
}

// dialAddrFor extracts the bare host (no port) from a discovered
// peer's Address. Discovered Address is "<tailnet-ip>:<port>" — mosh
// wants just the host. Returns "" if there's nothing usable.
func dialAddrFor(hs hostStatus) string {
	if hs.Address == "" {
		return ""
	}
	if i := strings.LastIndex(hs.Address, ":"); i > 0 {
		return hs.Address[:i]
	}
	return hs.Address
}

// shellQuote escapes `s` for safe interpolation inside a POSIX
// single-quoted string. The remote attach builds a single command
// string (PATH=... tmux attach -t '<name>') that's passed to ssh,
// which executes it via the remote user's shell. The session name
// could in theory contain characters the shell would expand; quoting
// it defensively keeps a pathological project basename from
// breaking out. ccmux's own session names are tame (alphanumeric +
// dash + underscore from SessionNameForPath), but belt-and-suspenders.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// remoteTmuxAttach builds the single-string command we hand to ssh
// for attaching to a remote tmux session. ssh runs it via the user's
// shell in NON-LOGIN NON-INTERACTIVE mode, so /etc/profile + zshrc/
// zprofile don't fire and Homebrew-installed tmux isn't on PATH by
// default. The prepended paths cover the common install locations
// on both ends of the wire:
//
//	/opt/homebrew/bin                    — macOS Apple Silicon Homebrew
//	/usr/local/bin                       — macOS Intel Homebrew + Linux convention
//	/home/linuxbrew/.linuxbrew/bin       — Linuxbrew on Linux
//	/snap/bin                            — Snap-installed tmux on Linux
//
// Non-existent paths in the list are silently ignored by the shell,
// so this is safe to include unconditionally regardless of whether
// the dialer or the target is macOS or Linux. The trailing $PATH
// preserves whatever else the remote shell already had set up.
func remoteTmuxAttach(session string) string {
	return "PATH=/opt/homebrew/bin:/usr/local/bin:/home/linuxbrew/.linuxbrew/bin:/snap/bin:$PATH" +
		" tmux attach-session -d -t " + shellQuote(session)
}

// attachOrCreateForSelectedProject is Enter on Projects screen.
// Routes by Host:
//   - local (Host == "" or "local"): existing flow — tmux.New here,
//     localAttachCmd attaches.
//   - remote: POST /v1/sessions to that host's ccmuxd so the tmux
//     session is created on the remote, then ssh-attach into it
//     using the same dial path the Sessions screen uses.
func (a App) attachOrCreateForSelectedProject() tea.Cmd {
	if len(a.projects) == 0 || a.projectsM.cursor < 0 || a.projectsM.cursor >= len(a.projects) {
		return nil
	}
	p := a.projects[a.projectsM.cursor]
	host := projectHost(p)
	if host == "local" {
		return a.attachOrCreateLocal(p)
	}
	return a.attachOrCreateRemote(p, host)
}

// attachOrCreateLocal is the existing same-machine path: tmux.New
// if missing, then localAttachCmd (which routes nested-tmux too).
func (a App) attachOrCreateLocal(p project.Project) tea.Cmd {
	session := p.SessionName()
	label := p.Name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		has, _ := tmux.Has(ctx, session)
		if !has {
			if err := tmux.New(ctx, session, p.Path, `claude --continue || claude || zsh`); err != nil {
				return toastMsg{Text: "start session: " + err.Error(), Kind: toastError, Until: time.Now().Add(5 * time.Second)}
			}
		}
		return projectSessionReadyMsg{Session: session, Project: label}
	}
}

// attachOrCreateRemote starts (or attaches to) a Claude session on a
// remote ccmuxd, then ssh-attaches to it from this machine. Two
// network round-trips and the user gets dropped into a fully-running
// remote tmux session from a single Enter press.
func (a App) attachOrCreateRemote(p project.Project, host string) tea.Cmd {
	hs := a.lookupHostByName(host)
	if hs == nil {
		return func() tea.Msg {
			return toastMsg{Text: "no reachable daemon for host: " + host, Kind: toastError, Until: time.Now().Add(5 * time.Second)}
		}
	}
	// Snapshot the dial/address before crossing the goroutine boundary.
	hostAddr := hs.Address
	dial := hs.DialHost
	if dial == "" {
		dial = dialAddrFor(*hs)
	}
	projectName := p.Name
	return tea.Sequence(
		func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cli := daemon.RemoteClient(hostAddr)
			ss, err := cli.NewSession(ctx, daemon.NewSessionRequest{
				Project:  projectName,
				Continue: true,
			})
			if err != nil {
				return toastMsg{Text: "remote start: " + err.Error(), Kind: toastError, Until: time.Now().Add(6 * time.Second)}
			}
			return remoteSessionStartedMsg{SessionName: ss.Name, DialHost: dial}
		},
	)
}

// lookupHostByName returns the hostStatus row matching `name` (set
// by refresh). Used to convert a project's Host label back into the
// daemon address + ssh dial host pair.
func (a App) lookupHostByName(name string) *hostStatus {
	for i := range a.hosts {
		if a.hosts[i].Name == name {
			return &a.hosts[i]
		}
	}
	return nil
}

// localAttachCmd builds the tea.Cmd that suspends Bubble Tea, applies
// ccmux's chrome to the target session, then either switch-clients
// (when we're already inside the outer ccmux tmux session, the mobile
// flow) or attach-sessions (when we're in a bare terminal). One
// definition shared by the Sessions screen and the Projects screen
// so both paths handle the nested-tmux case identically — Projects
// previously always called attach-session, which silently failed
// inside the outer ccmux session.
func (a App) localAttachCmd(session, projectLabel string) tea.Cmd {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	mst := moshi.Detect(ctx)
	nested := tmuxchrome.InTmux()
	// Moshi badge is "reachable" when the whole pipeline is wired AND
	// running: paired with Moshi cloud, Claude Code hooks installed,
	// daemon up. Previously this AND'ed Connected, which was always
	// false because moshi-hook status doesn't expose live websocket
	// state — so the chrome read "phone: not paired" even on a fully
	// configured host.
	reachable := mst.Paired && mst.HooksInstalled && mst.ServiceRunning
	_ = tmuxchrome.Apply(ctx, session, projectLabel, reachable, nested)

	if nested {
		c := exec.Command("tmux", "switch-client", "-t", session)
		return tea.ExecProcess(c, func(err error) tea.Msg {
			if err != nil {
				return toastMsg{Text: "tmux switch-client: " + err.Error(), Kind: toastError, Until: time.Now().Add(5 * time.Second)}
			}
			return refreshAfterDetachMsg{}
		})
	}
	return tea.ExecProcess(
		exec.Command("tmux", "attach-session", "-d", "-t", session),
		func(err error) tea.Msg {
			if err != nil {
				return toastMsg{Text: "tmux: " + err.Error(), Kind: toastError, Until: time.Now().Add(5 * time.Second)}
			}
			return refreshAfterDetachMsg{}
		},
	)
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
		// Check the Local flag, not the literal name "local": refresh
		// now stamps the local row with the actual hostname so the
		// Devices panel can show it alongside other machines. The
		// flag was added precisely so this predicate didn't need to
		// know the convention.
		if h.Local && h.OK {
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

// shortPeerName squeezes Tailscale's human-friendly HostName ("Sasha's
// Mac mini") into something legible on the Devices panel
// ("sashas-mac-mini"). Lifted out of tailnet so the App can also use
// it for NeedsInstall rows without re-exporting the helper.
func shortPeerName(s string) string {
	out := make([]rune, 0, len(s))
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out = append(out, r)
			prevDash = false
		case r >= 'A' && r <= 'Z':
			out = append(out, r+32)
			prevDash = false
		case r == ' ' || r == '-' || r == '_':
			if len(out) > 0 && !prevDash {
				out = append(out, '-')
				prevDash = true
			}
		}
	}
	for len(out) > 0 && out[len(out)-1] == '-' {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		return s
	}
	return string(out)
}
