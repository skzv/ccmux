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

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/ccusage"
	"github.com/skzv/ccmux/internal/claude"
	"github.com/skzv/ccmux/internal/claudeauth"
	"github.com/skzv/ccmux/internal/claudeusage"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/conversations"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/moshi"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/selfupdate"
	"github.com/skzv/ccmux/internal/tailnet"
	"github.com/skzv/ccmux/internal/tmux"
	"github.com/skzv/ccmux/internal/tmuxchrome"
	"github.com/skzv/ccmux/internal/tui/styles"
	"github.com/skzv/ccmux/internal/usage"
)

// Screen identifies which top-level screen is currently focused.
type Screen int

const (
	// ScreenHome is the combined home screen: running sessions list on
	// the left, dashboard stats (usage, update banner, device health) on
	// the right. Merges what were previously separate Dashboard and
	// Sessions tabs — the user can attach to a session directly from the
	// same view that shows token usage and host health.
	ScreenHome Screen = iota
	// ScreenConversations is the past-conversations browser:
	// Claude/Codex/Antigravity transcripts across every project, sorted
	// by recency. Reached via `2` (top-level) or `c` on a Projects row.
	ScreenConversations
	ScreenProjects
	ScreenNotes
	ScreenAgents
	ScreenSettings
	ScreenNetwork

	// screenCount is a sentinel — it must stay LAST in this block. It
	// equals the number of real screens, so allScreens() can iterate
	// the full set without a hand-maintained list that drifts. Adding
	// a new screen above this line is the *only* edit needed for it to
	// appear in the tab bar; the renderHeader bug (Conversations tab
	// missing because a hardcoded slice wasn't updated) is structurally
	// impossible once everything derives from here.
	screenCount
)

// screenLabels maps each Screen to its tab-bar / help-footer label.
// Index by Screen value. TestScreenLabelsCoverEveryScreen pins that
// this slice stays the same length as screenCount, so a new screen
// without a label trips a test instead of panicking String() at
// runtime.
//
// "Agents" was "Claude" before Codex / Antigravity joined — rename
// here is the canonical place since both the tab bar and the help
// footer read String().
var screenLabels = [screenCount]string{
	ScreenHome:          "Home",
	ScreenConversations: "Conversations",
	ScreenProjects:      "Projects",
	ScreenNotes:         "Notes",
	ScreenAgents:        "Agents",
	ScreenSettings:      "Settings",
	ScreenNetwork:       "Network",
}

func (s Screen) String() string {
	if s < 0 || s >= screenCount {
		return "?"
	}
	return screenLabels[s]
}

// allScreens returns every Screen in tab-bar order. Derived from the
// screenCount sentinel so it can never drift out of sync with the
// const block — the root cause of the "Conversations tab missing
// from the header" bug. renderHeader iterates this; a new screen
// joins the tab bar automatically.
func allScreens() []Screen {
	out := make([]Screen, screenCount)
	for i := range out {
		out[i] = Screen(i)
	}
	return out
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

	dashboard      dashboardModel
	sessionsM      sessionsModel
	conversationsM conversationsModel
	projectsM      projectsModel
	notes          notesModel
	agentsM        agentsModel
	settings       settingsModel
	network        networkModel

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
	if a.projectsM.form != nil || a.projectsM.menu != nil {
		return true
	}
	if a.sessionsM.form != nil || a.sessionsM.renameForm != nil {
		return true
	}
	if a.projectsM.FilterActive() {
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

	// Subscription-tier auto-detection deliberately does NOT run here.
	// It shells out to `claude auth status` (a Node CLI, ~0.9s) and
	// claudeauth's cache is per-process, so doing it synchronously in
	// New() blocked the very first frame on every single launch. It now
	// runs as detectTierCmd, fired async from Init() — see tierDetectedMsg.

	a := App{
		cfg:            cfg,
		styles:         st,
		keys:           km,
		version:        version,
		screen:         ScreenHome,
		dashboard:      newDashboard(st, km),
		sessionsM:      newSessions(st, km),
		conversationsM: newConversations(st, km),
		projectsM:      newProjects(st, km),
		notes:          newNotes(st, km),
		agentsM:        newAgents(st, km),
		settings:       newSettings(st, km, cfg, version),
		network:        newNetwork(st, km),
		tour:           newTour(st),
		matrix:         newMatrix(),
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
	cmds := []tea.Cmd{
		a.refreshSessionsCmd(),
		a.refreshProjectsCmd(),
		a.refreshUsageCmd(),
		detectTierCmd(),
		detectMoshiCmd(),
		tickEvery(2 * time.Second),
		usageTick(),
	}
	// Auto-update check: a one-shot background `git fetch` + behind-
	// count when the user hasn't opted out. Fires once per launch —
	// the user isn't running a TUI for weeks without restarting, so a
	// timer would be overkill. Any failure is swallowed (see the
	// updateCheckMsg handler); the worst case is no banner.
	if a.cfg.Update.AutoCheck {
		cmds = append(cmds, checkForUpdateCmd())
	}
	return tea.Batch(cmds...)
}

// checkForUpdateCmd runs selfupdate.Check off the UI goroutine and
// delivers the outcome as an updateCheckMsg. Bounded by a generous
// timeout — the embedded `git fetch` needs network, but a hung fetch
// must never wedge the TUI.
func checkForUpdateCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		res, err := selfupdate.Check(ctx)
		return updateCheckMsg{Result: res, Err: err}
	}
}

// tierDetectedMsg carries the result of the async `claude auth status`
// probe. Tier is the normalized ccmux tier, or "api" when undetectable.
type tierDetectedMsg struct{ Tier string }

// detectTierCmd runs the subscription-tier probe off the UI goroutine.
// It used to run synchronously in New(), which stalled the first frame
// for ~0.9s on every launch while the `claude` Node CLI booted —
// claudeauth's cache is per-process, so a fresh ccmux never hits it.
// Deferring it lets the TUI paint immediately; the quota bar's plan
// limit fills in a beat later when tierDetectedMsg lands.
func detectTierCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		st, err := claudeauth.Get(ctx)
		if err != nil {
			return tierDetectedMsg{Tier: "api"}
		}
		return tierDetectedMsg{Tier: st.Tier()}
	}
}

// moshiDetectedMsg carries the result of an async moshi.Detect probe.
type moshiDetectedMsg struct{ State moshi.Status }

// detectMoshiCmd runs the moshi/moshi-hook status probe off the UI
// goroutine. moshi.Detect shells out (launchctl/brew on macOS) and used
// to run synchronously in newSettings — a 2s startup stall on every
// launch — and again inline in settingsModel.Update every 30s, which
// froze the TUI mid-session. Both paths now go through this command.
func detectMoshiCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return moshiDetectedMsg{State: moshi.Detect(ctx)}
	}
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
		// field) and the same for Codex/Antigravity (their summaries
		// are today always zero — stub walkers, see internal/usage).
		const window = 5 * time.Hour
		agg, claudeErr := claudeusage.Walk(window)
		codex, _ := usage.WalkCodex(window)
		antigravity, _ := usage.WalkAntigravity(window)
		// Run ccusage alongside the transcript walk — it's an npx
		// invocation so it takes a second, but it's the most accurate
		// source for billing-block burn rate and projections.
		var blk *ccusageBlock
		if b, err := ccusage.CurrentBlock(context.Background()); err == nil {
			blk = &ccusageBlock{
				CostUSD:             b.CostUSD,
				BurnRateCostPerHour: b.BurnRateCostPerHour,
				ProjectedTotalCost:  b.ProjectedTotalCost,
				EndTime:             b.EndTime,
				IsActive:            b.IsActive,
			}
		}
		return usageLoadedMsg{Agg: agg, Codex: codex, Antigravity: antigravity, CcusageBlock: blk, Err: claudeErr}
	}
}

// refreshConversationsCmd loads the full conversations list. Fired on
// Conversations-tab entry and on the Refresh keybind while the screen
// is focused. Walks ~/.claude, ~/.codex, ~/.gemini/antigravity-cli —
// can take a beat on machines with hundreds of transcripts, hence the
// loading-state placeholder in the screen.
func (a App) refreshConversationsCmd() tea.Cmd {
	return func() tea.Msg {
		list, err := conversations.All(conversations.Options{})
		return conversationsLoadedMsg{List: list, Err: err}
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
		cmds := []tea.Cmd{a.refreshSessionsCmd(), tickEvery(2 * time.Second)}
		// Keep the Settings screen's moshi status fresh while it's
		// focused — async, so the 30s refresh never blocks the UI
		// goroutine the way the old inline moshi.Detect did.
		if a.screen == ScreenSettings && a.settings.MoshiStale() {
			cmds = append(cmds, detectMoshiCmd())
		}
		return a, tea.Batch(cmds...)

	case usageTickMsg:
		return a, tea.Batch(a.refreshUsageCmd(), usageTick())

	case openConversationsForProjectMsg:
		a.screen = ScreenConversations
		a.conversationsM.SetProjectFilter(msg.Project)
		a.conversationsM.SetLoading(true)
		return a, a.refreshConversationsCmd()

	case conversationsLoadedMsg:
		if msg.Err != nil {
			a.conversationsM.SetLoadErr(msg.Err.Error())
			return a, nil
		}
		a.conversationsM.SetList(msg.List)
		return a, nil

	case updateCheckMsg:
		// A failed check (no checkout, no upstream, offline) is not an
		// error worth surfacing — it just means "can't tell." Silently
		// drop it; the dashboard shows no banner.
		if msg.Err == nil && msg.Result.Available() {
			a.dashboard.SetUpdateAvailable(msg.Result)
		}
		return a, nil

	case tierDetectedMsg:
		// Async result of detectTierCmd. Only adopt a detected tier when
		// the user hasn't explicitly declared one ("api" is the
		// default-empty marker) — a hand-set tier in config.toml always
		// wins. Push the updated config into the screens that render it.
		if (a.cfg.Subscription.Tier == "" || a.cfg.Subscription.Tier == "api") && msg.Tier != "api" {
			a.cfg.Subscription.Tier = msg.Tier
			a.dashboard.SetConfig(a.cfg)
			a.settings.SetConfig(a.cfg)
		}
		return a, nil

	case moshiDetectedMsg:
		// Async result of detectMoshiCmd — push it into the Settings
		// screen, which renders the moshi/moshi-hook status block.
		a.settings.SetMoshiState(msg.State)
		return a, nil

	case conversationDeletedMsg:
		if msg.Err != nil {
			return a, func() tea.Msg {
				return toastMsg{
					Text:  "delete failed: " + msg.Err.Error(),
					Kind:  toastError,
					Until: time.Now().Add(5 * time.Second),
				}
			}
		}
		// Transcript removed — refresh the list so the row disappears,
		// and toast the confirmation.
		a.conversationsM.SetLoading(true)
		return a, tea.Batch(
			a.refreshConversationsCmd(),
			func() tea.Msg {
				return toastMsg{
					Text:  fmt.Sprintf("deleted %s conversation %s", msg.Agent, shortConversationID(msg.ID)),
					Kind:  toastSuccess,
					Until: time.Now().Add(4 * time.Second),
				}
			},
		)

	case conversationResumedMsg:
		if msg.Err != nil {
			return a, func() tea.Msg {
				return toastMsg{
					Text:  "resume failed: " + msg.Err.Error(),
					Kind:  toastError,
					Until: time.Now().Add(5 * time.Second),
				}
			}
		}
		// Spawn was successful; attach to the new tmux session and
		// refresh Sessions so the row shows up immediately on return.
		return a, tea.Batch(
			a.localAttachCmd(msg.Session, msg.Project),
			a.refreshSessionsCmd(),
			func() tea.Msg {
				return toastMsg{
					Text:  fmt.Sprintf("resumed %s conversation in %s", msg.Agent, msg.Session),
					Kind:  toastSuccess,
					Until: time.Now().Add(4 * time.Second),
				}
			},
		)

	case usageLoadedMsg:
		if msg.Err == nil && msg.Agg != nil {
			a.dashboard.SetUsage(msg.Agg)
		}
		// Codex and Antigravity summaries are pushed unconditionally —
		// today they're always empty (stub walkers) but the dashboard
		// uses HasData to decide between "real numbers" and "install
		// hint" rendering.
		a.dashboard.SetCodexUsage(msg.Codex)
		a.dashboard.SetAntigravityUsage(msg.Antigravity)
		a.dashboard.SetCcusageBlock(msg.CcusageBlock)
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
		a.sessionsM.SetDefaultAgent(a.cfg.Agents.Default)
		a.projectsM.SetDefaultAgent(a.cfg.Agents.Default)
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

	case projectMenuMsg:
		// A project was opened — show its running sessions and past
		// conversations so the user can attach, resume, or start new.
		menu := newProjectMenu(a.styles, msg.Project, msg.ProjectPath, msg.Sessions, msg.Conversations)
		a.projectsM.menu = &menu
		return a, nil

	case projectMenuPickMsg:
		a.projectsM.menu = nil
		switch msg.Entry.kind {
		case menuSession:
			// Attach to the chosen running session.
			return a, a.localAttachCmd(msg.Entry.session.Name, msg.Project)
		case menuConversation:
			// Resume the chosen past conversation in a fresh session.
			return a, a.resumeConversationCmd(msg.Entry.conv)
		case menuNewSession:
			// Start a new session for the project. The launch command
			// comes from the project's .ccmux/agent sidecar so an
			// Antigravity/Codex project doesn't silently boot claude.
			projectPath := msg.ProjectPath
			projectLabel := msg.Project
			launch := launchCmdForProjectPath(projectPath)
			return a, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				// Name it c-<project>, uniquified when that is already
				// running — the menu's "new" is explicitly a second
				// session alongside any existing one.
				name := tmux.SessionNameForPath(projectPath)
				if has, _ := tmux.Has(ctx, name); has {
					name = uniqueSessionName(ctx, name)
				}
				if err := tmux.New(ctx, name, projectPath, launch); err != nil {
					return toastMsg{Text: "start session: " + err.Error(), Kind: toastError, Until: time.Now().Add(5 * time.Second)}
				}
				return projectSessionReadyMsg{Session: name, Project: projectLabel}
			}
		}
		return a, nil

	case projectMenuCancelMsg:
		a.projectsM.menu = nil
		return a, nil

	case renameSessionSubmitMsg:
		a.sessionsM.renameForm = nil
		return a, renameSessionCmd(msg.OldName, msg.NewName)

	case renameSessionCancelMsg:
		a.sessionsM.renameForm = nil
		return a, nil

	case sessionRenamedMsg:
		if msg.Err != nil {
			a.setToast(toastError, "rename failed: "+msg.Err.Error(), 5*time.Second)
		} else {
			a.setToast(toastSuccess, "renamed "+msg.OldName+" → "+msg.NewName, 3*time.Second)
		}
		return a, a.refreshSessionsCmd()

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
		remoteCmd := remoteTmuxAttach(msg.SessionName, a.cfg.Sessions.DetachOthersOnAttach())
		var c *exec.Cmd
		if msg.Mosh {
			c = exec.Command("mosh", target, "--", "bash", "-c", remoteCmd)
			if dbg := debugLogger(); dbg != nil {
				dbg.Printf("remote attach: mosh %s -- bash -c %q", target, remoteCmd)
			}
		} else {
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
			!(a.screen == ScreenProjects && (a.projectsM.form != nil || a.projectsM.menu != nil)) {
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

		// If projects screen has its modal open (new-project form or session
		// picker), route through it. We intentionally still allow global Quit.
		if a.screen == ScreenProjects && (a.projectsM.form != nil || a.projectsM.menu != nil) {
			if msg.String() == "ctrl+c" {
				return a, tea.Quit
			}
			var cmd tea.Cmd
			a.projectsM, cmd = a.projectsM.Update(msg)
			return a, cmd
		}

		// Projects filter mode: textinput owns the keystrokes. Enter
		// commits the filter and attaches to the highlighted match;
		// esc clears the filter without firing attach. ctrl+c still
		// quits so the user is never trapped.
		if a.screen == ScreenProjects && a.projectsM.FilterActive() {
			if msg.String() == "ctrl+c" {
				return a, tea.Quit
			}
			if keyMatches(msg, a.keys.Enter) {
				a.projectsM.commitFilter()
				return a, a.attachOrCreateForSelectedProject()
			}
			if msg.String() == "esc" {
				a.projectsM.exitFilter()
				return a, nil
			}
			var cmd tea.Cmd
			a.projectsM, cmd = a.projectsM.Update(msg)
			return a, cmd
		}

		// Same modal-routing for the Sessions tab: new-bare-session form and
		// rename form both need to intercept Enter/digit keys before the global
		// handlers see them. Without this, the global Enter handler below
		// intercepts the form's submit key and attaches to whatever session
		// the cursor was on — observed as "Enter in the new-session form
		// attaches to c-ccmux instead of creating a new session".
		if a.screen == ScreenHome && (a.sessionsM.form != nil || a.sessionsM.renameForm != nil) {
			if msg.String() == "ctrl+c" {
				return a, tea.Quit
			}
			var cmd tea.Cmd
			a.sessionsM, cmd = a.sessionsM.Update(msg)
			return a, cmd
		}

		// Notes search mode: the search textinput owns every keystroke so
		// global bindings like "r" (refresh) don't swallow characters mid-query.
		if a.screen == ScreenNotes && a.notes.searching {
			if msg.String() == "ctrl+c" {
				return a, tea.Quit
			}
			var cmd tea.Cmd
			a.notes, cmd = a.notes.Update(msg)
			return a, cmd
		}

		switch {
		case keyMatches(msg, a.keys.Quit):
			return a, tea.Quit
		case keyMatches(msg, a.keys.Home):
			a.screen = ScreenHome
			return a, nil
		case keyMatches(msg, a.keys.Conversations):
			a.screen = ScreenConversations
			// Conversations are read lazily — refresh on every entry so
			// a newly-saved transcript from another window shows up next
			// time the user opens this tab.
			a.conversationsM.SetLoading(true)
			return a, a.refreshConversationsCmd()
		case keyMatches(msg, a.keys.Projects):
			a.screen = ScreenProjects
			return a, nil
		case keyMatches(msg, a.keys.Notes):
			a.screen = ScreenNotes
			// Propagate the currently-focused project from Projects,
			// honoring any active filter so the Notes pane opens on
			// what the user just highlighted. SetProject returns a Cmd
			// that runs Vault.List off the UI goroutine — chain it so
			// the screen doesn't freeze on projects with deep trees.
			var cmd tea.Cmd
			if sel := a.projectsM.Selected(); sel != nil {
				cmd = a.notes.SetProject(sel)
			}
			return a, cmd
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
		case keyMatches(msg, a.keys.Enter) && a.screen == ScreenHome:
			return a, a.attachSelectedSession()
		case keyMatches(msg, a.keys.Enter) && a.screen == ScreenProjects:
			return a, a.attachOrCreateForSelectedProject()
		case keyMatches(msg, a.keys.Enter) && a.screen == ScreenConversations:
			return a, a.resumeSelectedConversation()
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
	case ScreenHome:
		// Dashboard always updates (handles usage ticks, refresh msgs).
		// Sessions handles navigation and session-action keys.
		var dcmd tea.Cmd
		a.dashboard, dcmd = a.dashboard.Update(msg)
		a.sessionsM, cmd = a.sessionsM.Update(msg)
		cmd = tea.Batch(cmd, dcmd)
	case ScreenConversations:
		a.conversationsM, cmd = a.conversationsM.Update(msg)
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
	case ScreenHome:
		body = a.homeView(a.width, bodyHeight)
	case ScreenConversations:
		body = a.conversationsM.View(a.width, bodyHeight)
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

// homeView renders the combined Home screen. On a phone (narrow) the
// hero is dropped entirely and the screen is a single column: sessions,
// then the stat tiles. On a monitor the hero is a full-width banner
// across the top; below it the sessions list on the left, and the
// session detail + the three stat tiles stacked on the right.
func (a App) homeView(width, height int) string {
	narrow := isNarrow(width)
	a.dashboard.narrow = narrow // a is a value copy; the panels read m.narrow

	// A sessions modal (rename / new-session form) takes the whole
	// Home body, centered — sessionsModel.View owns that rendering.
	if a.sessionsM.form != nil || a.sessionsM.renameForm != nil {
		return a.sessionsM.View(width, height, narrow)
	}

	if narrow {
		// No hero on a phone — straight to sessions, then the tiles.
		tiles := a.dashboard.StatsView(width)
		listH := height - lipgloss.Height(tiles)
		if listH < 5 {
			listH = 5
		}
		sessions := a.sessionsM.View(width, listH, true)
		return lipgloss.JoinVertical(lipgloss.Left, sessions, tiles)
	}

	// Monitor: hero banner across the top; below it the sessions list
	// on the left and the session detail + stat tiles on the right.
	hero := a.dashboard.heroPanel(width)
	rowH := height - lipgloss.Height(hero)
	if rowH < 5 {
		rowH = 5
	}
	gutter := 1
	leftW := (width - gutter) / 2
	rightW := width - leftW - gutter
	left := a.sessionsM.renderList(leftW, rowH)
	right := lipgloss.JoinVertical(lipgloss.Left,
		a.sessionsM.renderDetail(rightW, false),
		a.dashboard.StatsView(rightW),
	)
	row := lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
	return lipgloss.JoinVertical(lipgloss.Left, hero, row)
}

// renderHeader is the top-of-screen tab strip. On narrow terminals the
// tab labels collapse to just their number; the full strip never wraps.
//
// The tab list comes from allScreens() — never a hardcoded slice.
// A hardcoded list is what dropped the Conversations tab from the bar
// for a release; deriving from the Screen enum makes that class of
// bug structurally impossible.
func (a App) renderHeader() string {
	narrow := isNarrow(a.width)
	var parts []string
	// The " ccmux " brand title is T2 — dropped on narrow so the tab
	// numbers get the reclaimed width.
	if !narrow {
		parts = append(parts, a.styles.Title.Render(" ccmux "))
	}
	for _, t := range allScreens() {
		// Number label = enum value + 1. This is the SAME number the
		// keymap binds (Dashboard→1 … Network→8), because the keymap
		// and the enum share their order. Using int(t)+1 rather than a
		// loop index keeps the label correct even if allScreens() is
		// ever reordered.
		num := int(t) + 1
		var label string
		if narrow {
			// Just the number when space is tight.
			label = fmt.Sprintf(" %d ", num)
			if t == a.screen {
				label = fmt.Sprintf("[%d %s]", num, t.String()[:1])
			}
		} else {
			label = fmt.Sprintf("[%d] %s", num, t.String())
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
	narrow := isNarrow(a.width)

	host, _ := os.Hostname()
	hostChip := a.styles.HostColor("local").Render("● " + shortHostname(host))

	daemonChip := a.styles.StatusError.Render("⚠ offline")
	if a.daemonOnline {
		daemonChip = a.styles.StatusGood.Render("✓ daemon")
	}

	dangerBanner := ""
	if a.cfg.Sleep.DangerousKeepAwakeOnBattery {
		dangerBanner = a.styles.StatusDanger.Render("⚠ BATT") + " "
	}

	// Left block ordered T0-first — battery-danger, daemon, then host
	// — so if forceSingleLine still has to truncate it eats the lower-
	// priority host before the safety-critical chips.
	leftBlock := dangerBanner + daemonChip + "  " + hostChip

	// Right block: the session count (T1) always; the refreshed-at
	// clock and the version chip (both T2) only when wide. Dirty
	// builds (`<sha>-dirty`) still self-flag in the wide version chip.
	right := a.styles.Muted.Render(fmt.Sprintf("%d sess", len(a.sessions)))
	if !narrow {
		refreshed := "—"
		if !a.lastRefresh.IsZero() {
			refreshed = a.lastRefresh.Format("15:04:05")
		}
		versionChip := a.styles.Muted.Render("v" + a.version)
		if strings.Contains(a.version, "dirty") {
			versionChip = a.styles.StatusWarning.Render(a.version)
		}
		right = a.styles.Muted.Render(fmt.Sprintf("%d sess • %s", len(a.sessions), refreshed)) + "  " + versionChip
	}

	// Compute available space for the right block. The StatusBar style
	// adds 1-col padding each side, so the composed body must target
	// width-2 — otherwise forceSingleLine would chop the right tail
	// (the version chip). If the left side already overflows we skip
	// the right block rather than feeding strings.Repeat a negative.
	inner := a.width - 2
	leftW := lipgloss.Width(leftBlock)
	rightW := lipgloss.Width(right)
	body := leftBlock
	if inner-leftW-rightW >= 2 {
		spacer := strings.Repeat(" ", inner-leftW-rightW)
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
	// Hint line ordered T0-first: `? help` (the gateway to every
	// binding) and `q quit` lead, so if forceSingleLine still has to
	// truncate it eats the T2 action hints from the tail. On narrow
	// only the T0/T1 pair is shown.
	hint := "? help • q quit"
	if !isNarrow(a.width) {
		hint += " • r refresh • x kill • n new • 1-7 screens"
	}
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
			remoteArgs := tmux.AttachArgs(sel.Name, a.cfg.Sessions.DetachOthersOnAttach())
			return tea.ExecProcess(
				exec.Command(bin, append([]string{target, "--", "tmux"}, remoteArgs...)...),
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
			remoteCmd := remoteTmuxAttach(sel.Name, a.cfg.Sessions.DetachOthersOnAttach())
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

// launchCmdForProject and launchCmdForProjectPath are the canonical
// places to resolve the tmux launch command for a project session.
// Both go through agent.ByID(...).LaunchCmd(true) — `true` because
// every project-attach path is "resume the existing conversation"
// from the user's POV; `--continue` is what makes the resume real.
//
// Two flavors because some sites have the Project in hand and others
// only have the path. Both must agree so a project whose sidecar
// says Antigravity launches `agy --continue || agy || zsh` no matter
// which code path the user took.
func launchCmdForProject(p project.Project) string {
	return agent.ByID(p.Agent).LaunchCmd(true)
}

func launchCmdForProjectPath(projectPath string) string {
	return agent.ByID(project.ReadAgent(projectPath)).LaunchCmd(true)
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
//
// detachOthers carries the local user's attach-mode preference onto
// the remote tmux: in mirror mode the remote session keeps any other
// clients (the remote machine's own terminal, another of the user's
// devices); in exclusive mode it kicks them. The preference is the
// attaching CLIENT's — "do I want to bump whoever else is viewing
// this" — so the local config is the right source.
func remoteTmuxAttach(session string, detachOthers bool) string {
	flags := ""
	if detachOthers {
		flags = " -d"
	}
	return "PATH=/opt/homebrew/bin:/usr/local/bin:/home/linuxbrew/.linuxbrew/bin:/snap/bin:$PATH" +
		" tmux attach-session" + flags + " -t " + shellQuote(session)
}

// attachOrCreateForSelectedProject is Enter on Projects screen.
// Routes by Host:
//   - local (Host == "" or "local"): existing flow — tmux.New here,
//     localAttachCmd attaches.
//   - remote: POST /v1/sessions to that host's ccmuxd so the tmux
//     session is created on the remote, then ssh-attach into it
//     using the same dial path the Sessions screen uses.
func (a App) attachOrCreateForSelectedProject() tea.Cmd {
	sel := a.projectsM.Selected()
	if sel == nil {
		return nil
	}
	p := *sel
	host := projectHost(p)
	if host == "local" {
		return a.attachOrCreateLocal(p)
	}
	return a.attachOrCreateRemote(p, host)
}

// attachOrCreateLocal handles Enter on a local project. It gathers the
// project's running tmux sessions and past conversations and emits a
// projectMenuMsg so the App opens the project menu modal — the user
// then attaches, resumes, or starts a new session. When the project has
// neither (a brand-new project, never run) it skips the modal and
// creates+attaches in one step, since a one-item menu is pointless.
func (a App) attachOrCreateLocal(p project.Project) tea.Cmd {
	label := p.Name
	path := p.Path
	// Resolve the launch command from the project's sidecar (.ccmux/
	// agent) up front — this used to hardcode the Claude command,
	// which silently overrode Codex / Antigravity projects.
	launch := launchCmdForProject(p)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		var sessions []tmux.Session
		if all, err := tmux.List(ctx); err == nil {
			for _, s := range all {
				if s.Path == path {
					sessions = append(sessions, s)
				}
			}
		}
		convs := conversationsForProject(path)

		if len(sessions) == 0 && len(convs) == 0 {
			// Nothing to choose between — create and attach directly.
			session := p.SessionName()
			if err := tmux.New(ctx, session, path, launch); err != nil {
				return toastMsg{Text: "start session: " + err.Error(), Kind: toastError, Until: time.Now().Add(5 * time.Second)}
			}
			return projectSessionReadyMsg{Session: session, Project: label}
		}
		return projectMenuMsg{
			Project:       label,
			ProjectPath:   path,
			Sessions:      sessions,
			Conversations: convs,
		}
	}
}

// conversationsForProject returns past conversations whose recorded
// working directory matches projectPath, newest first (conversations.All
// already sorts by recency). Drives the project menu's "resume" rows.
func conversationsForProject(projectPath string) []conversations.Conversation {
	all, err := conversations.All(conversations.Options{})
	if err != nil {
		return nil
	}
	var out []conversations.Conversation
	for _, c := range all {
		if c.Project == projectPath {
			out = append(out, c)
		}
	}
	return out
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

// shortConversationID truncates a conversation UUID to its first 8
// chars — enough to be recognizable in a tmux session name or a toast
// without the full 36-char UUID hogging the line. Short IDs that are
// already ≤8 chars pass through unchanged.
func shortConversationID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// resumeSelectedConversation spawns a new tmux session running the
// agent that owns the highlighted conversation, with the agent's
// per-CLI --resume flag pointed at this conversation's ID. The
// command produces a conversationResumedMsg the App handler turns
// into an attach + toast + sessions refresh.
//
// Session naming: c-resume-<short-id> rather than the usual
// c-<project> so resuming twice doesn't try to reuse the same tmux
// session (which would just attach to the first invocation's running
// agent instead of starting a new one with the resume flag).
//
// cwd: when the conversation has a known Project path, we cd there so
// the agent's working directory matches what it had originally —
// otherwise the user falls into $HOME and `/file edit some.go`
// completions break.
func (a App) resumeSelectedConversation() tea.Cmd {
	sel := a.conversationsM.Selected()
	if sel == nil {
		return nil
	}
	return a.resumeConversationCmd(*sel)
}

// resumeConversationCmd spawns a fresh tmux session that resumes
// conversation `c` and emits a conversationResumedMsg. Shared by the
// Conversations screen's Enter handler and the project menu's "resume"
// rows. See resumeSelectedConversation's doc for the naming/cwd
// rationale.
func (a App) resumeConversationCmd(c conversations.Conversation) tea.Cmd {
	return func() tea.Msg {
		argv := c.ResumeArgs()
		if len(argv) == 0 {
			return conversationResumedMsg{Err: fmt.Errorf("don't know how to resume agent %q", c.Agent)}
		}
		sessionName := "c-resume-" + shortConversationID(c.ID)
		// Build the shell command tmux runs in the new pane. zsh
		// fallback keeps the pane alive if the agent binary is missing.
		cmdline := strings.Join(argv, " ") + " || zsh"
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tmux.New(ctx, sessionName, c.Project, cmdline); err != nil {
			return conversationResumedMsg{Err: fmt.Errorf("tmux new-session: %w", err)}
		}
		return conversationResumedMsg{
			Session: sessionName,
			Project: c.Project,
			Agent:   string(c.Agent),
		}
	}
}

func (a App) localAttachCmd(session, projectLabel string) tea.Cmd {
	// moshi.Detect drives only the cosmetic Moshi badge and can be slow
	// on macOS (it shells out). Give it its own bounded context so it
	// can't starve the chrome step below.
	mctx, mcancel := context.WithTimeout(context.Background(), 2*time.Second)
	mst := moshi.Detect(mctx)
	mcancel()
	nested := tmuxchrome.InTmux()
	// Moshi badge is "reachable" when the whole pipeline is wired AND
	// running: paired with Moshi cloud, Claude Code hooks installed,
	// daemon up. Previously this AND'ed Connected, which was always
	// false because moshi-hook status doesn't expose live websocket
	// state — so the chrome read "phone: not paired" even on a fully
	// configured host.
	reachable := mst.Paired && mst.HooksInstalled && mst.ServiceRunning
	// Apply chrome on a fresh, independent context — a context shared
	// with moshi.Detect would let a slow probe cancel the set-option
	// calls, leaving the session in vanilla tmux styling.
	cctx, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = tmuxchrome.Apply(cctx, session, projectLabel, reachable, nested)
	ccancel()

	if nested {
		c := exec.Command("tmux", "switch-client", "-t", session)
		return tea.ExecProcess(c, func(err error) tea.Msg {
			if err != nil {
				return toastMsg{Text: "tmux switch-client: " + err.Error(), Kind: toastError, Until: time.Now().Add(5 * time.Second)}
			}
			return refreshAfterDetachMsg{}
		})
	}
	attachArgs := tmux.AttachArgs(session, a.cfg.Sessions.DetachOthersOnAttach())
	return tea.ExecProcess(
		exec.Command("tmux", attachArgs...),
		func(err error) tea.Msg {
			if err != nil {
				return toastMsg{Text: "tmux: " + err.Error(), Kind: toastError, Until: time.Now().Add(5 * time.Second)}
			}
			return refreshAfterDetachMsg{}
		},
	)
}

// uniqueSessionName finds the next unused tmux session name by appending a
// numeric suffix to `base` (e.g. "c-myproject-2", "c-myproject-3", …).
// Falls back to a millisecond timestamp suffix if the first 99 candidates
// are all taken. The caller is responsible for the context lifetime.
func uniqueSessionName(ctx context.Context, base string) string {
	for i := 2; i < 100; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if has, _ := tmux.Has(ctx, candidate); !has {
			return candidate
		}
	}
	return fmt.Sprintf("%s-%d", base, time.Now().UnixMilli())
}

// renameSessionCmd runs `tmux rename-session` and returns the result.
func renameSessionCmd(oldName, newName string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		err := tmux.Rename(ctx, oldName, newName)
		return sessionRenamedMsg{OldName: oldName, NewName: newName, Err: err}
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
