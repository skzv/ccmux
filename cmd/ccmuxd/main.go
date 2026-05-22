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

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/clipboard"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/moshi"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/scaffold"
	"github.com/skzv/ccmux/internal/sleeplock"
	"github.com/skzv/ccmux/internal/tmux"
	"github.com/skzv/ccmux/internal/tmuxchrome"
)

var version = "dev"

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

	mux := http.NewServeMux()
	srv.routes(mux)
	httpSrv := &http.Server{Handler: mux}

	go func() {
		if err := httpSrv.Serve(unixLn); err != nil && err != http.ErrServerClosed {
			log.Printf("ccmuxd: unix serve: %v", err)
		}
	}()

	// Optional tailnet listener.
	if cfg.Daemon.ListenTailnet {
		if addr, err := tailscaleAddr(cfg.Daemon.TailnetPort); err == nil {
			go func() {
				log.Printf("ccmuxd: tailnet listening on %s", addr)
				_ = http.ListenAndServe(addr, mux)
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
	keepAwake   bool
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
	return &server{
		cfg:       cfg,
		seen:      map[string]*tracked{},
		startedAt: time.Now(),
		capture:   tmux.CapturePane,
		bell:      func(ctx context.Context, name string) error { return tmux.SendKeys(ctx, name, "\a") },
	}
}

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
			State: string(t.state), KeepAwake: t.keepAwake, PromptCount: t.promptCount,
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
	session := tmux.SessionNameForPath(path)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

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
		launch := projectLaunchCmd(path, req.Continue)
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
		launch := bareSessionLaunchCmd(req.Agent, s.cfg.Agents.Default)
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
func projectLaunchCmd(projectPath string, continueFlag bool) string {
	return agent.ByID(project.ReadAgent(projectPath)).LaunchCmd(continueFlag)
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
func bareSessionLaunchCmd(reqAgent, configDefault string) string {
	if cmd := agentLaunchCmdOrShell(reqAgent, false); cmd != "" {
		return cmd
	}
	if cmd := agentLaunchCmdOrShell(configDefault, false); cmd != "" {
		return cmd
	}
	return shellLaunchCmd()
}

// agentLaunchCmdOrShell decodes a single agent-id-or-"shell" string.
// Returns the LaunchCmd for a known agent, the shell command for an
// explicit "shell" pick, and "" for an empty or unrecognized value so
// the caller can fall through to the next precedence level.
func agentLaunchCmdOrShell(s string, continueFlag bool) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return ""
	}
	if strings.EqualFold(trimmed, "shell") {
		return shellLaunchCmd()
	}
	if id, ok := agent.ParseID(trimmed); ok {
		return agent.ByID(id).LaunchCmd(continueFlag)
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
	// /v1/sessions/<name>[/<subaction>] — minimal stub.
	http.Error(w, "not implemented in v0.1", http.StatusNotImplemented)
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
			Modified: p.Modified,
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
		Name:  name,
		Dir:   dir,
		Agent: chosenAgent,
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
		t.state = newState
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
