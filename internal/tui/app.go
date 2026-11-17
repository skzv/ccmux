// Package tui is the Bubble Tea root model and screen router for ccmux.
package tui

import (
	"context"
	"fmt"
	"strconv"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/i18n"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// Screen identifies which top-level screen is currently focused.
type Screen int

const (
	// ScreenSessions is the combined sessions + dashboard view: running
	// session list on the left, dashboard stats (usage, update banner,
	// device health) on the right. Lives at key "1" because attaching
	// to a session is the highest-frequency action — that's the home
	// row, so to speak. Was named ScreenSessions before being relabelled
	// "Sessions" to match what's actually on the tab.
	ScreenSessions Screen = iota
	// ScreenProjects is at key "2" because the most common second move
	// is "where do I work next" — pick a project to attach or scaffold.
	// Reordered ahead of Conversations after the v0.1.x usability pass:
	// new sessions originate from projects, conversations are mostly
	// looked at after the fact.
	ScreenProjects
	// ScreenConversations is the past-conversations browser:
	// Claude/Codex/Antigravity transcripts across every project, sorted
	// by recency. Reached via `3` (top-level) or `c` on a Projects row.
	ScreenConversations
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
	ScreenSessions:      "Sessions",
	ScreenProjects:      "Projects",
	ScreenConversations: "Conversations",
	ScreenNotes:         "Notes",
	ScreenAgents:        "Agents",
	ScreenSettings:      "Settings",
	ScreenNetwork:       "Network",
}

func (s Screen) String() string {
	if s < 0 || s >= screenCount {
		return "?"
	}
	// Apply tr() at call time, not init time: a runtime language switch
	// (Settings → i18n.lang) must re-label the tab bar / help footer on
	// the next render. An array initialized with tr() at package init
	// would be frozen in the startup language.
	return tr(screenLabels[s])
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

// screenKey returns the digit a user presses to jump to the given
// Screen — the same number the tab bar renders as `[N] Name`. Derived
// from the iota order in the Screen const block, which is the single
// source of truth for tab order.
//
// Use this anywhere user-facing copy references a tab by number
// (empty-state hints like "Press 2 to open Projects", the first-run
// tour). Never hand-write the digit: a future reorder of the const
// block would silently make the copy lie. TestNoLiteralTabKeyDigits
// fails the build if anyone tries.
func screenKey(s Screen) string {
	return strconv.Itoa(int(s) + 1)
}

// tabBarMinWidth is the minimum terminal cols at which the WIDE tab
// bar (with the screen names spelled out) fits without wrapping.
// Computed from the live label lengths so renaming a screen or
// adding a new one automatically adjusts the threshold — no
// magic-number to keep in sync.
//
// Formula: width of " ccmux " brand + sum of " [N] Name " per tab.
// Matches renderHeader's wide-form rendering verbatim.
func tabBarMinWidth() int {
	width := lipgloss.Width(" ccmux ")
	for _, t := range allScreens() {
		width += lipgloss.Width(fmt.Sprintf(" [%d] %s ", int(t)+1, t.String()))
	}
	return width
}

// App is the root Bubble Tea model.
type App struct {
	cfg config.Config
	// runtimeOverrides re-applies per-run, never-persisted tweaks
	// (--projects, --expand-notes) whenever a fresh config is adopted
	// from disk. detectedClaudeTier is the auto-detected subscription
	// tier: shown in the UI, but only ever written to disk if the user
	// sets the tier themselves. startupConfigErr is a config.toml load
	// failure, surfaced as a toast once the UI is up.
	runtimeOverrides   func(*config.Config)
	detectedClaudeTier string
	startupConfigErr   error

	// convLoadGen numbers conversation-list loads; see
	// refreshConversationsCmd.
	convLoadGen int
	styles      styles.Styles
	keys        Keymap
	version     string

	width, height int

	// sessionsLoadGen numbers sessions refreshes and sessionsAppliedGen
	// is the newest one applied; see refreshSessionsCmd. sessionsTickGen
	// is the tick-issued refresh still in flight (0 = none), started at
	// sessionsTickAt — the 2s tick skips while it's pending so slow
	// hosts can't stack up overlapping refreshes.
	sessionsLoadGen    int
	sessionsAppliedGen int
	sessionsTickGen    int
	sessionsTickAt     time.Time

	// projectsLoadGen / projectsAppliedGen do the same for project
	// refreshes (Init, every detach, `r` on Projects), which overlap
	// and finish out of order just like session refreshes.
	projectsLoadGen    int
	projectsAppliedGen int

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

	toasts toastController // transient footer notification + the help-overlay ring buffer

	confirm confirmationModal

	helpOpen         bool
	helpScroll       int                        // first help line shown when the overlay is taller than the terminal
	usageOpen        bool                       // `u` opens the full usage overlay; esc/u closes
	convPreview      conversationPreviewOverlay // `p` opens the transcript-preview overlay on the Conversations screen
	projectInfoOpen  bool                       // `i` on Projects opens the per-project info overlay; esc/i closes
	settingsInfoOpen bool                       // `i` on Settings opens the version/paths/last-save overlay; esc/i closes
	tour             tourModel                  // first-run interactive tour; re-openable with T
	lastRefresh      time.Time
	daemonOnline     bool

	// Easter egg: pressing M (shift-M) opens the Matrix overlay.
	// Consistent with T which reopens the tour.
	matrix matrixModel

	// attach is the loading-overlay state shown between the moment
	// the user picks a session/conversation and the moment Bubble
	// Tea suspends to run `tmux attach`. See attach_loading.go.
	attach attachState

	// sshWizard is the one-time setup flow for a host that fails
	// key auth. Initialized in New() in the closed state; opened
	// from the Network screen ('s' key), from the CLI mirror
	// `ccmux host setup-ssh`, or automatically after a remote
	// attach that exits with permission-denied. See
	// sshsetup_wizard.go for the state machine.
	sshWizard *sshWizardModel
}

// modalCapturingText returns true when the App is in a state where
// keystrokes are going into a text field or a modal owns the
// viewport, so global single-key handlers (the matrix easter egg,
// screen-switch digits, overlay toggles) must NOT capture them.
// Reported case: typing a session name like "matrix-experiment" in
// the new-session form fired the overlay.
//
// The App owns only its own overlay states here; every screen model
// reports its modal/text-capture states through its own
// capturesInput() method. This replaces the old hand-maintained
// cross-model list, which missed newly-added modals twice — a screen
// adding a modal now extends its own capturesInput(), right next to
// the state it adds, instead of remembering to edit App.
//
// Only the ACTIVE screen's capturesInput counts. OR-ing every screen's
// made a modal left open on one screen (the Notes info panel, say)
// disable `?`, `T`, esc-to-dismiss and friends on every other screen.
func (a App) modalCapturingText() bool {
	if a.confirm.open() {
		return true
	}
	if a.sshWizard != nil && a.sshWizard.Active() {
		return true
	}
	// App-owned overlays.
	if a.tour.Active() || a.helpOpen || a.usageOpen || a.convPreview.IsOpen() || a.projectInfoOpen || a.settingsInfoOpen {
		return true
	}
	return a.activeScreenCapturesInput()
}

// activeScreenCapturesInput is the focused screen's capturesInput seam.
func (a App) activeScreenCapturesInput() bool {
	switch a.screen {
	case ScreenSessions:
		return a.sessionsM.capturesInput()
	case ScreenProjects:
		return a.projectsM.capturesInput()
	case ScreenConversations:
		return a.conversationsM.capturesInput()
	case ScreenNotes:
		return a.notes.capturesInput()
	case ScreenAgents:
		return a.agentsM.capturesInput()
	case ScreenSettings:
		return a.settings.capturesInput()
	case ScreenNetwork:
		return a.network.capturesInput()
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
	// Establish the UI language once, before any model is constructed —
	// every screen renders through tr() at View() time, so this one call
	// is all the wiring hot-switching needs.
	i18n.SetLanguage(cfg.Lang)

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
		screen:         ScreenSessions,
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
		sshWizard:      newSSHWizard(st),
		// Init's refreshes carry initRefreshGen; later ones count on
		// from it.
		sessionsLoadGen: initRefreshGen,
		projectsLoadGen: initRefreshGen,
	}
	a.dashboard.SetConfig(cfg)
	a.dashboard.SetVersion(version)
	a.network.SetVersion(version)
	a.sessionsM.SetDefaultDir(cfg.Sessions.DefaultDir)
	a.sessionsM.SetDefaultAgent(cfg.Agents.Default)
	a.sessionsM.SetAgentCommands(cfg.AgentCommands())
	a.projectsM.SetDefaultAgent(cfg.Agents.Default)
	a.projectsM.SetAgentCommands(cfg.AgentCommands())
	a.projectsM.SetProjectsRoot(cfg.Projects.Root)
	// Hide per-agent subscription-tier rows in Settings for agents the
	// user can't run (PATH binary or a setup-pinned command). Detected
	// once here via fast LookPath probes; Claude always shows regardless.
	a.settings.SetAvailableAgents(availableAgentIDs(cfg.AgentCommands()))
	// Seed the live headless-visibility toggle from config. Users
	// can flip it at runtime with H; the config value is just the
	// starting position.
	a.conversationsM.SetShowHeadless(cfg.Conversations.ShowHeadless)
	// Notes folder tree opens collapsed unless the user opts into
	// fully-expanded via config (or `ccmux --expand-notes`, which sets
	// this flag in cfg before New is called).
	a.notes.SetExpandFolders(cfg.Notes.ExpandFolders)
	// First-run tour: open automatically if the user hasn't completed it yet.
	if !cfg.Tour.Shown {
		a.tour.Open()
	}
	return a
}

// availableAgentIDs returns the IDs of agents ccmux can launch on this
// machine (PATH binary or a setup-pinned command override). Used to
// hide irrelevant per-agent tier rows from Settings.
func availableAgentIDs(commands agent.Commands) []agent.ID {
	avail := agent.AllAvailable(context.Background(), commands)
	ids := make([]agent.ID, 0, len(avail))
	for _, a := range avail {
		ids = append(ids, a.ID())
	}
	return ids
}

// Init is called once at startup.
func (a App) Init() tea.Cmd {
	cmds := []tea.Cmd{
		// Not refreshSessionsCmd / refreshProjectsCmd: their generation
		// bump would land on this value receiver's copy. New reserved
		// initRefreshGen for these two.
		a.sessionsRefreshCmd(initRefreshGen),
		a.projectsRefreshCmd(initRefreshGen),
		a.refreshUsageCmd(),
		detectTierCmd(),
		detectMoshiCmd(),
		tickEvery(2 * time.Second),
		usageTick(),
		a.projectsM.Init(),
		a.agentsM.Init(),
		// Kick the Moshi-probe spinner so the Settings screen has
		// motion the moment it's opened — even before the user lands
		// on it, the spinner ID is registered with Bubble Tea's tick
		// router so subsequent ticks are accepted.
		a.settings.SpinnerTick(),
	}
	// Auto-update check: a one-shot background `git fetch` + behind-
	// count when the user hasn't opted out. Fires once per launch —
	// the user isn't running a TUI for weeks without restarting, so a
	// timer would be overkill. Any failure is swallowed (see the
	// updateCheckMsg handler); the worst case is no banner.
	if a.cfg.Update.AutoCheck {
		cmds = append(cmds, checkForUpdateCmd())
	}
	if a.startupConfigErr != nil {
		// Printed to stderr this would be wiped by the alt screen
		// instantly. Every in-app save goes through config.Update,
		// which refuses to write while the file doesn't load.
		cmds = append(cmds, toastCmd(toastError,
			tr("config.toml didn't load; using defaults and not saving changes: ")+a.startupConfigErr.Error(), 15))
	}
	return tea.Batch(cmds...)
}
