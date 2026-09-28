package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tmux"
)

// projectsSandbox makes a projects root with one project ("proj") and a
// directory outside it carrying a .ccmux/agent sidecar ("elsewhere/
// sided", codex), plus a plain directory ("elsewhere/plain").
func projectsSandbox(t *testing.T) (root, proj, sided, plain string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "Projects")
	proj = filepath.Join(root, "proj")
	sided = filepath.Join(base, "elsewhere", "sided")
	plain = filepath.Join(base, "elsewhere", "plain")
	for _, d := range []string{proj, filepath.Join(proj, "src"), filepath.Join(root, ".hidden"), plain} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := project.SetAgent(sided, agent.IDCodex); err != nil {
		t.Fatal(err)
	}
	return root, proj, sided, plain
}

func TestProjectName(t *testing.T) {
	root, proj, sided, plain := projectsSandbox(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		root, path, want string
	}{
		{root, proj, "proj"},
		{root, filepath.Join(proj, "src"), "proj"},         // inside a project
		{root, root, ""},                                   // the root itself
		{root, filepath.Join(root, ".hidden"), ""},         // hidden: not a project
		{root, sided, "sided"},                             // outside, with a sidecar
		{root, plain, ""},                                  // outside, no sidecar
		{root, "", ""},                                     // unknown path
		{link, proj, "proj"},                               // root reached through a symlink
		{root, filepath.Join(link, "proj", "src"), "proj"}, // path reached through one
		{"", proj, ""},
	} {
		if got := projectName(tc.root, tc.path); got != tc.want {
			t.Errorf("projectName(%q, %q) = %q, want %q", tc.root, tc.path, got, tc.want)
		}
	}
}

// TestSessionAgent_PlainSessionsAreShells pins sessionAgent's rule.
func TestSessionAgent_PlainSessionsAreShells(t *testing.T) {
	root, proj, sided, plain := projectsSandbox(t)
	s := newPollTestServer(t)
	s.cfg.Projects.Root = root
	s.readAgent = project.ReadAgent
	for _, tc := range []struct {
		name string
		ts   tmux.Session
		want agent.ID
	}{
		{"tagged shell", tmux.Session{Name: "c-shell-1", Path: proj, Agent: "shell"}, shellAgentID},
		{"tagged agent", tmux.Session{Name: "notes", Path: plain, Agent: "codex"}, agent.IDCodex},
		{"ccmux-named, outside projects", tmux.Session{Name: "c-scratch", Path: plain}, agent.IDClaude},
		{"in a project dir", tmux.Session{Name: "work", Path: proj}, agent.IDClaude},
		{"outside the root with a sidecar", tmux.Session{Name: "side", Path: sided}, agent.IDCodex},
		{"user's own session elsewhere", tmux.Session{Name: "logs", Path: plain}, shellAgentID},
		{"user's own session, no path", tmux.Session{Name: "0"}, shellAgentID},
	} {
		if got := s.sessionAgent(tc.ts); got != tc.want {
			t.Errorf("%s: sessionAgent = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestPollOnce_PlainTmuxSessionIsAShell — tmux sessions the user made
// outside ccmux (no tag, no ccmux name, not in a project) were judged
// by Claude's rules: a session printing a log line every few seconds
// flapped active/idle forever with a push each time, and a plain zsh
// prompt showed as a crashed agent (error). They are shells now; ccmux
// sessions and sessions in project directories keep being classified.
func TestPollOnce_PlainTmuxSessionIsAShell(t *testing.T) {
	root, proj, _, plain := projectsSandbox(t)
	s := newPollTestServer(t)
	s.cfg.Projects.Root = root
	pushes := countPushes(t, s)
	bells := countBells(s)
	logs := &fakePane{Pane: tmux.Pane{ID: "%1", Width: 80, Height: 24}, body: "12:00:00 GET /health 200"}
	zsh := &fakePane{Pane: tmux.Pane{ID: "%2", Width: 80, Height: 24}, body: "user@host ~ % "}
	crashed := &fakePane{Pane: tmux.Pane{ID: "%3", Width: 80, Height: 24}, body: crashedPane}
	inProj := &fakePane{Pane: tmux.Pane{ID: "%4", Width: 80, Height: 24}, body: crashedPane}
	old := time.Now().Add(-time.Hour)
	f := newFakeTmux()
	f.addSession(tmux.Session{Name: "logs", Path: plain, Created: old}, logs)
	f.addSession(tmux.Session{Name: "scratch", Path: plain, Created: old}, zsh)
	f.addSession(tmux.Session{Name: "c-scratch", Path: plain, Created: old}, crashed)
	f.addSession(tmux.Session{Name: "work", Path: proj, Created: old}, inProj)
	f.wire(s)

	for i := 1; i <= 4; i++ { // a log line, then quiet, four times over
		f.update(func() { logs.body += "\n12:00:0" + strconv.Itoa(i) + " GET /health 200" })
		pollNTimes(s, 3)
	}
	for _, name := range []string{"logs", "scratch"} {
		if tr := s.seen[name]; tr.agentID != shellAgentID || tr.state != agent.StateIdle {
			t.Errorf("%s: agent=%q state=%s, want a shell (idle)", name, tr.agentID, tr.state)
		}
	}
	if got := pushes(); got != 0 || *bells != 0 {
		t.Errorf("plain sessions notified: pushes=%d bells=%d, want none", got, *bells)
	}
	// Back-compat: a ccmux-named session and one in a project directory
	// are still judged as the project's agent (Claude by default).
	for _, name := range []string{"c-scratch", "work"} {
		if tr := s.seen[name]; tr.agentID != agent.IDClaude || tr.state != agent.StateError {
			t.Errorf("%s: agent=%q state=%s, want claude/error", name, tr.agentID, tr.state)
		}
	}
}

// decodeSessions decodes a GET /v1/sessions response.
func decodeSessions(t *testing.T, rec *httptest.ResponseRecorder) []daemon.SessionState {
	t.Helper()
	var out []daemon.SessionState
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode /v1/sessions: %v (%s)", err, rec.Body)
	}
	return out
}

// listedAgents is GET /v1/sessions' agent for each session.
func listedAgents(t *testing.T, s *server) map[string]string {
	t.Helper()
	rec := httptest.NewRecorder()
	s.listSessions(rec, httptest.NewRequest(http.MethodGet, "/v1/sessions", nil))
	agents := map[string]string{}
	for _, ss := range decodeSessions(t, rec) {
		agents[ss.Name] = ss.Agent
	}
	return agents
}

// TestPollOnce_ProjectAgentSwitchKeepsRunningSessions — switching a
// project's agent (Projects screen `a`) rewrites its .ccmux/agent
// sidecar, and the daemon re-read it every tick: the Claude session
// already running there was suddenly judged, and shown, as Codex. The
// sidecar is what the project's next session starts; a running
// session keeps the agent it was resolved with. Its tag, when it has
// one, still wins.
func TestPollOnce_ProjectAgentSwitchKeepsRunningSessions(t *testing.T) {
	s := newPollTestServer(t)
	sidecar := agent.IDClaude
	s.readAgent = func(string) agent.ID { return sidecar }
	p := &fakePane{Pane: tmux.Pane{ID: "%1", Width: 120, Height: 40}, body: readFixture(t, "claude_v2_idle.txt")}
	f := newFakeTmux()
	ts := tmux.Session{Name: "c-proj", Path: "/tmp", Created: time.Now().Add(-time.Hour)}
	f.addSession(ts, p)
	f.wire(s)
	pollNTimes(s, 1)

	sidecar = agent.IDCodex // the user switches the project to Codex
	pollNTimes(s, 2)
	if got := s.seen["c-proj"].agentID; got != agent.IDClaude {
		t.Errorf("running session's agent = %q after the project switched, want %q", got, agent.IDClaude)
	}
	if got := listedAgents(t, s)["c-proj"]; got != string(agent.IDClaude) {
		t.Errorf("/v1/sessions agent = %q, want %q", got, agent.IDClaude)
	}

	ts.Agent = "muse" // tagged: the tag wins
	f.addSession(ts, p)
	pollNTimes(s, 1)
	if got := s.seen["c-proj"].agentID; got != agent.IDMuse {
		t.Errorf("tagged session's agent = %q, want %q", got, agent.IDMuse)
	}
}
