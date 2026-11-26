package main

import (
	"context"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/apns"
	"github.com/skzv/ccmux/internal/claudemodels"
	"github.com/skzv/ccmux/internal/clipboard"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/fcm"
	"github.com/skzv/ccmux/internal/moshi"
	"github.com/skzv/ccmux/internal/proctree"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/sleeplock"
	"github.com/skzv/ccmux/internal/tmux"
)

// apnsPusher / fcmPusher are the parts of *apns.Sender / *fcm.Sender the
// daemon uses — interfaces so tests can observe which gateway a push
// goes to.
type apnsPusher interface {
	Enabled() bool
	Send(ctx context.Context, deviceToken, environment string, n apns.Notification) error
}

type fcmPusher interface {
	Enabled() bool
	Send(deviceToken string, n fcm.Notification) error
}

// tracked is the per-session daemon state held across poll iterations.
type tracked struct {
	last        string    // last captured pane content (for change detection)
	lastChange  time.Time // when content last changed
	state       agent.State
	promptCount int
	// agentID is the AI agent this session is running — its tag, its
	// project's sidecar, or the agent in its foreground (see
	// fixedAgent in poll.go); shellAgentID for none. The classifier for
	// state detection is `agent.ByID(agentID)`'s — that's what lets
	// Codex and Antigravity sessions get their own heuristics instead
	// of borrowing Claude's box-drawing prompt detector.
	agentID agent.ID
	// projectPath is the working directory of the tmux session, used
	// to resolve the agent sidecar.
	projectPath string
	// seen is the reviewed/unreviewed flag exposed via daemon.SessionState.
	// Set false when the agent transitions to a state the user should
	// look at (needs_input, or active→idle while not attached); set
	// true once a tmux client is attached. Default true so sessions
	// the daemon just discovered don't immediately scream for
	// attention until they actually do something.
	seen bool
	// muted mirrors @ccmux_muted on the tmux session. When true,
	// bells and push notifications are suppressed for this session.
	muted bool
	// baseline marks a session the daemon is seeing for the first time
	// although it wasn't just created: it existed before this daemon
	// started (restart, upgrade, crash respawn) or appeared under a new
	// name (a rename done straight through tmux). Its first
	// classification is recorded as the starting state, not treated as
	// a transition — otherwise every restart re-rang the bell and
	// re-sent a push for every session.
	baseline bool
	// touched is stamped by IPC handlers that move this entry while a
	// poll tick may be in flight (rename). Phase 3's GC skips entries
	// touched after the tick's Phase-1 live-set snapshot, so a rename
	// racing a tick can't wipe the renamed session's tracked state.
	touched time.Time
	// pollTrack is the rest of the poll loop's per-session memory
	// (poll.go).
	pollTrack
}

type server struct {
	cfg       config.Config
	startedAt time.Time
	mu        sync.Mutex
	seen      map[string]*tracked
	// gone tombstones names this daemon killed or renamed away (name →
	// when), guarded by mu. A poll tick whose tmux.List ran before the
	// kill or rename still lists the old name; Phase 1 skips it rather
	// than re-track a ghost and announce it as created. Short-lived:
	// see tombstoneTTL.
	gone    map[string]time.Time
	sleeper *sleeplock.Manager

	tokens *daemon.TokenStore
	events *daemon.EventBus
	// streamsDone is closed (once, via stopEventStreams) when the daemon
	// shuts down, ending the long-lived /v1/events handlers.
	streamsDone chan struct{}
	stopStreams sync.Once

	// tailnetLive is true while the tailnet HTTP listener is serving.
	tailnetLive atomic.Bool

	// enableClipboard applies ccmux's tmux clipboard options/bindings
	// (clipboard.EnableTmuxClipboard; a seam for tests). They live in
	// the tmux *server*, so they are lost whenever that server exits;
	// clipboardApplied tracks whether the current server has them, and
	// the poll loop re-applies them when a server (re)appears. Touched
	// only from startBackground and the poll goroutine.
	enableClipboard  func(context.Context) error
	clipboardApplied bool

	// devices tracks paired iPhones (and Android phones once the FCM
	// path is fully wired) for push routing. apnsSender / fcmSender
	// are gateway clients; all three fields stay non-nil even when
	// push is disabled, so handlers can call them unconditionally.
	devices    *daemon.DeviceStore
	apnsSender apnsPusher
	fcmSender  fcmPusher
	// apnsSlots caps the number of concurrent APNs sends so a slow
	// HTTP/2 handshake can't accumulate goroutines on every poll tick.
	// Defaults to 16 — enough headroom for a small fleet of paired
	// phones, small enough that a wedged APNs endpoint applies
	// back-pressure to the poll loop rather than leaking forever.
	apnsSlots chan struct{}
	// fcmSlots mirrors apnsSlots for the FCM v1 path.
	fcmSlots chan struct{}

	// moshiState is refreshed by moshiLoop (not every poll) so we don't
	// shell out to moshi-hook every 2 seconds. Used only to drive the
	// "moshi reachable" badge in the tmux status bar — the bell rings
	// independently per the always-ring policy. detectMoshi is the
	// detection seam (moshi.Detect); nil disables the loop.
	moshiState  moshi.Status
	moshiMu     sync.Mutex
	detectMoshi func(ctx context.Context) moshi.Status

	// models is the Claude model catalog service. Reads from disk
	// cache; refreshes it in the background weekly (modelRefreshLoop)
	// from the `claude` CLI, else the Anthropic Models API when an API
	// key is set. Always non-nil — falls back to a curated in-binary
	// list when neither answers so the picker still has something
	// useful to show. See internal/claudemodels.
	models *claudemodels.Service

	// Poll-loop seams. Defaulted by newServer to the real tmux-backed
	// implementations; tests override them to drive pollOnce
	// deterministically without a real pane. list enumerates tmux
	// sessions; capture reads a session's pane content; bell signals a
	// needs-input transition.
	list    func(ctx context.Context) ([]tmux.Session, error)
	capture func(ctx context.Context, name string, lines int) (string, error)
	// paneTitle reads the agent CLI's OSC-set title (#{pane_title}) —
	// a second, higher-quality detection signal alongside capture-pane.
	// Same shape as capture so tests can inject deterministic titles.
	paneTitle func(ctx context.Context, name string) (string, error)
	bell      func(ctx context.Context, name string) error
	readAgent func(projectPath string) agent.ID
	// panes lists a session's panes and capturePane reads one pane by
	// id: the poll loop's way to read the agent's own pane rather than
	// whichever one is active (see observe in poll.go). Either left nil
	// falls back to capture/paneTitle on the session's active pane.
	panes       func(ctx context.Context, session string) ([]tmux.Pane, error)
	capturePane func(ctx context.Context, paneID string, lines int) (string, error)
	// sendKeysPane types into one pane by id, sendKeys into a session's
	// active pane (tmux.SendKeysPane / tmux.SendKeys; nil means those):
	// /send-keys reaches the agent's pane through the first and falls
	// back to the second only when that pane can't be resolved (see
	// agentPaneID).
	sendKeysPane func(ctx context.Context, paneID, keys string) error
	sendKeys     func(ctx context.Context, name, keys string) error
	// markSpinner records on a session that agentID announces its turns
	// there with a working-spinner title (tmux.SetSessionSpinner), so a
	// restarted daemon reads it back rather than learning it again (see
	// spinnerMarkDue in poll.go). nil skips recording.
	markSpinner func(ctx context.Context, name, agentID string) error
	// markReview writes a session's review record — its reviewed flag,
	// prompt count and state (tmux.SetSessionReview) — so a restarted
	// daemon, or one that finds the session renamed behind its back,
	// reads them back (see reviewDue in poll.go). nil skips recording.
	markReview func(ctx context.Context, name string, r tmux.Review) error
	// procs snapshots the process table (proctree.Snapshot), once per
	// tick, to tell an agent still running in its pane from one that
	// exited to the launch chain's fallback shell (see noteAgentExits in
	// agent_exit.go). nil leaves agent exits to the screen rules.
	procs func(ctx context.Context) (*proctree.Table, error)
	// titleRecheckDelay is how long after a pane's read a spinner title
	// that looks frozen is read again (see recheckTitles); zero means
	// defaultTitleRecheckDelay.
	titleRecheckDelay time.Duration

	// Session-handler seams, defaulted to tmux.Has / tmux.Kill /
	// tmux.Rename so the create/kill/rename handlers' bookkeeping is
	// unit-testable without a tmux server.
	has      func(ctx context.Context, name string) (bool, error)
	kill     func(ctx context.Context, name string) error
	rename   func(ctx context.Context, oldName, newName string) error
	setMuted func(ctx context.Context, name string, muted bool) error
	// startGrace is how long the create handlers watch a new session
	// for an immediate exit (confirmStarted). Zero skips the check.
	startGrace time.Duration

	// pollBudget bounds one whole pollOnce tick (tmux.List + every
	// capture-pane/display-message + the bell path). Without it a
	// single wedged subprocess — a SIGSTOP'd tmux, most plausibly —
	// blocks the poll loop forever while the daemon keeps serving
	// frozen state. Comfortably above normal capture latency for ~20
	// sessions; tests shrink it to drive the timeout path fast.
	pollBudget time.Duration
	// listErrLoggedAt rate-limits the tmux.List failure log in
	// pollOnce, and procErrLoggedAt the process snapshot's (accessed
	// only from the poll goroutine).
	listErrLoggedAt time.Time
	procErrLoggedAt time.Time
}

// newServer builds a server with its default (real, tmux-backed)
// poll-loop seams wired. Both the daemon entrypoint and the
// integration tests construct through here so the seam defaults stay
// in one place.
func newServer(cfg config.Config) *server {
	// Device-token + APNs setup is best-effort: failures (no store
	// dir, bad APNs key) log and disable push but never block the
	// daemon from coming up.
	var devices *daemon.DeviceStore
	if devPath, err := daemon.DefaultDeviceStorePath(); err == nil {
		if ds, derr := daemon.OpenDeviceStore(devPath); derr == nil {
			devices = ds
		} else {
			log.Printf("ccmuxd: device store unavailable: %v", derr)
		}
	}
	apnsCfg := apns.Config{
		Enabled:     cfg.APNs.Enabled,
		KeyPath:     cfg.APNs.KeyPath,
		KeyID:       cfg.APNs.KeyID,
		TeamID:      cfg.APNs.TeamID,
		Topic:       cfg.APNs.Topic,
		Environment: cfg.APNs.Environment,
	}
	sender, err := apns.New(apnsCfg)
	if err != nil {
		log.Printf("ccmuxd: APNs disabled: %v", err)
		sender, _ = apns.New(apns.Config{})
	}

	// FCM sender follows the APNs pattern: best-effort init, log+disable
	// on failure so a misconfigured Firebase entry never blocks startup.
	// The package is dormant by default so the no-op fallback after a
	// bad config keeps the dispatcher's per-provider branch well-formed.
	fcmCfg := fcm.Config{
		Enabled:         cfg.FCM.Enabled,
		CredentialsPath: cfg.FCM.CredentialsPath,
		ProjectID:       cfg.FCM.ProjectID,
	}
	fcmSender, err := fcm.New(fcmCfg)
	if err != nil {
		log.Printf("ccmuxd: FCM disabled: %v", err)
		fcmSender, _ = fcm.New(fcm.Config{})
	}

	// Model catalog. CachePath errors only when $HOME is unresolvable —
	// log and degrade to an in-memory-only Service (writes silently
	// fail, but the in-binary fallback list is still served). Picking
	// up ANTHROPIC_API_KEY at startup is the simplest sane model;
	// users who set it later restart the daemon.
	modelCache, err := claudemodels.CachePath()
	if err != nil {
		log.Printf("ccmuxd: model cache path unresolved (%v) — serving fallback list only", err)
	}
	models := claudemodels.New(modelCache, os.Getenv("ANTHROPIC_API_KEY"))

	return &server{
		cfg:             cfg,
		seen:            map[string]*tracked{},
		startedAt:       time.Now(),
		enableClipboard: clipboard.EnableTmuxClipboard,
		list:            tmux.List,
		capture:         tmux.CapturePane,
		pollBudget:      10 * time.Second,
		paneTitle:       tmux.PaneTitle,
		panes:           tmux.ListPanes,
		capturePane:     tmux.CapturePaneID,
		sendKeysPane:    tmux.SendKeysPane,
		sendKeys:        tmux.SendKeys,
		markSpinner:     tmux.SetSessionSpinner,
		markReview:      tmux.SetSessionReview,
		procs:           proctree.Snapshot,
		detectMoshi:     moshi.Detect,
		bell:            notificationBell(cfg.Notifications),
		readAgent:       project.ReadAgent,
		has:             tmux.Has,
		kill:            tmux.Kill,
		rename:          tmux.Rename,
		setMuted:        tmux.SetSessionMuted,
		startGrace:      defaultStartGrace,
		tokens:          daemon.NewTokenStore(),
		events:          daemon.NewEventBus(),
		streamsDone:     make(chan struct{}),
		devices:         devices,
		apnsSender:      sender,
		fcmSender:       fcmSender,
		apnsSlots:       make(chan struct{}, 16),
		fcmSlots:        make(chan struct{}, 16),
		models:          models,
	}
}

// freshConfig re-reads ~/.config/ccmux/config.toml so a launch picks
// up any setting the user changed since the daemon started: `[claude]
// default_model`, sessions.default_dir, agents.default. The whole
// config read is cheap (one tiny TOML file) and only happens at
// session-create time, not in the poll loop. Falls back to the
// startup-cached cfg on read errors so a temporarily-corrupt config
// file can't break new-session.
func (s *server) freshConfig() config.Config {
	if cfg, err := config.Load(); err == nil {
		return cfg
	}
	return s.cfg
}

// freshCommands is freshConfig's agent commands.
func (s *server) freshCommands() agent.Commands {
	return s.freshConfig().AgentCommands()
}
