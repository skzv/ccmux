package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tmux"
)

// post sends body as JSON to one of the create handlers.
func post(t *testing.T, h http.HandlerFunc, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw)))
	return rec
}

// newCreateTestServer is a server over the logging fake tmux with a
// projects root, returning the server, the root and the tmux call log.
func newCreateTestServer(t *testing.T) (*server, string, string) {
	t.Helper()
	logPath := fakeTmuxLog(t)
	root := t.TempDir()
	s := &server{
		cfg:  config.Config{Projects: config.ProjectsConfig{Root: root}},
		seen: map[string]*tracked{},
		// The fake tmux exits 0 for everything, so its has-session would
		// say every session exists.
		has: func(context.Context, string) (bool, error) { return false, nil },
	}
	return s, root, logPath
}

func mkdir(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestProjectLaunchCmd_RequestedAgentWins — the agent a request names
// is launched even when the project records another one.
func TestProjectLaunchCmd_RequestedAgentWins(t *testing.T) {
	dir := t.TempDir()
	if err := project.SetAgent(dir, agent.IDClaude); err != nil {
		t.Fatal(err)
	}
	id, launch := projectLaunchCmd(dir, agent.IDCodex, false, agent.Commands{})
	if id != agent.IDCodex || launch != agent.LaunchCmd(agent.IDCodex, false, agent.Commands{}) {
		t.Errorf("projectLaunchCmd(requested codex) = %q, %q; want codex's launch", id, launch)
	}
	if id, _ := projectLaunchCmd(dir, "", false, agent.Commands{}); id != agent.IDClaude {
		t.Errorf("no requested agent: got %q, want the sidecar's claude", id)
	}
}

// TestCreateSession_UnwritableSidecarStillLaunchesRequestedAgent — the
// requested agent was written to .ccmux/agent and the launch command
// built by reading it back, so when the sidecar couldn't be written
// (read-only project, a file in the way) {"agent":"codex"} ran claude.
// The session is also tagged with what runs, in the creating call.
func TestCreateSession_UnwritableSidecarStillLaunchesRequestedAgent(t *testing.T) {
	s, root, logPath := newCreateTestServer(t)
	dir := mkdir(t, filepath.Join(root, "proj"))
	// A regular file where the .ccmux directory belongs: SetAgent fails
	// even for root, unlike a chmod.
	if err := os.WriteFile(filepath.Join(dir, ".ccmux"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	rec := post(t, s.createSession, "/v1/sessions", daemon.NewSessionRequest{Project: "proj", Agent: "codex"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	codex := agent.LaunchCmd(agent.IDCodex, false, agent.Commands{})
	assertTaggedAtCreation(t, logPath, "c-proj", "codex")
	if creates := tmuxCallsWith(t, logPath, "new-session|"); len(creates) != 1 || !strings.Contains(creates[0], "|"+codex+"|;|") {
		t.Errorf("new-session = %q, want it to launch %q", creates, codex)
	}
	var got daemon.SessionState
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Agent != "codex" {
		t.Errorf("response agent = %q, want codex", got.Agent)
	}
}

// TestCreateSession_RequestedAgentPersistsAfterStart — a requested agent
// still becomes the project's agent once the session is running.
func TestCreateSession_RequestedAgentPersistsAfterStart(t *testing.T) {
	s, root, _ := newCreateTestServer(t)
	dir := mkdir(t, filepath.Join(root, "proj"))
	if rec := post(t, s.createSession, "/v1/sessions", daemon.NewSessionRequest{Project: "proj", Agent: "codex"}); rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got := project.ReadAgent(dir); got != agent.IDCodex {
		t.Errorf("sidecar = %q, want codex", got)
	}
}

// TestCreateEndpoints_RejectUnknownAgent — an unknown agent id silently
// launched the default agent (claude); now it's a 400 and nothing
// starts. "shell" keeps meaning a plain shell for bare sessions.
func TestCreateEndpoints_RejectUnknownAgent(t *testing.T) {
	s, root, logPath := newCreateTestServer(t)
	mkdir(t, filepath.Join(root, "proj"))
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"sessions": post(t, s.createSession, "/v1/sessions", daemon.NewSessionRequest{Project: "proj", Agent: "nope"}),
		"bare":     post(t, s.createBareSession, "/v1/sessions/bare", daemon.NewBareSessionRequest{Path: root, Agent: "nope"}),
		"projects": post(t, s.createProject, "/v1/projects", daemon.NewProjectRequest{Name: "fresh", Agent: "nope"}),
	} {
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `unknown agent "nope"`) {
			t.Errorf("%s: status %d body %q, want 400 unknown agent", name, rec.Code, rec.Body)
		}
	}
	if calls := tmuxCallsWith(t, logPath, "new-session"); len(calls) != 0 {
		t.Errorf("tmux new-session ran for an unknown agent: %q", calls)
	}
	if _, err := os.Stat(filepath.Join(root, "fresh")); err == nil {
		t.Error("POST /v1/projects created the directory for a rejected request")
	}
	// "shell" and a known id (any case) still start a bare session.
	for _, a := range []string{"shell", "Shell", "codex", ""} {
		if rec := post(t, s.createBareSession, "/v1/sessions/bare", daemon.NewBareSessionRequest{Path: root, Agent: a}); rec.Code != http.StatusOK {
			t.Errorf("bare agent %q: status %d (%s)", a, rec.Code, rec.Body)
		}
	}
}

// TestCreateEndpoints_RequireADirectory — a file path (/etc/passwd) was
// accepted: 200 reporting that path while tmux started the pane in $HOME.
func TestCreateEndpoints_RequireADirectory(t *testing.T) {
	s, root, logPath := newCreateTestServer(t)
	file := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"sessions": post(t, s.createSession, "/v1/sessions", daemon.NewSessionRequest{Project: "p", Path: file}),
		"bare":     post(t, s.createBareSession, "/v1/sessions/bare", daemon.NewBareSessionRequest{Path: file, Agent: "shell"}),
	} {
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "not a directory") {
			t.Errorf("%s: status %d body %q, want 400 not a directory", name, rec.Code, rec.Body)
		}
	}
	if calls := tmuxCallsWith(t, logPath, "new-session"); len(calls) != 0 {
		t.Errorf("tmux new-session ran for a file path: %q", calls)
	}
}

// TestCreateSession_ExistingSession — a create whose session already
// runs in the requested directory returns that session as it is (its
// tracked state, not a fresh "unknown"/unseen one); a session of that
// name in another directory is a 409. Before, both answered 200 with the
// request's project and path, and re-chromed the other project's session
// with the requesting project's label.
func TestCreateSession_ExistingSession(t *testing.T) {
	s, root, logPath := newCreateTestServer(t)
	alpha := mkdir(t, filepath.Join(root, "alpha"))
	beta := mkdir(t, filepath.Join(root, "beta"))
	s.list = func(context.Context) ([]tmux.Session, error) {
		return []tmux.Session{{Name: "shared", Path: beta, Agent: "codex", Windows: 1}}, nil
	}
	s.seen["shared"] = &tracked{state: agent.StateNeedsInput, agentID: agent.IDCodex, promptCount: 2}

	rec := post(t, s.createSession, "/v1/sessions", daemon.NewSessionRequest{Project: "alpha", Name: "shared"})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), beta) {
		t.Errorf("collision: status %d body %q, want 409 naming %s", rec.Code, rec.Body, beta)
	}
	for _, call := range tmuxCallsWith(t, logPath, "set-option") {
		if strings.Contains(call, "alpha") {
			t.Errorf("session in beta relabelled for alpha: %q", call)
		}
	}
	_ = alpha

	rec = post(t, s.createSession, "/v1/sessions", daemon.NewSessionRequest{Project: "beta", Name: "shared"})
	if rec.Code != http.StatusOK {
		t.Fatalf("same directory: status %d (%s)", rec.Code, rec.Body)
	}
	var got daemon.SessionState
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "shared" || got.Path != beta || got.Project != "beta" || got.Agent != "codex" ||
		got.State != string(agent.StateNeedsInput) || got.PromptCount != 2 || got.Windows != 1 {
		t.Errorf("existing session reported as %+v", got)
	}
	if calls := tmuxCallsWith(t, logPath, "new-session"); len(calls) != 0 {
		t.Errorf("tmux new-session ran for an existing session: %q", calls)
	}
}

// TestCreateBareSession_ExistingSession — an existing bare session is
// reported with the directory it runs in, not the one the request would
// have used; an explicit different directory is a 409.
func TestCreateBareSession_ExistingSession(t *testing.T) {
	s, root, logPath := newCreateTestServer(t)
	work := mkdir(t, filepath.Join(root, "work"))
	other := mkdir(t, filepath.Join(root, "other"))
	s.list = func(context.Context) ([]tmux.Session, error) {
		return []tmux.Session{{Name: "work", Path: work}}, nil
	}
	rec := post(t, s.createBareSession, "/v1/sessions/bare", daemon.NewBareSessionRequest{Name: "work", Agent: "shell"})
	var got daemon.NewBareSessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); rec.Code != http.StatusOK || err != nil || got.Path != work {
		t.Errorf("no path: status %d, response %+v, want 200 with path %s", rec.Code, got, work)
	}
	rec = post(t, s.createBareSession, "/v1/sessions/bare", daemon.NewBareSessionRequest{Name: "work", Path: other, Agent: "shell"})
	if rec.Code != http.StatusConflict {
		t.Errorf("other path: status %d (%s), want 409", rec.Code, rec.Body)
	}
	if calls := tmuxCallsWith(t, logPath, "new-session"); len(calls) != 0 {
		t.Errorf("tmux new-session ran for an existing session: %q", calls)
	}
}

// TestCreateProject_ExistingSession — creating a project whose session
// is already running answered 500 "duplicate session"; it is now
// idempotent like POST /v1/sessions.
func TestCreateProject_ExistingSession(t *testing.T) {
	s, root, logPath := newCreateTestServer(t)
	dir := mkdir(t, filepath.Join(root, "proj"))
	s.list = func(context.Context) ([]tmux.Session, error) {
		return []tmux.Session{{Name: "c-proj", Path: dir}}, nil
	}
	rec := post(t, s.createProject, "/v1/projects", daemon.NewProjectRequest{Name: "proj"})
	var got daemon.NewProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); rec.Code != http.StatusOK || err != nil || got.Session != "c-proj" || got.Path != dir {
		t.Errorf("status %d, response %+v (%s), want 200 with c-proj in %s", rec.Code, got, rec.Body, dir)
	}
	if calls := tmuxCallsWith(t, logPath, "new-session"); len(calls) != 0 {
		t.Errorf("tmux new-session ran for an existing session: %q", calls)
	}
}

// TestCreateEndpoints_SameFolderNameElsewhere — a c-proj session
// running in another directory (a same-named project under another
// root) made POST /v1/projects answer 409 and POST /v1/sessions hand
// back 409 too, so the project could never be opened. Each now starts
// the project's own, path-tagged session and — once it runs — returns
// that; the other directory's c-proj is left alone. 409 remains for a
// tagged name that is taken as well.
func TestCreateEndpoints_SameFolderNameElsewhere(t *testing.T) {
	for _, endpoint := range []string{"/v1/projects", "/v1/sessions"} {
		t.Run(endpoint, func(t *testing.T) {
			s, root, logPath := newCreateTestServer(t)
			dir := mkdir(t, filepath.Join(root, "proj"))
			elsewhere := mkdir(t, filepath.Join(t.TempDir(), "proj"))
			tagged := tmux.PathTaggedSessionName(dir)
			sessions := []tmux.Session{{Name: "c-proj", Path: elsewhere}}
			s.list = func(context.Context) ([]tmux.Session, error) { return sessions, nil }
			create := func() (int, string, string) {
				var rec *httptest.ResponseRecorder
				if endpoint == "/v1/projects" {
					rec = post(t, s.createProject, endpoint, daemon.NewProjectRequest{Name: "proj"})
					var got daemon.NewProjectResponse
					_ = json.Unmarshal(rec.Body.Bytes(), &got)
					return rec.Code, got.Session, got.Path
				}
				rec = post(t, s.createSession, endpoint, daemon.NewSessionRequest{Project: "proj"})
				var got daemon.SessionState
				_ = json.Unmarshal(rec.Body.Bytes(), &got)
				return rec.Code, got.Name, got.Path
			}

			code, name, path := create()
			if code != http.StatusOK || name != tagged || path != dir {
				t.Fatalf("first open: status %d session %q path %q, want 200 %s in %s", code, name, path, tagged, dir)
			}
			creates := tmuxCallsWith(t, logPath, "new-session")
			if len(creates) != 1 || !strings.Contains(creates[0], "|-s|"+tagged+"|-c|"+dir+"|") {
				t.Fatalf("new-session = %q, want %s started in %s", creates, tagged, dir)
			}
			for _, call := range tmuxCallsWith(t, logPath, "=c-proj:") {
				t.Errorf("the other directory's c-proj was touched: %q", call)
			}

			// Opened again, the tagged session is the answer; nothing new starts.
			sessions = append(sessions, tmux.Session{Name: tagged, Path: dir, Windows: 1})
			if code, name, _ := create(); code != http.StatusOK || name != tagged {
				t.Errorf("second open: status %d session %q, want 200 %s", code, name, tagged)
			}
			if n := len(tmuxCallsWith(t, logPath, "new-session")); n != 1 {
				t.Errorf("second open started a session (%d new-session calls)", n)
			}

			// The tagged name held by yet another directory: 409, not a stranger's session.
			sessions = []tmux.Session{{Name: "c-proj", Path: elsewhere}, {Name: tagged, Path: elsewhere}}
			if code, _, _ := create(); code != http.StatusConflict {
				t.Errorf("tagged name taken elsewhere: status %d, want 409", code)
			}
		})
	}
}

// TestCreateProject_NoAgentRunsTheProjectsAgent — POST /v1/projects for
// an existing project with no agent in the request launched Claude
// whatever the project's .ccmux/agent sidecar recorded. The project's
// own agent must run, tagged on the session, and a failed start must
// name that agent's binary.
func TestCreateProject_NoAgentRunsTheProjectsAgent(t *testing.T) {
	s, root, logPath := newCreateTestServer(t)
	dir := mkdir(t, filepath.Join(root, "proj"))
	if err := project.SetAgent(dir, agent.IDCodex); err != nil {
		t.Fatal(err)
	}
	if rec := post(t, s.createProject, "/v1/projects", daemon.NewProjectRequest{Name: "proj"}); rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	codex := agent.LaunchCmd(agent.IDCodex, false, agent.Commands{})
	assertTaggedAtCreation(t, logPath, "c-proj", "codex")
	if creates := tmuxCallsWith(t, logPath, "new-session|"); len(creates) != 1 || !strings.Contains(creates[0], "|"+codex+"|;|") {
		t.Errorf("new-session = %q, want it to launch %q", creates, codex)
	}
	if got := project.ReadAgent(dir); got != agent.IDCodex {
		t.Errorf("project agent = %q, want codex kept", got)
	}

	s.startGrace = 100 * time.Millisecond // the session dies at once
	if rec := post(t, s.createProject, "/v1/projects", daemon.NewProjectRequest{Name: "proj"}); rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "is codex installed") {
		t.Errorf("dead session: status %d body %q, want 502 naming codex", rec.Code, rec.Body)
	}
}

// TestCreateEndpoints_SessionThatExitsImmediately — with the agent's
// binary missing (and no --continue shell fallback) the session died at
// once, yet every create endpoint answered 200 for a session that no
// longer existed. They now watch it briefly and answer 502.
func TestCreateEndpoints_SessionThatExitsImmediately(t *testing.T) {
	s, root, _ := newCreateTestServer(t)
	mkdir(t, filepath.Join(root, "proj"))
	s.startGrace = 200 * time.Millisecond
	alive := false
	s.has = func(context.Context, string) (bool, error) { return alive, nil }

	for name, send := range map[string]func() *httptest.ResponseRecorder{
		"sessions": func() *httptest.ResponseRecorder {
			return post(t, s.createSession, "/v1/sessions", daemon.NewSessionRequest{Project: "proj", Agent: "codex"})
		},
		"bare": func() *httptest.ResponseRecorder {
			return post(t, s.createBareSession, "/v1/sessions/bare", daemon.NewBareSessionRequest{Path: root, Agent: "codex"})
		},
		"projects": func() *httptest.ResponseRecorder {
			return post(t, s.createProject, "/v1/projects", daemon.NewProjectRequest{Name: "fresh", Agent: "codex"})
		},
	} {
		alive = false
		if rec := send(); rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "is codex installed") {
			t.Errorf("%s, dead session: status %d body %q, want 502 naming codex", name, rec.Code, rec.Body)
		}
		if name == "sessions" {
			// A failed start doesn't switch the project to the agent
			// that just died.
			if got := project.ReadAgent(filepath.Join(root, "proj")); got != agent.IDClaude {
				t.Errorf("failed start recorded the project's agent as %q", got)
			}
		}
		alive = true
		if rec := send(); rec.Code != http.StatusOK {
			t.Errorf("%s, live session: status %d (%s), want 200", name, rec.Code, rec.Body)
		}
	}
}

// TestCreateSession_ResponseIsSeen — POST /v1/sessions answered
// seen:false for a session GET /v1/sessions lists as seen:true.
func TestCreateSession_ResponseIsSeen(t *testing.T) {
	s, root, _ := newCreateTestServer(t)
	mkdir(t, filepath.Join(root, "proj"))
	rec := post(t, s.createSession, "/v1/sessions", daemon.NewSessionRequest{Project: "proj"})
	var got daemon.SessionState
	if err := json.Unmarshal(rec.Body.Bytes(), &got); rec.Code != http.StatusOK || err != nil {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if !got.Seen {
		t.Error("new session reported seen:false; GET /v1/sessions reports it seen:true")
	}
}
