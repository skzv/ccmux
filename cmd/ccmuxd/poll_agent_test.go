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

// TestSessionAgent_PlainSessionsAreShells pins sessionAgent's rule: an
// explicit agent tag, else the sidecar for an untagged ccmux-named
// session, else a shell — until a poll tick sees an agent in the
// session's foreground. Where a session runs no longer matters: a plain
// tmux session in a project directory used to be judged as that
// project's agent.
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
		{"ccmux-named, with a sidecar", tmux.Session{Name: "c-side", Path: sided}, agent.IDCodex},
		{"in a project dir", tmux.Session{Name: "work", Path: proj}, shellAgentID},
		{"outside the root with a sidecar", tmux.Session{Name: "side", Path: sided}, shellAgentID},
		{"user's own session elsewhere", tmux.Session{Name: "logs", Path: plain}, shellAgentID},
		{"user's own session, no path", tmux.Session{Name: "0"}, shellAgentID},
	} {
		if got := s.sessionAgent(tc.ts); got != tc.want {
			t.Errorf("%s: sessionAgent = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestPollOnce_PlainTmuxSessionIsAShell — tmux sessions the user made
// outside ccmux (no tag, no ccmux name) were judged by Claude's rules
// whenever they ran in a project directory: a session printing a log
// line every few seconds flapped active/idle forever with a push each
// time (30 of them: 120 pushes in 20s, "sender saturated"), and a plain
// zsh prompt showed as a crashed agent (error). With no agent in their
// foreground they are shells, wherever they run; an untagged ccmux
// session keeps its project's agent.
func TestPollOnce_PlainTmuxSessionIsAShell(t *testing.T) {
	root, proj, _, plain := projectsSandbox(t)
	s := newPollTestServer(t)
	s.cfg.Projects.Root = root
	pushes := countPushes(t, s)
	bells := countBells(s)
	logs := &fakePane{Pane: tmux.Pane{ID: "%1", Width: 80, Height: 24, Command: "tail"}, body: "12:00:00 GET /health 200"}
	projLogs := &fakePane{Pane: tmux.Pane{ID: "%5", Width: 80, Height: 24, Command: "tail"}, body: "12:00:00 GET /health 200"}
	zsh := &fakePane{Pane: tmux.Pane{ID: "%2", Width: 80, Height: 24, Command: "zsh"}, body: "user@host ~ % "}
	crashed := &fakePane{Pane: tmux.Pane{ID: "%3", Width: 80, Height: 24, Command: "zsh"}, body: crashedPane}
	inProj := &fakePane{Pane: tmux.Pane{ID: "%4", Width: 80, Height: 24, Command: "zsh"}, body: "user@host ~/Projects/proj % "}
	old := time.Now().Add(-time.Hour)
	f := newFakeTmux()
	f.addSession(tmux.Session{Name: "logs", Path: plain, Created: old}, logs)
	f.addSession(tmux.Session{Name: "proj-logs", Path: proj, Created: old}, projLogs)
	f.addSession(tmux.Session{Name: "scratch", Path: plain, Created: old}, zsh)
	f.addSession(tmux.Session{Name: "c-scratch", Path: plain, Created: old}, crashed)
	f.addSession(tmux.Session{Name: "work", Path: proj, Created: old}, inProj)
	f.wire(s)

	for i := 1; i <= 4; i++ { // a log line, then quiet, four times over
		f.update(func() {
			logs.body += "\n12:00:0" + strconv.Itoa(i) + " GET /health 200"
			projLogs.body += "\n12:00:0" + strconv.Itoa(i) + " GET /health 200"
		})
		pollNTimes(s, 3)
	}
	for _, name := range []string{"logs", "proj-logs", "scratch", "work"} {
		if tr := s.seen[name]; tr.agentID != shellAgentID || tr.state != agent.StateIdle {
			t.Errorf("%s: agent=%q state=%s, want a shell (idle)", name, tr.agentID, tr.state)
		}
	}
	if got := pushes(); got != 0 || *bells != 0 {
		t.Errorf("plain sessions notified: pushes=%d bells=%d, want none", got, *bells)
	}
	// Back-compat: an untagged ccmux-named session is still judged as its
	// project's agent (Claude by default): its agent runs under `sh -c`,
	// which is what its foreground shows.
	if tr := s.seen["c-scratch"]; tr.agentID != agent.IDClaude || tr.state != agent.StateError {
		t.Errorf("c-scratch: agent=%q state=%s, want claude/error", tr.agentID, tr.state)
	}
}

// TestPollOnce_AgentRunByHandIsClassified — `claude` run by hand in a
// plain tmux session, or in a ccmux shell (`ccmux shell --agent shell`,
// tagged "shell"), was invisible: the session was a shell, so a whole
// turn stayed idle with no notification. While an agent runs in the
// session's foreground, the session is classified as that agent; when it
// exits, it is a shell again.
func TestPollOnce_AgentRunByHandIsClassified(t *testing.T) {
	for _, tc := range []struct {
		name    string
		session tmux.Session
		command string // the agent's process as tmux reports it
		title   string // what the agent sets while waiting
	}{
		{"plain tmux session, native claude", tmux.Session{Name: "scratch", Path: "/tmp"}, "claude", "✳ Claude Code"},
		{"ccmux shell, native installer", tmux.Session{Name: "c-shell-1", Path: "/tmp", Agent: "shell"}, "2.1.281", "✳ Claude Code"},
		{"plain tmux session, npm claude", tmux.Session{Name: "npm", Path: "/tmp"}, "node", "✳ Claude Code"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newPollTestServer(t)
			s.startedAt = time.Now().Add(-time.Hour)
			pushes := countPushes(t, s)
			bells := countBells(s)
			idle, working := readFixture(t, "claude_v2_idle.txt"), readFixture(t, "claude_v2_working.txt")
			p := &fakePane{Pane: tmux.Pane{ID: "%1", Width: 120, Height: 40, Command: "zsh", Title: "host.local"}, body: "user@host /tmp % "}
			f := newFakeTmux()
			tc.session.Created = time.Now().Add(-time.Hour)
			f.addSession(tc.session, p)
			f.wire(s)
			name := tc.session.Name
			pollNTimes(s, 2)
			if tr := s.seen[name]; tr.agentID != shellAgentID || tr.state != agent.StateIdle {
				t.Fatalf("at the shell prompt: agent=%q state=%s, want shell/idle", tr.agentID, tr.state)
			}

			// The user runs claude: it starts up at its input box.
			f.update(func() { p.Command, p.body, p.Title = tc.command, idle, tc.title })
			pollNTimes(s, 4)
			tr := s.seen[name]
			if tr.agentID != agent.IDClaude || tr.state != agent.StateNeedsInput {
				t.Fatalf("claude started: agent=%q state=%s, want claude/needs_input", tr.agentID, tr.state)
			}
			if got := pushes(); got != 0 || *bells != 0 || tr.promptCount != 0 {
				t.Fatalf("claude starting up notified: pushes=%d bells=%d promptCount=%d", got, *bells, tr.promptCount)
			}

			// A turn.
			f.update(func() { p.body, p.Title = working, "⠋ Fix flaky poll test" })
			pollNTimes(s, 2)
			f.update(func() {
				p.body, p.Title = answered(idle, "fix the flaky poll test", "Fixed."), "✳ Fix flaky poll test"
			})
			pollNTimes(s, 3)
			if got := pushes(); got != 1 || *bells != 1 || tr.promptCount != 1 {
				t.Errorf("a turn of a hand-run claude: pushes=%d bells=%d promptCount=%d, want 1/1/1", got, *bells, tr.promptCount)
			}

			// It exits: a shell again, with nothing to announce.
			f.update(func() {
				p.Command, p.body = "zsh", answered(idle, "fix the flaky poll test", "Fixed.")+"\nuser@host /tmp % "
			})
			pollNTimes(s, 3)
			if tr.agentID != shellAgentID || tr.state != agent.StateIdle {
				t.Errorf("claude exited: agent=%q state=%s, want shell/idle", tr.agentID, tr.state)
			}
			if got := pushes(); got != 1 || *bells != 1 {
				t.Errorf("claude exiting notified: pushes=%d bells=%d, want 1/1", got, *bells)
			}
		})
	}
}

// TestPollOnce_NodeWithoutClaudeIsAShell — `node` runs many programs;
// only one showing Claude Code's screen is taken for Claude.
func TestPollOnce_NodeWithoutClaudeIsAShell(t *testing.T) {
	s := newPollTestServer(t)
	p := &fakePane{Pane: tmux.Pane{ID: "%1", Width: 80, Height: 24, Command: "node"}, body: "Welcome to Node.js v22.22.3.\n> "}
	f := newFakeTmux()
	f.addSession(tmux.Session{Name: "repl", Path: "/tmp", Created: time.Now().Add(-time.Hour)}, p)
	f.wire(s)
	pollNTimes(s, 2)
	if tr := s.seen["repl"]; tr.agentID != shellAgentID || tr.state != agent.StateIdle {
		t.Errorf("a node REPL: agent=%q state=%s, want shell/idle", tr.agentID, tr.state)
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
