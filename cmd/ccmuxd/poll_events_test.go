package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tmux"
)

// lastEvent returns the last published event of kind for name.
func lastEvent(t *testing.T, evs []daemon.SessionEvent, kind, name string) daemon.SessionState {
	t.Helper()
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Kind == kind && evs[i].Session.Name == name {
			return evs[i].Session
		}
	}
	t.Fatalf("no %q event for %s in %+v", kind, name, evs)
	return daemon.SessionState{}
}

// TestPollOnce_EventsCarryTheWholeSession — SSE events carried a name
// and a bare state: needs_input / state_change had attached=false,
// prompt_count=0, no agent and zero last_change/created; created had
// no project, windows or created time; killed carried state "" (not a
// documented value). Every event now carries the session as GET
// /v1/sessions reports it — killed, as it was last seen.
func TestPollOnce_EventsCarryTheWholeSession(t *testing.T) {
	root, proj, _, _ := projectsSandbox(t)
	s := newPollTestServer(t)
	s.cfg.Projects.Root = root
	s.startedAt = time.Now().Add(-time.Hour)
	s.readAgent = func(string) agent.ID { return agent.IDCodex }
	created := time.Now().Add(-time.Second).Truncate(time.Second)
	ts := tmux.Session{Name: "c-proj", Path: proj, Attached: true, Windows: 2, Created: created}
	p := &fakePane{Pane: tmux.Pane{ID: "%1", Width: 80, Height: 24, Title: "⠋ working"}, body: "thinking…"}
	f := newFakeTmux()
	f.addSession(ts, p)
	f.wire(s)
	ch := s.events.Subscribe()
	defer s.events.Unsubscribe(ch)

	pollNTimes(s, 2)
	f.update(func() { p.body, p.Title = "done.\n› ", "codex" })
	pollNTimes(s, 3)
	f.removeSession("c-proj")
	pollNTimes(s, 1)
	evs := publishedEvents(ch)

	check := func(kind string, want daemon.SessionState) {
		t.Helper()
		got := lastEvent(t, evs, kind, "c-proj")
		if got.Project != want.Project || got.Path != want.Path || got.Attached != want.Attached ||
			got.Windows != want.Windows || !got.Created.Equal(want.Created) || got.Agent != want.Agent ||
			got.State != want.State || got.PromptCount != want.PromptCount || got.Host != "local" {
			t.Errorf("%s event session = %+v\nwant (fields) %+v", kind, got, want)
		}
		if kind != "created" && got.LastChange.IsZero() {
			t.Errorf("%s event has no last_change", kind)
		}
	}
	base := daemon.SessionState{
		Project: "proj", Path: proj, Attached: true, Windows: 2, Created: created, Agent: string(agent.IDCodex),
	}
	want := base
	want.State = string(agent.StateUnknown)
	check("created", want)
	want = base
	want.State, want.PromptCount = string(agent.StateNeedsInput), 1
	check("needs_input", want)
	check("killed", want) // as last seen, never an undocumented ""
}

// TestHandleKill_EventCarriesLastKnownState — the kill handler's event
// had state "" too; it reports the session as last seen, or "unknown"
// for one the poll loop hadn't picked up.
func TestHandleKill_EventCarriesLastKnownState(t *testing.T) {
	s := newPollTestServer(t)
	s.kill = func(_ context.Context, _ string) error { return nil }
	s.seen["c-a"] = &tracked{state: agent.StateNeedsInput, agentID: agent.IDClaude, promptCount: 2,
		pollTrack: pollTrack{project: "a", projectKnown: true, listed: tmux.Session{Name: "c-a", Windows: 1}}}
	ch := s.events.Subscribe()
	defer s.events.Unsubscribe(ch)

	for _, name := range []string{"c-a", "c-untracked"} {
		rec := httptest.NewRecorder()
		s.handleKill(rec, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+name+"/kill", nil), name)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("kill %s: status %d", name, rec.Code)
		}
	}
	evs := publishedEvents(ch)
	if got := lastEvent(t, evs, "killed", "c-a"); got.State != string(agent.StateNeedsInput) ||
		got.Project != "a" || got.PromptCount != 2 || got.Agent != string(agent.IDClaude) || got.Windows != 1 {
		t.Errorf("killed c-a = %+v, want its last known state", got)
	}
	if got := lastEvent(t, evs, "killed", "c-untracked"); got.State != string(agent.StateUnknown) {
		t.Errorf("killed c-untracked state = %q, want unknown", got.State)
	}
}

// TestListSessions_ReportsProject — GET /v1/sessions never filled
// `project`, so MCP list_sessions always showed project "" although its
// spec promises it. A session in a project directory reports the
// project's name (as list_projects names it); one elsewhere reports "".
func TestListSessions_ReportsProject(t *testing.T) {
	root, proj, sided, plain := projectsSandbox(t)
	s := newPollTestServer(t)
	s.cfg.Projects.Root = root
	f := newFakeTmux()
	f.addSession(tmux.Session{Name: "c-proj", Path: proj}, &fakePane{Pane: tmux.Pane{ID: "%1"}})
	f.addSession(tmux.Session{Name: "c-side", Path: sided}, &fakePane{Pane: tmux.Pane{ID: "%2"}})
	f.addSession(tmux.Session{Name: "c-plain", Path: plain}, &fakePane{Pane: tmux.Pane{ID: "%3"}})
	f.wire(s)

	want := map[string]string{"c-proj": "proj", "c-side": "sided", "c-plain": ""}
	check := func(when string) {
		t.Helper()
		rec := httptest.NewRecorder()
		s.listSessions(rec, httptest.NewRequest(http.MethodGet, "/v1/sessions", nil))
		for _, ss := range decodeSessions(t, rec) {
			if ss.Project != want[ss.Name] {
				t.Errorf("%s: %s project = %q, want %q", when, ss.Name, ss.Project, want[ss.Name])
			}
		}
	}
	check("before the poll loop tracks them")
	pollNTimes(s, 1)
	check("once tracked")
}
