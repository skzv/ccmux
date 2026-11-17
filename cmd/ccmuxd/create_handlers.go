package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tmux"
	"github.com/skzv/ccmux/internal/tmuxchrome"
)

// createSession handles POST /v1/sessions: start or attach to a
// project's tmux session running its agent. The request body is
// daemon.NewSessionRequest.
//
// Idempotent on the session name: when the named session already runs
// in the requested directory, it is returned as it is (its real state,
// agent and path) and nothing is started. When it runs somewhere else
// the request is a name collision and gets 409 — answering 200 with the
// request's project and path described a session that didn't exist,
// and re-chroming it with that project relabelled someone else's
// session. See existingSession.
func (s *server) createSession(w http.ResponseWriter, r *http.Request) {
	var req daemon.NewSessionRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		http.Error(w, "decode: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Project == "" {
		http.Error(w, "project required", http.StatusBadRequest)
		return
	}
	requested, err := requestAgent(req.Agent)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// An explicit path gets the same "~/" expansion bare sessions do
	// (MCP spawn_session passes paths like "~/Projects/foo"). Without
	// one the project name becomes a path under the projects root, so it
	// gets createProject's validation: "../.." used to escape the root.
	path := expandTilde(strings.TrimSpace(req.Path))
	if path == "" {
		if err := project.ValidateName(req.Project); err != nil {
			http.Error(w, "project must be a single non-hidden path segment: "+err.Error(), http.StatusBadRequest)
			return
		}
		path = filepath.Join(project.ResolveRoot(s.cfg.Projects.Root), req.Project)
	}
	if !requireDir(w, path, "project path") {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Caller-supplied name wins; same rule createBareSession uses to
	// keep names safe for `tmux new-session -s`. Without one, the
	// project's own session: c-<folder>, or a path-tagged name when a
	// session called c-<folder> runs in another directory (two projects
	// with the same folder name) — see tmux.ProjectSessionName.
	var session string
	sessionDir := path
	if name := strings.TrimSpace(req.Name); name != "" {
		if badNewSessionName(name) {
			http.Error(w, badNewSessionNameMsg, http.StatusBadRequest)
			return
		}
		session = name
	} else {
		name, running, err := s.projectSession(ctx, path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		session = name
		if running {
			sessionDir = "" // already known to run in path
		}
	}

	if st, done := s.existingSession(ctx, w, session, sessionDir); done {
		if st != nil {
			writeJSON(w, st)
		}
		return
	}
	// Launch the agent the request names, else the one recorded in the
	// project's sidecar. This used to hardcode "claude --continue ||
	// claude || zsh" regardless, which meant Codex / Antigravity
	// projects launched claude from remote starts. The session is tagged
	// with it in the creating tmux call, so the poll loop classifies
	// what actually runs whatever the sidecar says.
	launched, launch := projectLaunchCmd(path, requested, req.Continue, s.freshCommands())
	if err := tmux.NewWithAgent(ctx, session, path, launch, string(launched)); err != nil {
		http.Error(w, "tmux new-session: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !s.confirmStarted(ctx, w, session, agent.ByID(launched).Binary()) {
		return
	}
	// A requested agent persists to .ccmux/agent so future attaches pick
	// the same one — only once it has started (a failed start mustn't
	// switch the project), and only when starting a session: rewriting
	// it for one already running would make the poll loop judge that
	// running agent by another agent's rules.
	if requested != "" {
		if err := project.SetAgent(path, requested); err != nil {
			log.Printf("ccmuxd: set agent for %s: %v", path, err)
		}
	}
	// Chrome the new session so a client ssh-attaching lands in a
	// ccmux-styled bar.
	s.applyChrome(ctx, session, req.Project)

	writeJSON(w, s.startedSessionState(ctx, daemon.SessionState{
		Name: session, Host: "local", Project: req.Project, Path: path,
		State: string(agent.StateUnknown), Agent: string(launched),
		// What GET /v1/sessions reports for a session nobody has had a
		// chance to miss anything in yet.
		Seen: true,
	}))
}

// startedSessionState completes the answer for a session a create
// handler just started: its window count, creation time and attached
// flag as tmux lists them, and — as the poll loop records for a new
// session — its content as last changed when it started. The response
// used to carry windows:0 and a zero last_change, which GET
// /v1/sessions never reports for a live session. If tmux can't list it,
// the session still has the one window it was created with.
func (s *server) startedSessionState(ctx context.Context, st daemon.SessionState) daemon.SessionState {
	st.Created, st.Windows = time.Now(), 1
	if ts, ok, err := s.lookupSession(ctx, st.Name); err == nil && ok {
		if !ts.Created.IsZero() {
			st.Created = ts.Created
		}
		st.Windows = max(ts.Windows, 1)
		st.Attached = ts.Attached
	}
	st.LastChange = st.Created
	s.mu.Lock()
	if t, ok := s.seen[st.Name]; ok && !t.lastChange.IsZero() {
		st.LastChange = t.lastChange // a poll tick got to it first
	}
	s.mu.Unlock()
	return st
}

// requireDir checks that path is an existing directory, answering 404
// (missing) or 400 (not a directory) otherwise. A file path such as
// /etc/passwd used to be accepted: 200 with that path while tmux
// started the pane in $HOME.
func requireDir(w http.ResponseWriter, path, what string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		http.Error(w, what+" not found: "+path, http.StatusNotFound)
		return false
	}
	if !fi.IsDir() {
		http.Error(w, what+" is not a directory: "+path, http.StatusBadRequest)
		return false
	}
	return true
}

// existingSession handles a create request whose session name may
// already be taken. done=false: no such session, go ahead and create
// it. done=true with a state: it exists in dir, so the create is a
// no-op and that state is the answer. done=true without one: the
// response (500 on a tmux failure, 409 when the session runs in a
// different directory) has been written. dir "" accepts any directory.
func (s *server) existingSession(ctx context.Context, w http.ResponseWriter, name, dir string) (st *daemon.SessionState, done bool) {
	ts, exists, err := s.lookupSession(ctx, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return nil, true
	}
	if !exists {
		return nil, false
	}
	if dir != "" && !tmux.SamePath(ts.Path, dir) {
		http.Error(w, fmt.Sprintf("session %q already exists in %s", name, ts.Path), http.StatusConflict)
		return nil, true
	}
	// Re-chrome it (sessions started outside the daemon have none), but
	// with the label of the directory it really runs in.
	s.applyChrome(ctx, name, filepath.Base(ts.Path))
	state := s.liveSessionState(ts)
	return &state, true
}

// lookupSession finds a session by exact name in the tmux session list.
func (s *server) lookupSession(ctx context.Context, name string) (tmux.Session, bool, error) {
	list := s.list
	if list == nil {
		list = tmux.List
	}
	sessions, err := list(ctx)
	if err != nil {
		return tmux.Session{}, false, err
	}
	for _, ts := range sessions {
		if ts.Name == name {
			return ts, true, nil
		}
	}
	return tmux.Session{}, false, nil
}

// liveSessionState is the wire view of a live session: tmux's metadata
// plus the daemon's tracked state, as GET /v1/sessions reports it.
func (s *server) liveSessionState(ts tmux.Session) daemon.SessionState {
	s.mu.Lock()
	st := s.trackedStateLocked(ts.Name)
	s.mu.Unlock()
	st.Path = ts.Path
	st.Project = filepath.Base(ts.Path)
	st.Attached = ts.Attached
	st.Windows = ts.Windows
	st.Created = ts.Created
	if st.Agent == "" {
		st.Agent = string(s.sessionAgent(ts))
	}
	return st
}

// projectSession resolves the tmux session of the project in dir
// against this daemon's session list (tmux.ProjectSessionName):
// running reports that it already runs there.
func (s *server) projectSession(ctx context.Context, dir string) (name string, running bool, err error) {
	list := s.list
	if list == nil {
		list = tmux.List
	}
	sessions, err := list(ctx)
	if err != nil {
		return "", false, err
	}
	name, running = tmux.ProjectSessionName(sessions, dir)
	return name, running, nil
}

// defaultStartGrace is how long a create handler watches a session it
// just started before answering. An agent that isn't installed exits at
// once, tmux closes its only pane and the session with it, and the
// handler used to answer 200 for a session that was already gone.
const defaultStartGrace = 300 * time.Millisecond

// confirmStarted watches a just-created session for s.startGrace and
// answers 502 when it has already exited. A has-session failure isn't
// proof of anything, so it lets the create stand.
func (s *server) confirmStarted(ctx context.Context, w http.ResponseWriter, name, what string) bool {
	if s.startGrace <= 0 {
		return true
	}
	has := s.has
	if has == nil {
		has = tmux.Has
	}
	deadline := time.Now().Add(s.startGrace)
	for {
		alive, err := has(ctx, name)
		if err == nil && !alive {
			http.Error(w, fmt.Sprintf("session %q exited immediately after starting — is %s installed and working on this host?", name, what), http.StatusBadGateway)
			return false
		}
		if err != nil || !time.Now().Before(deadline) {
			return true
		}
		select {
		case <-ctx.Done():
			return true
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// createBareSession handles POST /v1/sessions/bare — a shell-only
// session not tied to any project, no agent, no scaffold. Used by
// the Sessions tab's "new session" form for ad-hoc work on any
// device.
//
// Path defaults to the daemon's configured sessions.default_dir,
// falling back to $HOME on the daemon's machine. We never resolve
// to the client's home — the whole point is "shell on the remote
// machine in that machine's home". The defaults come from config.toml
// as it is now, not as it was when the daemon started: an edited
// default_dir used to need a daemon restart to take effect.
//
// Idempotent: if `Name` already exists as a tmux session, return it
// (with the directory it really runs in) without re-creating — unless
// the request names a different directory, which is a name collision
// (409), the same rule createSession applies.
//
// The agent is "shell", a known agent id, or empty (the configured
// default); anything else is refused rather than silently falling back
// to the default agent.
func (s *server) createBareSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req daemon.NewBareSessionRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		http.Error(w, "decode: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !strings.EqualFold(strings.TrimSpace(req.Agent), tmux.ShellAgentTag) {
		if _, err := requestAgent(req.Agent); err != nil {
			http.Error(w, err.Error()+` (want "shell" or an agent id)`, http.StatusBadRequest)
			return
		}
	}
	cfg := s.freshConfig()
	path := config.ResolveBareSessionDir(req.Path, cfg.Sessions.DefaultDir)
	if !requireDir(w, path, "path on this host") {
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
	if badNewSessionName(name) {
		http.Error(w, badNewSessionNameMsg, http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	host, _ := os.Hostname()
	// Only an explicit path can conflict: without one, any existing
	// session of that name is the one asked for.
	wantDir := ""
	if strings.TrimSpace(req.Path) != "" {
		wantDir = path
	}
	if st, done := s.existingSession(ctx, w, name, wantDir); done {
		if st != nil {
			writeJSON(w, daemon.NewBareSessionResponse{Session: name, Path: st.Path, Host: host})
		}
		return
	}
	// Order: explicit request agent → daemon's [agents] default
	// (cfg.Agents.Default) → $SHELL. Bare sessions don't carry
	// --continue because they're not tied to a project transcript.
	launch := bareSessionLaunchCmd(req.Agent, cfg.Agents.Default, cfg.AgentCommands())
	// Tag what actually runs there, in the same tmux call that
	// creates the session. A bare session has no project sidecar,
	// so untagged the poll loop assumed Claude — a plain shell
	// prompt then read as "Claude crashed" (error) — and a tick
	// landing between two separate calls did exactly that.
	tag := bareSessionAgentTag(req.Agent, cfg.Agents.Default)
	if err := tmux.NewWithAgent(ctx, name, path, launch, tag); err != nil {
		http.Error(w, "tmux new-session: "+err.Error(), http.StatusInternalServerError)
		return
	}
	what := shellLaunchCmd()
	if id, ok := agent.ParseID(tag); ok {
		what = agent.ByID(id).Binary()
	}
	if !s.confirmStarted(ctx, w, name, what) {
		return
	}
	// Chrome the new session so when the client ssh-attaches it
	// lands in a ccmux-styled bar. The "project label" for bare
	// sessions is the basename of the working dir — gives the user
	// something readable in the status bar.
	s.applyChrome(ctx, name, filepath.Base(path))

	writeJSON(w, daemon.NewBareSessionResponse{
		Session: name,
		Path:    path,
		Host:    host,
	})
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
