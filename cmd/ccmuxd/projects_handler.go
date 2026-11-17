package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/scaffold"
)

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
	root := project.ResolveRoot(s.cfg.Projects.Root)
	ps, _ := project.Discover(root)
	out := make([]daemon.ProjectInfo, 0, len(ps))
	for _, p := range ps {
		out = append(out, daemon.ProjectInfo{
			Name: p.Name, Host: host, Path: p.Path,
			HasGit: p.HasGit, HasCM: p.HasCM, HasAgents: p.HasAgents, HasDocs: p.HasDocs,
			Agent: string(p.Agent), Modified: p.Modified,
		})
	}
	writeJSON(w, out)
}

// createProject creates a new project — its directory plus an agent
// session — on this host. The directory is placed under the daemon's
// Projects.Root and the session is named via tmux.ProjectSessionName
// so the client can ssh-attach directly. It creates only the
// directory; no CLAUDE.md, no docs/ tree, no git init.
func (s *server) createProject(w http.ResponseWriter, r *http.Request) {
	var req daemon.NewProjectRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
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
	if err := project.ValidateName(name); err != nil {
		http.Error(w, "name must be a single non-hidden path segment: "+err.Error(), http.StatusBadRequest)
		return
	}
	// An empty agent defers to the project's recorded one (Claude when
	// there is none); a typo'd one is refused instead of quietly
	// launching Claude.
	chosenAgent, err := requestAgent(req.Agent)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	dir := filepath.Join(project.ResolveRoot(s.cfg.Projects.Root), name)

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	host, _ := os.Hostname()
	// A project whose session is already running is answered like
	// POST /v1/sessions answers one: 200 with that session (creating it
	// again used to fail with 500 "duplicate session"). The session is
	// the project's own (tmux.ProjectSessionName): a c-<name> session
	// running in another directory — a same-named project elsewhere —
	// no longer blocks it; it gets a path-tagged session instead. 409
	// only when that name is taken too.
	session, running, err := s.projectSession(ctx, dir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sessionDir := dir
	if running {
		sessionDir = "" // already known to run in dir
	}
	if st, done := s.existingSession(ctx, w, session, sessionDir); done {
		if st != nil {
			writeJSON(w, daemon.NewProjectResponse{Session: st.Name, Path: st.Path, Host: host})
		}
		return
	}
	opts := scaffold.Options{
		Name:     name,
		Dir:      dir,
		Agent:    chosenAgent,
		Commands: s.freshCommands(),
		Session:  session,
	}
	// What StartSession runs: the requested agent, else the existing
	// project's recorded one.
	launched := scaffold.SessionAgent(opts, dir)
	session, err = scaffold.StartSession(ctx, opts)
	if err != nil {
		http.Error(w, "start: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !s.confirmStarted(ctx, w, session, agent.ByID(launched).Binary()) {
		return
	}
	// Apply ccmux chrome on the session before the client ssh-attaches.
	// Without this the remote tmux looks like plain stock tmux instead
	// of a ccmux-managed session — no project label in the status bar,
	// no detach hint, no moshi badge.
	s.applyChrome(ctx, session, name)

	writeJSON(w, daemon.NewProjectResponse{
		Session: session,
		Path:    dir,
		Host:    host,
	})
}
