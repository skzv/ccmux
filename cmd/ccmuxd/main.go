// Command ccmuxd is the ccmux background daemon. It polls tmux state,
// classifies Claude session state, rings the terminal bell on needs-input
// transitions, holds a sleep-prevention lock while sessions are active,
// and serves the IPC protocol over a Unix socket (and optionally a
// Tailscale-bound HTTP listener).
//
// This v0.1 entrypoint wires up the listeners and the poll loop. The
// individual subsystems (state machine, bell injector, sleep manager,
// tailnet listener) live in internal/daemon and grow file-by-file.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/apns"
	"github.com/skzv/ccmux/internal/clipboard"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/conversations"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/moshi"
	"github.com/skzv/ccmux/internal/notes"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/scaffold"
	"github.com/skzv/ccmux/internal/sleeplock"
	"github.com/skzv/ccmux/internal/tailnet"
	"github.com/skzv/ccmux/internal/tmux"
	"github.com/skzv/ccmux/internal/tmuxchrome"
	"github.com/skzv/ccmux/internal/usage"
)

var version = "dev"

// maxJSONBodyBytes caps the size of an incoming JSON request body for
// every handler. The largest legitimate body on either socket is a
// device-registration payload at well under 4 KiB; the cap exists so
// a tailnet peer can't OOM the daemon by streaming gigabytes of trash
// into a Decode call.
const maxJSONBodyBytes = 64 * 1024

func main() {
	if err := run(); err != nil {
		log.Fatalf("ccmuxd: %v", err)
	}
}

func run() error {
	cfg, _ := config.Load()
	if cfg.Daemon.PollIntervalSeconds == 0 {
		cfg.Daemon.PollIntervalSeconds = 2
	}
	if cfg.Daemon.IdleSecondsForNeedsInput == 0 {
		cfg.Daemon.IdleSecondsForNeedsInput = 3
	}

	srv := newServer(cfg)
	srv.startSleepManager()
	// Make sure any system-wide override (very_dangerous mode) is
	// reverted on every clean exit path. SIGKILL won't run defers; for
	// that case the launchd/systemd job re-runs the daemon, which calls
	// Stop() on startup (Stop is idempotent and clears any stale state).
	defer srv.sleeper.Stop()

	// Best-effort: tell tmux to forward selections as OSC 52 so
	// cross-device clipboard works through SSH. Fails silently when
	// the tmux server isn't up yet — it'll be retried on first
	// SetActive() poll. Honestly this is fine to do unconditionally:
	// `set -s set-clipboard on` is idempotent and harmless on
	// terminals that ignore OSC 52.
	{
		cctx, ccancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = clipboard.EnableTmuxClipboard(cctx)
		ccancel()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Poll loop.
	go srv.pollLoop(ctx)

	// Unix socket listener.
	sockPath, err := daemon.SocketPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(sockPath), 0o755); err != nil {
		return err
	}
	// Bind safely:
	//
	// The old logic was `os.Remove(sockPath); net.Listen(...)` — a race
	// that let two daemons start ~1s apart both succeed: the second's
	// Remove unlinked the first's socket from the filesystem, but the
	// first listener kept serving on the orphaned inode. The result is
	// the "rogue daemon" we found in the wild: same binary, no
	// requests, but accumulating heap from its background poll loop
	// because it can't be reached for `daemon stop`.
	//
	// Fix is to detect a live owner BEFORE removing. Dial the socket
	// with a short timeout. If dial succeeds, another ccmuxd is alive
	// and we refuse to start. If dial fails (no socket file, or stale
	// socket from a crash), it's safe to clean up and bind.
	if isAnotherDaemonAlive(sockPath, 300*time.Millisecond) {
		return fmt.Errorf(
			"another ccmuxd is already listening on %s — stop it first with `ccmux daemon stop`",
			sockPath,
		)
	}
	_ = os.Remove(sockPath)
	unixLn, err := net.Listen("unix", sockPath)
	if err != nil {
		return fmt.Errorf("listen unix %q: %w", sockPath, err)
	}
	if err := os.Chmod(sockPath, 0o600); err != nil {
		return err
	}

	// Unix-socket mux: full surface (tailnet-safe routes + local-only).
	mux := http.NewServeMux()
	srv.routes(mux)
	srv.localOnlyRoutes(mux)
	httpSrv := newHTTPServer(mux)

	go func() {
		if err := httpSrv.Serve(unixLn); err != nil && err != http.ErrServerClosed {
			log.Printf("ccmuxd: unix serve: %v", err)
		}
	}()

	// Optional tailnet listener. Its mux is *separate* and intentionally
	// excludes localOnlyRoutes — a tailnet peer must not be able to mint
	// pair tokens for itself.
	if cfg.Daemon.ListenTailnet {
		if addr, err := tailscaleAddr(cfg.Daemon.TailnetPort); err == nil {
			tailnetMux := http.NewServeMux()
			srv.routes(tailnetMux)
			tailnetSrv := newHTTPServer(tailnetMux)
			tailnetSrv.Addr = addr
			go func() {
				log.Printf("ccmuxd: tailnet listening on %s", addr)
				if err := tailnetSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					log.Printf("ccmuxd: tailnet serve: %v", err)
				}
			}()
		} else {
			log.Printf("ccmuxd: tailnet listener disabled: %v", err)
		}
	}

	log.Printf("ccmuxd: %s ready (socket %s)", version, sockPath)

	// Wait for signal.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("ccmuxd: shutting down")
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutCancel()
	_ = httpSrv.Shutdown(shutCtx)
	return nil
}

// tracked is the per-session daemon state held across poll iterations.
type tracked struct {
	last        string    // last captured pane content (for change detection)
	lastChange  time.Time // when content last changed
	state       agent.State
	promptCount int
	created     time.Time
	// agentID is the AI agent this session is running, sourced from
	// <project>/.ccmux/agent on first sight. Cached so we don't stat
	// the sidecar every poll tick. The classifier for state detection
	// is `agent.ByID(agentID).Classify(…)` — that's what lets Codex
	// and Antigravity sessions get their own heuristics instead of
	// borrowing Claude's box-drawing prompt detector.
	agentID agent.ID
	// projectPath is the working directory of the tmux session, used
	// to resolve the agent sidecar. Captured at session-add time and
	// not re-read on subsequent ticks.
	projectPath string
}

type server struct {
	cfg       config.Config
	startedAt time.Time
	mu        sync.Mutex
	seen      map[string]*tracked
	sleeper   *sleeplock.Manager

	tokens  *daemon.TokenStore
	events  *daemon.EventBus
	sshUser string

	// devices tracks paired iPhones for push routing. apnsSender
	// is the APNs HTTP/2 client; both fields stay non-nil even when
	// push is disabled, so handlers can call them unconditionally.
	devices    *daemon.DeviceStore
	apnsSender *apns.Sender
	// apnsSlots caps the number of concurrent APNs sends so a slow
	// HTTP/2 handshake can't accumulate goroutines on every poll tick.
	// Defaults to 16 — enough headroom for a small fleet of paired
	// phones, small enough that a wedged APNs endpoint applies
	// back-pressure to the poll loop rather than leaking forever.
	apnsSlots chan struct{}

	// moshiState is refreshed periodically (not every poll) so we don't
	// shell out to moshi-hook every 2 seconds. Used only to drive the
	// "moshi reachable" badge in the tmux status bar — the bell rings
	// independently per the always-ring policy.
	moshiState   moshi.Status
	moshiCheckAt time.Time
	moshiMu      sync.Mutex

	// Poll-loop seams. Defaulted by newServer to the real tmux-backed
	// implementations; tests override them to drive pollOnce
	// deterministically without a real pane. capture reads a session's
	// pane content; bell signals a needs-input transition.
	capture func(ctx context.Context, name string, lines int) (string, error)
	bell    func(ctx context.Context, name string) error
}

// newServer builds a server with its default (real, tmux-backed)
// poll-loop seams wired. Both the daemon entrypoint and the
// integration tests construct through here so the seam defaults stay
// in one place.
func newServer(cfg config.Config) *server {
	sshUser := cfg.Daemon.SSHUser
	if sshUser == "" {
		sshUser, _ = os.LookupEnv("USER")
	}
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

	return &server{
		cfg:        cfg,
		seen:       map[string]*tracked{},
		startedAt:  time.Now(),
		capture:    tmux.CapturePane,
		bell:       func(ctx context.Context, name string) error { return tmux.SendKeys(ctx, name, "\a") },
		tokens:     daemon.NewTokenStore(),
		events:     daemon.NewEventBus(),
		sshUser:    sshUser,
		devices:    devices,
		apnsSender: sender,
		apnsSlots:  make(chan struct{}, 16),
	}
}

// routes registers every tailnet-safe endpoint. Anything that an
// unauthenticated tailnet peer is allowed to hit goes here. The Unix
// socket additionally registers localOnlyRoutes; the tailnet listener
// does not.
func (s *server) routes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/health", s.handleHealth)
	mux.HandleFunc("/v1/sessions", s.handleSessions)
	// /v1/sessions/bare lives BEFORE /v1/sessions/ so the more
	// specific route matches first. ServeMux's longest-prefix match
	// would handle this either way, but the explicit ordering makes
	// the relationship obvious to a reader.
	mux.HandleFunc("/v1/sessions/bare", s.createBareSession)
	mux.HandleFunc("/v1/sessions/", s.handleSessionsItem)
	mux.HandleFunc("/v1/projects", s.handleProjects)
	mux.HandleFunc("/v1/pair", s.handlePair)
	mux.HandleFunc("/v1/events", s.handleEvents)
	mux.HandleFunc("/v1/devices", s.handleRegisterDevice)
	mux.HandleFunc("/v1/devices/test", s.handleTestPush)
	mux.HandleFunc("/v1/peers", s.handlePeers)
	mux.HandleFunc("/v1/conversations", s.handleConversations)
	mux.HandleFunc("/v1/usage", s.handleUsage)
	mux.HandleFunc("/v1/notes", s.handleNotes)
}

// localOnlyRoutes registers endpoints that must never be reachable from
// the tailnet. /v1/pair-token mints a one-time pairing token, so a
// tailnet peer that could hit it would just issue itself a token and
// then redeem it on /v1/pair — defeating the whole point of pairing.
// The function exists separately so the tailnet HTTP listener stays
// structurally unable to register these.
func (s *server) localOnlyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/pair-token", s.handlePairToken)
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	host, _ := os.Hostname()
	s.mu.Lock()
	n := len(s.seen)
	s.mu.Unlock()
	writeJSON(w, daemon.HealthInfo{
		OK: true, Hostname: host, Version: version, Sessions: n, SleepMode: string(s.sleeper.Effective()),
	})
}

func (s *server) handleSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listSessions(w, r)
	case http.MethodPost:
		s.createSession(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *server) listSessions(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	// tmux.List shells out to the tmux CLI — slow enough that holding
	// s.mu across it would stall the poll loop, which needs the same
	// lock. Snapshot the session list first, then take the lock only
	// to read the per-session tracked state.
	tss, _ := tmux.List(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]daemon.SessionState, 0, len(tss))
	for _, ts := range tss {
		t, ok := s.seen[ts.Name]
		if !ok {
			t = &tracked{created: ts.Created, state: agent.StateUnknown}
		}
		// Agent is read once at session-add time and cached on `tracked`;
		// for sessions we've seen via the poll loop it's already
		// populated. For pre-existing sessions (e.g. the daemon just
		// started and hasn't tickled the poll loop yet), fall back to
		// reading the sidecar on the fly. Fast — single os.ReadFile.
		agentID := t.agentID
		if agentID == "" {
			agentID = project.ReadAgent(ts.Path)
		}
		out = append(out, daemon.SessionState{
			Name: ts.Name, Host: "local", Path: ts.Path,
			Attached: ts.Attached, Windows: ts.Windows,
			Created: ts.Created, LastChange: t.lastChange,
			State: string(t.state), PromptCount: t.promptCount,
			Agent: string(agentID),
		})
	}
	writeJSON(w, out)
}

// createSession handles POST /v1/sessions: scaffold or attach to a
// project's tmux session running Claude. Idempotent — if the named
// tmux session already exists, returns it without creating a new one.
// The request body is daemon.NewSessionRequest.
func (s *server) createSession(w http.ResponseWriter, r *http.Request) {
	var req daemon.NewSessionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)).Decode(&req); err != nil {
		http.Error(w, "decode: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Project == "" {
		http.Error(w, "project required", http.StatusBadRequest)
		return
	}
	path := req.Path
	if path == "" {
		home, _ := os.UserHomeDir()
		root := s.cfg.Projects.Root
		if root == "" {
			root = filepath.Join(home, "Projects")
		}
		path = filepath.Join(root, req.Project)
	}
	if _, err := os.Stat(path); err != nil {
		http.Error(w, "project path not found: "+path, http.StatusNotFound)
		return
	}

	// Caller-supplied name wins; same rule createBareSession uses to
	// keep names safe for `tmux new-session -s`.
	var session string
	if name := strings.TrimSpace(req.Name); name != "" {
		if strings.ContainsAny(name, "/\\:") {
			http.Error(w, "name must not contain /, \\, or :", http.StatusBadRequest)
			return
		}
		session = name
	} else {
		session = tmux.SessionNameForPath(path)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Caller-supplied agent persists to .ccmux/agent so the launch
	// command (read via project.ReadAgent) and future attaches all
	// pick the same one. Invalid agent strings are ignored — the
	// sidecar then keeps its current value (or stays unset → Claude).
	if a := strings.TrimSpace(req.Agent); a != "" {
		if id, ok := agent.ParseID(a); ok {
			_ = project.SetAgent(path, id)
		}
	}

	has, herr := tmux.Has(ctx, session)
	if herr != nil {
		http.Error(w, "tmux has-session: "+herr.Error(), http.StatusInternalServerError)
		return
	}
	if !has {
		// Launch the agent recorded in the project's sidecar (or the
		// one the request explicitly named). This used to hardcode
		// "claude --continue || claude || zsh" regardless, which
		// meant Codex / Antigravity projects launched claude from
		// remote starts.
		launch := projectLaunchCmd(path, req.Continue, s.cfg.AgentCommands())
		if err := tmux.New(ctx, session, path, launch); err != nil {
			http.Error(w, "tmux new-session: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	// Apply chrome on every reachable-via-remote session, whether we
	// just created it or it's a known one being re-attached. Idempotent
	// — re-running set-option just overwrites the same string.
	s.applyChrome(ctx, session, req.Project)

	writeJSON(w, daemon.SessionState{
		Name: session, Host: "local", Project: req.Project, Path: path,
		State: string(agent.StateUnknown), Created: time.Now(),
	})
}

// createBareSession handles POST /v1/sessions/bare — a shell-only
// session not tied to any project, no agent, no scaffold. Used by
// the Sessions tab's "new session" form for ad-hoc work on any
// device.
//
// Path defaults to the daemon's configured sessions.default_dir,
// falling back to $HOME on the daemon's machine. We never resolve
// to the client's home — the whole point is "shell on the remote
// machine in that machine's home".
//
// Idempotent: if `Name` already exists as a tmux session, return it
// without re-creating. Catches the case where the form's auto-
// generated name happens to collide with a leftover.
func (s *server) createBareSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req daemon.NewBareSessionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)).Decode(&req); err != nil {
		http.Error(w, "decode: "+err.Error(), http.StatusBadRequest)
		return
	}
	path := resolveBarePath(req.Path, s.cfg.Sessions.DefaultDir)
	if _, err := os.Stat(path); err != nil {
		http.Error(w, "path not found on this host: "+path, http.StatusNotFound)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		// AutoSessionName appends an atomic counter so two bare-session
		// requests in the same millisecond can't be handed the same
		// name (which the idempotent has-session check below would then
		// silently collapse into one session).
		name = tmux.AutoSessionName("c-shell")
	}
	// Reject obviously-bad names — the same rule createProject uses,
	// for the same reason (we'll pass it to tmux as -s).
	if strings.ContainsAny(name, "/\\:") {
		http.Error(w, "name must not contain /, \\, or :", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	has, herr := tmux.Has(ctx, name)
	if herr != nil {
		http.Error(w, "tmux has-session: "+herr.Error(), http.StatusInternalServerError)
		return
	}
	if !has {
		// Order: explicit request agent → daemon's
		// sessions.default_agent → $SHELL. Bare sessions don't carry
		// --continue because they're not tied to a project transcript.
		launch := bareSessionLaunchCmd(req.Agent, s.cfg.Agents.Default, s.cfg.AgentCommands())
		if err := tmux.New(ctx, name, path, launch); err != nil {
			http.Error(w, "tmux new-session: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	// Chrome the new session so when the client ssh-attaches it
	// lands in a ccmux-styled bar. The "project label" for bare
	// sessions is the basename of the working dir — gives the user
	// something readable in the status bar.
	s.applyChrome(ctx, name, filepath.Base(path))

	host, _ := os.Hostname()
	writeJSON(w, daemon.NewBareSessionResponse{
		Session: name,
		Path:    path,
		Host:    host,
	})
}

// projectLaunchCmd resolves the launch command for a project's tmux
// session from its .ccmux/agent sidecar. Pure helper so a test can
// pin "Antigravity project → agy launch" without standing up tmux.
//
// continueFlag=true matches the existing UX: every "attach to known
// project" path passes --continue so the user resumes their prior
// conversation; only fresh scaffolds start without --continue.
func projectLaunchCmd(projectPath string, continueFlag bool, commands agent.Commands) string {
	return agent.LaunchCmd(project.ReadAgent(projectPath), continueFlag, commands)
}

// bareSessionLaunchCmd resolves which command tmux new-session runs
// inside a new bare session. Precedence:
//
//  1. explicit request agent — the picker selection or
//     `ccmux shell --agent`. The literal "shell" short-circuits to
//     $SHELL so a conscious "no agent" pick isn't second-guessed by
//     the config default.
//  2. daemon's sessions.default_agent config (same rules).
//  3. $SHELL (or /bin/sh if $SHELL is unset).
//
// IDs are normalized via agent.ParseID so the daemon accepts the
// "gemini" back-compat alias. Exposed for tests so the precedence is
// pinned without standing up an http server.
func bareSessionLaunchCmd(reqAgent, configDefault string, commands agent.Commands) string {
	if cmd := agentLaunchCmdOrShell(reqAgent, false, commands); cmd != "" {
		return cmd
	}
	if cmd := agentLaunchCmdOrShell(configDefault, false, commands); cmd != "" {
		return cmd
	}
	return shellLaunchCmd()
}

// agentLaunchCmdOrShell decodes a single agent-id-or-"shell" string.
// Returns the LaunchCmd for a known agent, the shell command for an
// explicit "shell" pick, and "" for an empty or unrecognized value so
// the caller can fall through to the next precedence level.
func agentLaunchCmdOrShell(s string, continueFlag bool, commands agent.Commands) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return ""
	}
	if strings.EqualFold(trimmed, "shell") {
		return shellLaunchCmd()
	}
	if id, ok := agent.ParseID(trimmed); ok {
		return agent.LaunchCmd(id, continueFlag, commands)
	}
	return ""
}

// shellLaunchCmd is the bare-shell escape hatch.
func shellLaunchCmd() string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	return shell
}

// resolveBarePath picks the working directory for a bare session.
// Order: explicit req.Path → daemon's configured DefaultDir → $HOME.
// Exported as a helper so the unit tests can pin the priority.
func resolveBarePath(reqPath, configDefault string) string {
	for _, candidate := range []string{reqPath, configDefault} {
		if c := strings.TrimSpace(candidate); c != "" {
			return expandTilde(c)
		}
	}
	home, _ := os.UserHomeDir()
	return home
}

// expandTilde rewrites a leading "~/" to the daemon's $HOME. Bare-
// path strings come straight from config.toml and the wire; users
// expect "~/foo" to mean the daemon's home, not the client's. Other
// shell expansions ($VAR, *, …) are deliberately NOT handled —
// that's a recipe for surprises in a daemon process.
func expandTilde(p string) string {
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// applyChrome wraps tmuxchrome.Apply with the daemon-side defaults:
//
//   - moshiReachable is sourced from the cached moshiState so the badge
//     reflects whether THIS host's phone pipeline is wired.
//   - nested=false because the daemon never runs from inside a tmux
//     session (it's a background launchd/systemd job).
//
// Errors are deliberately swallowed: a partial or missing chrome is
// strictly cosmetic, and the user's friend reporting "the remote
// session had no ccmux chrome" should never become "the remote session
// failed to start because chrome failed".
func (s *server) applyChrome(ctx context.Context, session, projectLabel string) {
	s.moshiMu.Lock()
	moshiReachable := s.moshiState.Paired && s.moshiState.HooksInstalled && s.moshiState.ServiceRunning
	s.moshiMu.Unlock()
	_ = tmuxchrome.Apply(ctx, session, projectLabel, moshiReachable, false)
}

func (s *server) handleSessionsItem(w http.ResponseWriter, r *http.Request) {
	// /v1/sessions/<name>[/<subaction>]
	tail := strings.TrimPrefix(r.URL.Path, "/v1/sessions/")
	parts := strings.SplitN(tail, "/", 2)
	name := parts[0]
	if name == "" {
		http.Error(w, "session name required", http.StatusBadRequest)
		return
	}
	if len(parts) == 1 {
		http.Error(w, "subaction required", http.StatusBadRequest)
		return
	}
	switch parts[1] {
	case "kill":
		s.handleKill(w, r, name)
	case "rename":
		s.handleRename(w, r, name)
	case "send-keys":
		s.handleSendKeys(w, r, name)
	case "preview":
		s.handlePreview(w, r, name)
	case "attach":
		s.handleAttach(w, r, name)
	default:
		http.Error(w, "unknown subaction", http.StatusNotFound)
	}
}

func (s *server) handleKill(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := tmux.Kill(ctx, name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.mu.Lock()
	delete(s.seen, name)
	s.mu.Unlock()
	s.events.Publish(daemon.SessionEvent{At: time.Now(), Kind: "killed", Session: daemon.SessionState{Name: name, Host: "local"}})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleRename(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req daemon.RenameRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)).Decode(&req); err != nil || req.Name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	// Same rule createSession/createBareSession enforce: tmux interprets
	// `name:window.pane` as a target spec, so a rename to "victim:0"
	// would let later send-keys land in an unrelated tmux session.
	if strings.ContainsAny(req.Name, "/\\:") {
		http.Error(w, "name must not contain /, \\, or :", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := tmux.Rename(ctx, name, req.Name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.mu.Lock()
	if t, ok := s.seen[name]; ok {
		s.seen[req.Name] = t
		delete(s.seen, name)
	}
	s.mu.Unlock()
	writeJSON(w, daemon.SessionState{Name: req.Name, Host: "local"})
}

func (s *server) handleSendKeys(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req daemon.SendKeysRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)).Decode(&req); err != nil || req.Keys == "" {
		http.Error(w, "keys required", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := tmux.SendKeys(ctx, name, req.Keys); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleNotes serves both list and read for a project's markdown
// vault — `?project=<name>` returns the list, `&file=<rel>` returns
// the contents of one file.
//
// Security: project is resolved against the daemon's configured
// Projects.Root via project.Discover (same source as the dashboard),
// so a caller can only ever reference projects ccmux already lists.
// The file query is path-traversal-validated below — clients can't
// reach outside the project root via "../" or absolute paths.
func (s *server) handleNotes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("project"))
	if name == "" {
		http.Error(w, "project query required", http.StatusBadRequest)
		return
	}
	projs, err := project.Discover(s.cfg.Projects.Root)
	if err != nil {
		http.Error(w, "discover: "+err.Error(), http.StatusInternalServerError)
		return
	}
	var proj project.Project
	var found bool
	for _, p := range projs {
		if p.Name == name {
			proj = p
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}
	vault := notes.Open(proj.Path)

	if rel := strings.TrimSpace(r.URL.Query().Get("file")); rel != "" {
		// Path-traversal hardening: reject absolute paths, ".." segments,
		// and anything that isn't a .md file. notes.Vault.Read trusts
		// its input, so we validate here.
		if strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) {
			http.Error(w, "file must be a project-relative path", http.StatusBadRequest)
			return
		}
		cleaned := filepath.ToSlash(filepath.Clean(rel))
		if strings.HasPrefix(cleaned, "..") || strings.Contains(cleaned, "/../") {
			http.Error(w, "path traversal not allowed", http.StatusBadRequest)
			return
		}
		if !strings.HasSuffix(strings.ToLower(cleaned), ".md") {
			http.Error(w, "only .md files are served", http.StatusBadRequest)
			return
		}
		body, err := vault.Read(cleaned)
		if err != nil {
			if os.IsNotExist(err) {
				http.Error(w, "file not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, daemon.NoteContent{Rel: cleaned, Content: string(body)})
		return
	}

	entries, err := vault.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]daemon.NoteEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, daemon.NoteEntry{
			Rel:      e.Rel,
			Dir:      e.Dir,
			Display:  e.Display,
			Modified: e.Modified,
		})
	}
	writeJSON(w, out)
}

// handlePreview returns the last N lines of the session's active pane
// as plain text. Read-only — the daemon's poll loop captures the pane
// every few seconds anyway, so this just adds a "give me current
// content" hook for clients that don't want to open the WebSocket
// PTY just to take a peek. Used by the iOS app's session detail view
// to show "what's on screen right now" without committing to a full
// terminal attach.
func (s *server) handlePreview(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	lines := 24
	if q := r.URL.Query().Get("lines"); q != "" {
		var n int
		if _, err := fmt.Sscanf(q, "%d", &n); err == nil && n > 0 && n <= 200 {
			lines = n
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	out, err := tmux.CapturePane(ctx, name, lines)
	if err != nil {
		// tmux returns a non-zero exit when the session is gone; map to
		// 404 so clients can distinguish "no session" from other
		// errors without parsing stderr.
		if strings.Contains(err.Error(), "can't find session") ||
			strings.Contains(err.Error(), "no current session") {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, daemon.PreviewResponse{Lines: lines, Content: out})
}

func (s *server) handlePairToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Pairing produces a URL the phone POSTs back to over the tailnet.
	// If the tailnet listener was never started, the minted URL points
	// at a port nothing is listening on — the phone gets a confusing
	// "connection refused" with no hint that daemon.listen_tailnet is
	// off. Fail loudly here instead.
	if !s.cfg.Daemon.ListenTailnet {
		http.Error(w,
			"tailnet listener disabled (set daemon.listen_tailnet=true in config.toml and restart ccmuxd)",
			http.StatusServiceUnavailable)
		return
	}
	token, err := s.tokens.Create(5 * time.Minute)
	if err != nil {
		http.Error(w, "generate token: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// Build the ccmux:// deep-link URL.
	host, _ := os.Hostname()
	tailIP, _ := tailscaleAddr(s.cfg.Daemon.TailnetPort)
	// Use MagicDNS hostname if available, otherwise tailscale IP.
	pairHost := host
	if tailIP != "" {
		// tailIP is "IP:port"; strip the port
		if idx := strings.LastIndex(tailIP, ":"); idx >= 0 {
			pairHost = tailIP[:idx]
		}
	}
	sshUser := s.cfg.Daemon.SSHUser
	if sshUser == "" {
		sshUser, _ = os.LookupEnv("USER")
	}
	pairURL := fmt.Sprintf("ccmux://pair?host=%s&user=%s&port=%d&token=%s",
		pairHost, sshUser, s.cfg.Daemon.TailnetPort, token)
	writeJSON(w, daemon.PairTokenResponse{Token: token, URL: pairURL})
}

func (s *server) handlePair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req daemon.PairRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)).Decode(&req); err != nil {
		http.Error(w, "decode: "+err.Error(), http.StatusBadRequest)
		return
	}
	// Validate the public key BEFORE consuming the token so a malformed
	// key doesn't burn the user's single-use pair token. Returns a
	// canonical single-line authorized_keys entry (any pre-key options
	// the client tried to smuggle in are stripped during re-serialize).
	authLine, err := validatePairKey(req.PublicKey)
	if err != nil {
		http.Error(w, "invalid public key: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !s.tokens.Consume(req.Token) {
		http.Error(w, "invalid or expired token", http.StatusUnauthorized)
		return
	}
	if err := appendAuthorizedKey(authLine); err != nil {
		http.Error(w, "write authorized_keys: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// Optional APNs registration carried with the pair — lets push
	// work from first pair without a second round trip. Failures
	// (no device store, bad env) log and continue; pairing itself
	// has already succeeded.
	if s.devices != nil && req.DeviceToken != "" {
		if err := s.devices.Register(authLine, req.DeviceToken, req.APNsEnv); err != nil {
			log.Printf("ccmuxd: pair-time device register: %v", err)
		}
	}
	host, _ := os.Hostname()
	writeJSON(w, daemon.PairResponse{Hostname: host, Version: version})
}

// handleRegisterDevice updates an APNs device token on an already-
// paired host. The client supplies the public key it paired with so
// the daemon can scope the update to the right paired identity.
func (s *server) handleRegisterDevice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.devices == nil {
		http.Error(w, "device registry unavailable", http.StatusServiceUnavailable)
		return
	}
	var req daemon.RegisterDeviceRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)).Decode(&req); err != nil {
		http.Error(w, "decode: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.devices.Register(req.PublicKey, req.Token, req.Env); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleTestPush sends a verification push to the device registered
// for the requesting public key. Returns 204 even when APNs is
// disabled (the request is well-formed; the user just hasn't flipped
// the switch yet) so the mobile UI can give an honest "sent"
// confirmation. The detailed status appears in ccmuxd's log.
func (s *server) handleTestPush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.devices == nil {
		http.Error(w, "device registry unavailable", http.StatusServiceUnavailable)
		return
	}
	var body struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)).Decode(&body); err != nil || body.PublicKey == "" {
		http.Error(w, "public_key required", http.StatusBadRequest)
		return
	}
	reg, ok := s.devices.Lookup(body.PublicKey)
	if !ok {
		http.Error(w, "no device registered for this key", http.StatusNotFound)
		return
	}
	if !s.apnsSender.Enabled() {
		log.Printf("ccmuxd: test push requested but APNs disabled")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.sendAPNsAsync("test-push", reg.Token, reg.Environment, apns.Notification{
		Title:     "ccmux test push",
		Body:      "If you see this, push notifications are working.",
		SessionID: "ccmux-test",
	})
	w.WriteHeader(http.StatusNoContent)
}

// sendAPNsAsync dispatches one push on a bounded worker pool. If the
// pool is saturated (16 concurrent sends — usually means APNs is
// stalled or down) the call drops the notification and logs, rather
// than spawning an unbounded goroutine that may never return.
func (s *server) sendAPNsAsync(label, token, env string, n apns.Notification) {
	select {
	case s.apnsSlots <- struct{}{}:
	default:
		log.Printf("ccmuxd: APNs %s (%s): dropped — sender saturated", label, n.SessionID)
		return
	}
	go func() {
		defer func() { <-s.apnsSlots }()
		if err := s.apnsSender.Send(token, env, n); err != nil {
			log.Printf("ccmuxd: APNs %s (%s): %v", label, n.SessionID, err)
		}
	}()
}

// handlePeers returns every tailnet peer plus an indication of which
// ones already run ccmuxd. Used by clients (iOS Settings → Add host)
// to populate a "your other Macs on the tailnet" picker without each
// needing tailscale-CLI access themselves. Probe budget is intentional:
// ScanTailnet pings each peer's /v1/health in parallel with a 1s
// deadline, so even a tailnet of dozens settles in ~1s.
func (s *server) handlePeers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	scan, err := tailnet.ScanTailnet(ctx, s.cfg.Daemon.TailnetPort)
	if err != nil {
		// Tailscale not installed / not running is a normal state on
		// some hosts — return an empty list rather than 500 so clients
		// can show "no peers found" instead of an error toast.
		log.Printf("ccmuxd: scan tailnet: %v", err)
		writeJSON(w, []daemon.PeerInfo{})
		return
	}
	out := make([]daemon.PeerInfo, 0, len(scan.Reachable)+len(scan.NeedsInstall)+len(scan.Mobile))
	port := s.cfg.Daemon.TailnetPort
	if port == 0 {
		port = 7474
	}
	for _, d := range scan.Reachable {
		// Address is "ip:port" — split for the client.
		ip, _, _ := strings.Cut(d.Address, ":")
		p := port
		out = append(out, daemon.PeerInfo{
			Hostname: d.Name, Addr: ip, OS: "macOS",
			Online: true, RunsCCMuxd: true, Port: &p,
		})
	}
	for _, peer := range scan.NeedsInstall {
		out = append(out, daemon.PeerInfo{
			Hostname: peer.DisplayName(), Addr: peer.Addr, OS: peer.OS,
			Online: peer.Online, RunsCCMuxd: false,
		})
	}
	for _, peer := range scan.Mobile {
		out = append(out, daemon.PeerInfo{
			Hostname: peer.DisplayName(), Addr: peer.Addr, OS: peer.OS,
			Online: peer.Online, RunsCCMuxd: false,
		})
	}
	writeJSON(w, out)
}

// handleUsage returns per-agent token + cost activity over a rolling
// window (default 5 hours, override via ?window=2h, 24h, 30m, …). The
// walkers are best-effort: a missing or corrupt transcript on one
// agent doesn't sink the others. iOS uses this for its dashboard
// usage card.
func (s *server) handleUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	window := 5 * time.Hour
	if q := r.URL.Query().Get("window"); q != "" {
		if d, err := time.ParseDuration(q); err == nil && d > 0 {
			window = d
		}
	}
	// Each walker is cheap and IO-bound; run them concurrently so a
	// slow disk doesn't serialize three reads of ~the same fs subtree.
	var (
		wg                 sync.WaitGroup
		claude, codex, ant usage.AgentSummary
	)
	wg.Add(3)
	go func() { defer wg.Done(); claude, _ = usage.WalkClaude(window) }()
	go func() { defer wg.Done(); codex, _ = usage.WalkCodex(window) }()
	go func() { defer wg.Done(); ant, _ = usage.WalkAntigravity(window) }()
	wg.Wait()
	writeJSON(w, daemon.AgentUsage{
		Claude:      toUsageSummary(claude),
		Codex:       toUsageSummary(codex),
		Antigravity: toUsageSummary(ant),
	})
}

func toUsageSummary(s usage.AgentSummary) daemon.UsageSummary {
	return daemon.UsageSummary{
		HasData:       s.HasData,
		WindowSeconds: int(s.Window / time.Second),
		Prompts:       s.Prompts,
		InputTokens:   s.InputTokens,
		OutputTokens:  s.OutputTokens,
		EstimatedCost: s.EstimatedCost,
	}
}

// handleConversations returns past agent transcripts (Claude, Codex,
// Antigravity) from the daemon's home directory. Sorted most-recent
// first; clients can do their own filtering. Headless / SDK runs are
// excluded by default — they pile up fast in automation and aren't
// usually what a user means by "my conversations".
func (s *server) handleConversations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	convos, err := conversations.All(conversations.Options{ExcludeHeadless: true})
	if err != nil {
		// Best-effort: even if one walker failed, others may have
		// returned rows. conversations.All only errors when ALL walkers
		// failed, which usually means home dir resolution broke.
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]daemon.Conversation, 0, len(convos))
	for _, c := range convos {
		out = append(out, daemon.Conversation{
			ID: c.ID, Agent: string(c.Agent), Project: c.Project,
			Path: c.Path, Preview: c.Preview, Modified: c.LastActivity,
		})
	}
	writeJSON(w, out)
}

// maybePushForStateTransition fires an APNs push when a tracked
// session enters a state the user should know about: needs_input
// (Y/N from the agent) or active → idle (the agent finished its
// response and is waiting for the next prompt). No-op when push is
// disabled or no devices are registered.
func (s *server) maybePushForStateTransition(sessionName string, prev, next agent.State) {
	if s.devices == nil || !s.apnsSender.Enabled() {
		return
	}
	var title, body string
	switch {
	case next == agent.StateNeedsInput:
		title = sessionName + " needs input"
		body = "Tap to reply."
	case next == agent.StateIdle && prev == agent.StateActive:
		title = sessionName + " finished"
		body = "Your agent is waiting for the next prompt."
	default:
		return
	}
	notif := apns.Notification{
		Title:     title,
		Body:      body,
		SessionID: "local/" + sessionName,
	}
	for _, reg := range s.devices.All() {
		s.sendAPNsAsync("push", reg.Token, reg.Environment, notif)
	}
}

func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	ch := s.events.Subscribe()
	defer s.events.Unsubscribe(ch)
	enc := json.NewEncoder(w)
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, open := <-ch:
			if !open {
				return
			}
			fmt.Fprintf(w, "data: ")
			_ = enc.Encode(ev)
			fmt.Fprintf(w, "\n")
			flusher.Flush()
		}
	}
}

// validatePairKey parses the wire-format public key into a canonical
// authorized_keys line. Rejects:
//   - empty / whitespace-only input
//   - anything that isn't a parseable SSH public key
//   - extra data on the line (pre-key options like `command="rm -rf /"`,
//     `from="…"`, `no-pty`, etc.) — these would otherwise let a paired
//     peer install an unconstrained backdoor as a side effect of pairing
//   - extra lines (a peer that smuggles `<valid-key>\n<malicious-key>\n`)
//
// Re-serialize via MarshalAuthorizedKey to produce a canonical
// `<type> <base64>` line, dropping any comment and any options the
// caller may have prepended.
func validatePairKey(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("empty")
	}
	pub, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(s))
	if err != nil {
		return "", err
	}
	if len(options) > 0 {
		return "", errors.New("pre-key options are not allowed")
	}
	if strings.TrimSpace(string(rest)) != "" {
		return "", errors.New("multi-line input is not allowed")
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub))), nil
}

func appendAuthorizedKey(pubKey string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return err
	}
	authKeys := filepath.Join(sshDir, "authorized_keys")
	f, err := os.OpenFile(authKeys, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	key := strings.TrimSpace(pubKey) + "\n"
	_, err = f.WriteString(key)
	return err
}

// handleProjects routes /v1/projects:
//
//	GET — list discovered projects under the daemon's Projects.Root
//	POST — create a new project on this host (its directory + an agent
//	       session); body is daemon.NewProjectRequest
//
// POST exists so the laptop's Projects screen can ask `mac-mini` to
// create a project natively, instead of trying to ssh + tmux new over
// the wire. It creates only the directory — no CLAUDE.md / docs/ / git.
func (s *server) handleProjects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, "":
		s.listProjects(w, r)
	case http.MethodPost:
		s.createProject(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *server) listProjects(w http.ResponseWriter, _ *http.Request) {
	host, _ := os.Hostname()
	root := s.cfg.Projects.Root
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, "Projects")
	}
	ps, _ := project.Discover(root)
	out := make([]daemon.ProjectInfo, 0, len(ps))
	for _, p := range ps {
		out = append(out, daemon.ProjectInfo{
			Name: p.Name, Host: host, Path: p.Path,
			HasGit: p.HasGit, HasCM: p.HasCM, HasDocs: p.HasDocs,
			Agent: string(p.Agent), Modified: p.Modified,
		})
	}
	writeJSON(w, out)
}

// createProject creates a new project — its directory plus an agent
// session — on this host. The directory is placed under the daemon's
// Projects.Root and the session is named via tmux.SessionNameForPath
// so the client can ssh-attach directly. It creates only the
// directory; no CLAUDE.md, no docs/ tree, no git init.
func (s *server) createProject(w http.ResponseWriter, r *http.Request) {
	var req daemon.NewProjectRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)).Decode(&req); err != nil {
		http.Error(w, "decode: "+err.Error(), http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	// Reject anything that would escape the Projects.Root: no slashes,
	// no `..`, no leading dots. The daemon is the security boundary
	// here — a malicious tailnet peer could otherwise create
	// directories at arbitrary paths.
	if strings.ContainsAny(name, "/\\") || strings.HasPrefix(name, ".") {
		http.Error(w, "name must be a single non-hidden path segment", http.StatusBadRequest)
		return
	}
	root := s.cfg.Projects.Root
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, "Projects")
	}
	dir := filepath.Join(root, name)

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// ParseID returns ok=false on empty + unknown strings, which we
	// treat the same: an empty/typo'd Agent just defers to the
	// claude-default on read via project.ReadAgent.
	chosenAgent, _ := agent.ParseID(req.Agent)
	session, err := scaffold.StartSession(ctx, scaffold.Options{
		Name:     name,
		Dir:      dir,
		Agent:    chosenAgent,
		Commands: s.cfg.AgentCommands(),
	})
	if err != nil {
		http.Error(w, "start: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// Apply ccmux chrome on the session before the client ssh-attaches.
	// Without this the remote tmux looks like plain stock tmux instead
	// of a ccmux-managed session — no project label in the status bar,
	// no detach hint, no moshi badge.
	s.applyChrome(ctx, session, name)

	host, _ := os.Hostname()
	writeJSON(w, daemon.NewProjectResponse{
		Session: session,
		Path:    dir,
		Host:    host,
	})
}

// pollLoop is the heartbeat: capture-pane on each tmux session, derive
// state, and trigger bell when transitioning to NEEDS_INPUT.
func (s *server) pollLoop(ctx context.Context) {
	interval := time.Duration(s.cfg.Daemon.PollIntervalSeconds) * time.Second
	idleNeeds := time.Duration(s.cfg.Daemon.IdleSecondsForNeedsInput) * time.Second
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.pollOnce(ctx, idleNeeds)
		}
	}
}

func (s *server) pollOnce(ctx context.Context, idleNeeds time.Duration) {
	tss, err := tmux.List(ctx)
	if err != nil {
		return
	}
	// Keep the moshi state cache warm — it drives the tmux status-bar
	// "moshi reachable" badge in applyChrome. The result is no longer
	// used for the bell decision: ccmux rings the bell on every
	// needs_input transition when Notifications.Bell is true, and the
	// phone push (if any) fires alongside it.
	s.refreshMoshiStateCached(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()
	live := map[string]bool{}
	anyActive := false
	for _, ts := range tss {
		live[ts.Name] = true
		t, ok := s.seen[ts.Name]
		if !ok {
			t = &tracked{
				created:     ts.Created,
				lastChange:  time.Now(),
				state:       agent.StateUnknown,
				agentID:     project.ReadAgent(ts.Path),
				projectPath: ts.Path,
			}
			s.seen[ts.Name] = t
			s.events.Publish(daemon.SessionEvent{
				At:   time.Now(),
				Kind: "created",
				Session: daemon.SessionState{
					Name: ts.Name, Host: "local", State: string(agent.StateUnknown),
					Path: ts.Path,
				},
			})
		}
		pane, err := s.capture(ctx, ts.Name, 60)
		if err != nil {
			// A capture failure (pane closing mid-poll, tmux busy)
			// must not be swallowed silently — log it. The session's
			// state is left as last classified rather than blanked,
			// since the failure is usually transient.
			log.Printf("ccmuxd: capture-pane %s: %v", ts.Name, err)
			continue
		}
		if pane != t.last {
			t.last = pane
			t.lastChange = time.Now()
		}
		// Per-session agent dispatch. ByID is the unchecked path; we
		// fed it via project.ReadAgent which always returns a valid id
		// (defaulting to claude on missing/garbage). The Classify
		// signature is uniform across agents — a string pane + the
		// lastChange/idle threshold pair — so the switch is invisible
		// from this call site's perspective.
		newState := agent.ByID(t.agentID).Classify(pane, t.lastChange, idleNeeds)
		// Transition into NEEDS_INPUT triggers the bell. Always-ring
		// policy: the BEL fires whenever notifications.bell is true,
		// independent of moshi-hook. The two notification channels
		// are complementary (audible chime at the laptop, push on
		// your phone); duplicate-suppression was a knob that hid the
		// laptop signal even when the user was at the laptop.
		if newState == agent.StateNeedsInput && t.state != agent.StateNeedsInput {
			if s.cfg.Notifications.Bell {
				_ = s.bell(ctx, ts.Name)
			}
			t.promptCount++
		}
		prevState := t.state
		t.state = newState
		if newState != prevState {
			kind := "state_change"
			if newState == agent.StateNeedsInput {
				kind = "needs_input"
			}
			s.events.Publish(daemon.SessionEvent{
				At:   time.Now(),
				Kind: kind,
				Session: daemon.SessionState{
					Name: ts.Name, Host: "local", State: string(newState),
					Path: ts.Path,
				},
			})
			// Push notifications to paired phones — needs_input
			// (agent paused for Y/N) and active → idle (agent
			// finished its response). Off when APNs is disabled or
			// no devices are registered.
			s.maybePushForStateTransition(ts.Name, prevState, newState)
		}
		if newState == agent.StateActive {
			anyActive = true
		}
	}
	// Garbage-collect tracked entries for sessions that no longer exist.
	for name := range s.seen {
		if !live[name] {
			delete(s.seen, name)
		}
	}
	// Sleep manager reacts to the boolean "any session active?".
	s.sleeper.SetActive(anyActive)
}

// refreshMoshiStateCached keeps the moshi.Status cache warm for the
// tmux status-bar badge. Cached for 60s so we don't shell out to
// moshi-hook every 2-second poll tick. The cache is consumed by
// applyChrome — the bell decision itself ignores it (always-ring).
func (s *server) refreshMoshiStateCached(ctx context.Context) {
	s.moshiMu.Lock()
	defer s.moshiMu.Unlock()
	if time.Since(s.moshiCheckAt) > 60*time.Second {
		s.moshiState = moshi.Detect(ctx)
		s.moshiCheckAt = time.Now()
	}
}

// startSleepManager constructs the sleeplock.Manager from config. The
// backward-compat shim: if Mode is empty AND the legacy
// DangerousKeepAwakeOnBattery flag is true, we treat that as
// Mode="dangerous". The legacy flag is otherwise honored only as the
// "off" interpretation for safe.
func (s *server) startSleepManager() {
	modeStr := s.cfg.Sleep.Mode
	if modeStr == "" && s.cfg.Sleep.DangerousKeepAwakeOnBattery {
		modeStr = "dangerous"
	}
	cutoff := s.cfg.Sleep.LowBatteryCutoff
	if cutoff <= 0 {
		cutoff = 20
	}
	s.sleeper = sleeplock.NewManager(sleeplock.ParseMode(modeStr), cutoff)
	log.Printf("ccmuxd: sleep manager initialized (mode=%s, low_battery_cutoff=%d%%)",
		s.sleeper.Requested(), cutoff)
}

// tailscaleAddr returns "<tailscale_ip>:<port>" if Tailscale is running, else error.
func tailscaleAddr(port int) (string, error) {
	out, err := exec.Command("tailscale", "ip", "-4").Output()
	if err != nil {
		return "", err
	}
	ip := string(out)
	for _, c := range []byte{'\n', '\r', ' ', '\t'} {
		if i := indexByte(ip, c); i >= 0 {
			ip = ip[:i]
		}
	}
	if ip == "" {
		return "", fmt.Errorf("tailscale ip -4 returned empty")
	}
	return fmt.Sprintf("%s:%d", ip, port), nil
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// newHTTPServer returns an *http.Server with timeouts set. zero-value
// timeouts let a tailnet peer hold a TCP connection open forever
// (slow-loris) or stall a handler reading from a half-open body — both
// of which leak Server goroutines for the daemon's lifetime. The
// values are generous enough to cover handleAttach's long-lived
// websocket and handleEvents's SSE stream (both opt out via
// per-request hijacking / streaming flush; ReadHeaderTimeout still
// applies to the initial request line).
func newHTTPServer(h http.Handler) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// Bodies are read inside handlers; per-handler context timeouts
		// (5s on most write paths) bound how long a Decode can stall.
	}
}
