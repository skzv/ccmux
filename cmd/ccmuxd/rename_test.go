package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
)

// TestHandleRename_RejectsInvalidNames pins the validation guard added
// to handleRename — same rule createSession enforces, for the same
// reason. Without it a peer could rename a session to "victim:0" and
// then subsequent send-keys parses session=victim, window=0, letting
// the attacker inject keystrokes into a totally unrelated tmux session.
//
// Unit-level test: the validation runs before tmux is invoked, so no
// real tmux server is needed.
func TestHandleRename_RejectsInvalidNames(t *testing.T) {
	s := &server{
		cfg: config.Config{
			Daemon: config.DaemonConfig{TailnetPort: 7474},
		},
	}

	for _, newName := range []string{"victim:0", "bad/name", "bad\\name", "x#{session_id}", "tab\tname"} {
		t.Run(newName, func(t *testing.T) {
			body, _ := json.Marshal(daemon.RenameRequest{Name: newName})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/sessions/old/rename", bytes.NewReader(body))
			s.handleRename(rec, req, "old")
			if rec.Code != http.StatusBadRequest {
				t.Errorf("rename to %q: status = %d, want 400; body=%s", newName, rec.Code, rec.Body)
			}
			if !strings.Contains(rec.Body.String(), badNewSessionNameMsg) {
				t.Errorf("rename to %q: body should explain the rule; got %q", newName, rec.Body)
			}
		})
	}
}

// TestHandleRename_RejectsWrongMethod — POST-only, mirrors handleKill.
func TestHandleRename_RejectsWrongMethod(t *testing.T) {
	s := &server{cfg: config.Config{}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/old/rename", nil)
	s.handleRename(rec, req, "old")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET status = %d, want 405", rec.Code)
	}
}

// postRename drives handleRename for oldName → newName with the tmux
// rename faked out.
func postRename(t *testing.T, s *server, oldName, newName string) *httptest.ResponseRecorder {
	t.Helper()
	s.rename = func(context.Context, string, string) error { return nil }
	body, _ := json.Marshal(daemon.RenameRequest{Name: newName})
	rec := httptest.NewRecorder()
	s.handleRename(rec, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+oldName+"/rename", bytes.NewReader(body)), oldName)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename %s → %s: status %d: %s", oldName, newName, rec.Code, rec.Body)
	}
	return rec
}

// publishedEvents drains whatever has been published to ch so far.
func publishedEvents(ch chan daemon.SessionEvent) []daemon.SessionEvent {
	var out []daemon.SessionEvent
	for {
		select {
		case ev := <-ch:
			out = append(out, ev)
		default:
			return out
		}
	}
}

// TestHandleRename_CreatedEventCarriesTrackedState — the "created" event
// announcing a renamed session had an empty State, Path and Agent and
// Seen=false, so every event-stream client showed the renamed session as
// a blank row demanding attention until its next state change.
func TestHandleRename_CreatedEventCarriesTrackedState(t *testing.T) {
	s := newPollTestServer(t)
	s.seen["old"] = &tracked{
		state: agent.StateNeedsInput, seen: false, agentID: agent.IDCodex,
		projectPath: "/work/proj", promptCount: 2,
	}
	ch := s.events.Subscribe()
	defer s.events.Unsubscribe(ch)

	postRename(t, s, "old", "new")

	evs := publishedEvents(ch)
	if len(evs) != 2 || evs[0].Kind != "killed" || evs[0].Session.Name != "old" || evs[1].Kind != "created" {
		t.Fatalf("events = %+v, want killed old then created new", evs)
	}
	got := evs[1].Session
	want := daemon.SessionState{
		Name: "new", Host: "local", Path: "/work/proj", State: string(agent.StateNeedsInput),
		Agent: string(agent.IDCodex), PromptCount: 2, Seen: false,
	}
	if got != want {
		t.Errorf("created event session = %+v\nwant %+v", got, want)
	}
}

// TestHandleRename_UntrackedSessionAnnouncedAsUnknown — a session the
// poll loop hasn't picked up yet is announced the way listSessions
// shows it: state unknown, nothing to review.
func TestHandleRename_UntrackedSessionAnnouncedAsUnknown(t *testing.T) {
	s := newPollTestServer(t)
	ch := s.events.Subscribe()
	defer s.events.Unsubscribe(ch)

	postRename(t, s, "old", "new")

	evs := publishedEvents(ch)
	if len(evs) != 2 {
		t.Fatalf("events = %+v, want killed + created", evs)
	}
	if got := evs[1].Session; got.State != string(agent.StateUnknown) || !got.Seen {
		t.Errorf("created event = %+v, want state unknown and seen", got)
	}
}

// TestHandleRename_SameNameKeepsTrackedState — tmux accepts a rename to
// the session's own name. renameTracked then stored and deleted the same
// key, wiping the tracked state (prompt count, unreviewed flag), and the
// handler announced the session as killed and re-created.
func TestHandleRename_SameNameKeepsTrackedState(t *testing.T) {
	s := newPollTestServer(t)
	s.seen["c-a"] = &tracked{state: agent.StateActive, promptCount: 3, seen: false}
	ch := s.events.Subscribe()
	defer s.events.Unsubscribe(ch)

	postRename(t, s, "c-a", "c-a")

	tr, ok := s.seen["c-a"]
	if !ok {
		t.Fatal("same-name rename dropped the tracked entry")
	}
	if tr.promptCount != 3 || tr.seen {
		t.Errorf("tracked state changed: promptCount=%d seen=%v", tr.promptCount, tr.seen)
	}
	if evs := publishedEvents(ch); len(evs) != 0 {
		t.Errorf("same-name rename published %+v, want nothing", evs)
	}
}

// TestHandleRename_TrimsName — the create handlers trim the name they're
// given; rename passed "  new " to tmux verbatim, leaving a session
// whose name nobody types back correctly.
func TestHandleRename_TrimsName(t *testing.T) {
	s := newPollTestServer(t)
	var got string
	s.rename = func(_ context.Context, _, newName string) error { got = newName; return nil }
	body, _ := json.Marshal(daemon.RenameRequest{Name: "  new \t"})
	rec := httptest.NewRecorder()
	s.handleRename(rec, httptest.NewRequest(http.MethodPost, "/v1/sessions/old/rename", bytes.NewReader(body)), "old")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got != "new" {
		t.Errorf("tmux rename got %q, want %q", got, "new")
	}
}

// TestHandleRename_RejectsEmptyName — the existing guard before the new
// character validation.
func TestHandleRename_RejectsEmptyName(t *testing.T) {
	s := &server{cfg: config.Config{}}
	body, _ := json.Marshal(daemon.RenameRequest{Name: ""})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/old/rename", bytes.NewReader(body))
	s.handleRename(rec, req, "old")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}
