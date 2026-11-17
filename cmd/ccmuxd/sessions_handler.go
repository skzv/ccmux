package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tmux"
)

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
	//
	// A List failure is surfaced as a 500, not an empty list: "no tmux
	// server running" is already a nil,nil success inside tmux.List, so
	// any error here is a real fault (tmux missing from PATH, wedged
	// server) and hiding it made the dashboard silently empty.
	tss, err := s.list(ctx)
	if err != nil {
		// tmux.List's error already names the command.
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.mu.Lock()
	out := make([]daemon.SessionState, 0, len(tss))
	for _, ts := range tss {
		t, ok := s.seen[ts.Name]
		if !ok {
			// A session the poll loop hasn't tickled yet: treat as
			// seen=true (nothing for the user to review yet) rather
			// than implicitly unseen, otherwise restarting ccmuxd
			// would resurface every old session as "needs attention" —
			// unless the session carries a daemon's review record,
			// which the poll loop's first look will read back too.
			t = &tracked{state: agent.StateUnknown, seen: true}
			t.restoreReview(ts)
		}
		// For sessions we've seen via the poll loop this is already
		// populated. For pre-existing sessions (e.g. the daemon just
		// started and hasn't tickled the poll loop yet), fall back to
		// reading the sidecar on the fly. Fast — single os.ReadFile.
		agentID := t.agentID
		if agentID == "" {
			agentID = s.sessionAgent(ts)
		}
		proj := t.project
		if !t.projectKnown {
			proj = s.sessionProject(ts.Path)
		}
		out = append(out, daemon.SessionState{
			Name: ts.Name, Host: "local", Project: proj, Path: ts.Path,
			Attached: ts.Attached, Windows: ts.Windows,
			Created: ts.Created, LastChange: t.lastChange,
			State: string(t.state), PromptCount: t.promptCount,
			Agent: string(agentID),
			Seen:  t.seen,
		})
	}
	s.mu.Unlock()
	writeJSON(w, out)
}

// badSessionName reports whether a session name can't be passed to a
// tmux `-t` target: tmux.ValidTarget's rule — no leading `$` (tmux reads
// it as a session ID even in the exact `=name:` form, so
// POST /v1/sessions/$1/kill killed whichever session had ID $1), no
// `:` (a window/pane qualifier), `.` (tmux also rewrites it to `_` in
// new session names on older versions, so the name we'd report back
// wouldn't exist), `/` or `\` (path separators in these routes), and no
// control characters. `@` and `%` names are fine: tmux finds them by
// name in an exact target. Centralizes the rule every handler that
// passes a name to a tmux `-t` argument shares, and the one the CLI
// applies before it targets a session.
//
// The empty name is not "bad" here: each caller handles it (an auto
// name for a new session, "session name required" for a route).
func badSessionName(name string) bool {
	return name != "" && !tmux.ValidTarget(name)
}

const badSessionNameMsg = "name must not start with $ or contain /, \\, :, . or control characters"

// badNewSessionName is badSessionName for a name ccmux gives a session
// (create, rename), which additionally must not contain `#`: tmux
// expands formats in the name passed to new-session -s and
// rename-session, so "x#{session_id}" is created as "x$1" and the name
// we'd report back doesn't exist (and "#(…)" would run a command). `#`
// stays allowed when targeting an existing session.
func badNewSessionName(name string) bool {
	return badSessionName(name) || strings.Contains(name, "#")
}

const badNewSessionNameMsg = "name must not start with $ or contain /, \\, :, ., # or control characters"

func (s *server) handleSessionsItem(w http.ResponseWriter, r *http.Request) {
	// /v1/sessions/<name>[/<subaction>]
	tail := strings.TrimPrefix(r.URL.Path, "/v1/sessions/")
	parts := strings.SplitN(tail, "/", 2)
	name := parts[0]
	if name == "" {
		http.Error(w, "session name required", http.StatusBadRequest)
		return
	}
	// Validate the path-derived name BEFORE routing to any subaction.
	// kill/send-keys/preview/attach all pass this straight to tmux as a
	// `-t` target; without this guard a `:` in the URL name could
	// mis-target an unrelated window/pane of another session (the
	// create/rename handlers already reject these, but the item
	// handlers didn't).
	if badSessionName(name) {
		http.Error(w, badSessionNameMsg, http.StatusBadRequest)
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
	if err := s.kill(ctx, name); err != nil {
		writeTmuxError(w, err)
		return
	}
	last := s.forgetKilled(name)
	s.events.Publish(daemon.SessionEvent{At: time.Now(), Kind: "killed", Session: last})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleRename(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req daemon.RenameRequest
	err := decodeJSONBody(w, r, &req)
	// Trimmed like the create handlers' names: " new " would otherwise
	// become a session whose name nobody types back correctly.
	req.Name = strings.TrimSpace(req.Name)
	if err != nil || req.Name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	// Same rule createSession/createBareSession enforce: tmux interprets
	// `name:window.pane` as a target spec, so a rename to "victim:0"
	// would let later send-keys land in an unrelated tmux session, and
	// rename-session expands formats (`#{…}`) in the new name.
	if badNewSessionName(req.Name) {
		http.Error(w, badNewSessionNameMsg, http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := s.rename(ctx, name, req.Name); err != nil {
		if strings.Contains(err.Error(), "duplicate session") {
			http.Error(w, fmt.Sprintf("a session named %q already exists", req.Name), http.StatusConflict)
			return
		}
		writeTmuxError(w, err)
		return
	}
	renamed := s.renameTracked(name, req.Name)
	if req.Name == name {
		// tmux accepts a rename to the same name and changes nothing, so
		// there is nothing to announce: a killed+created pair would make
		// clients drop the row and re-add it.
		writeJSON(w, renamed)
		return
	}
	// There is no "renamed" event kind (the mobile apps decode a fixed
	// set), so report a rename as the old name going away and the new
	// one appearing — with the state it had, not a blank "unseen" row.
	gone := renamed
	gone.Name = name
	s.events.Publish(daemon.SessionEvent{At: time.Now(), Kind: "killed", Session: gone})
	s.events.Publish(daemon.SessionEvent{At: time.Now(), Kind: "created", Session: renamed})
	writeJSON(w, renamed)
}

func (s *server) handleSendKeys(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req daemon.SendKeysRequest
	if err := decodeJSONBody(w, r, &req); err != nil || req.Keys == "" {
		http.Error(w, "keys required", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := s.sendKeysToAgent(ctx, name, req.Keys); err != nil {
		writeTmuxError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// sendKeysToAgent types keys into the pane the session's agent runs in
// — the pane the poll loop classifies (agentPaneID) — rather than the
// session's active pane. A bare session target means the active pane of
// the active window, so with the agent in window 0 and the user in a
// shell window, a phone's or an MCP client's reply to the agent ("y",
// "Enter", a prompt) was typed into the shell and run there.
//
// The session's active pane is used only when the agent's pane can't
// be resolved at all (list-panes failed: typically no such session,
// which the fallback then reports as such). Once resolved, a failed
// send is an error, never a retry on the active pane.
func (s *server) sendKeysToAgent(ctx context.Context, name, keys string) error {
	if id := s.agentPaneID(ctx, name); id != "" {
		send := s.sendKeysPane
		if send == nil {
			send = tmux.SendKeysPane
		}
		return send(ctx, id, keys)
	}
	send := s.sendKeys
	if send == nil {
		send = tmux.SendKeys
	}
	return send(ctx, name, keys)
}

// sessionNotFound reports whether a tmux error means the targeted
// session doesn't exist — including there being no tmux server at all.
// internal/tmux folds tmux's stderr into its errors (a bare
// exec.ExitError says only "exit status 1").
func sessionNotFound(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "can't find session") ||
		strings.Contains(msg, "no such session") || // set-option's wording
		strings.Contains(msg, "no current session") ||
		strings.Contains(msg, "no server running") ||
		(strings.Contains(msg, "error connecting to") && strings.Contains(msg, "No such file or directory"))
}

// writeTmuxError answers a failed tmux call on a named session: 404
// when the session doesn't exist (kill/rename/send-keys used to answer
// 500 for that, indistinguishable from a real failure), else 500.
func writeTmuxError(w http.ResponseWriter, err error) {
	if sessionNotFound(err) {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}
