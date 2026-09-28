package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tmux"
)

// claudeTurn runs one Claude turn on p: its spinner title and status
// line while it works, then its input box again.
func claudeTurn(t *testing.T, s *server, f *fakeTmux, p *fakePane) {
	t.Helper()
	working, idle := readFixture(t, "claude_v2_working.txt"), readFixture(t, "claude_v2_idle.txt")
	f.update(func() { p.body, p.Title = working, "⠋ Fix flaky poll test" })
	pollNTimes(s, 2)
	f.update(func() { p.body, p.Title = idle, "✳ Fix flaky poll test" })
	pollNTimes(s, 3)
}

// restartDaemon is a fresh daemon (nothing in memory) over the same
// tmux server, counting its bells and pushes.
func restartDaemon(t *testing.T, f *fakeTmux) (*server, func() int, *int) {
	t.Helper()
	s := newPollTestServer(t)
	s.startedAt = time.Now()
	pushes := countPushes(t, s)
	bells := countBells(s)
	f.wire(s)
	return s, pushes, bells
}

// TestPollOnce_ReviewSurvivesRestart — every daemon restart (every
// `ccmux update`, every upgrade) forgot each session's reviewed flag and
// prompt count: a session the user had looked at came back unreviewed
// because it sat waiting for input, and its prompt count went back to 0.
// The session keeps both (its review record), and a restarted daemon
// reads them back — without notifying, and without writing the record
// again when nothing changed.
func TestPollOnce_ReviewSurvivesRestart(t *testing.T) {
	s := newPollTestServer(t)
	s.startedAt = time.Now()
	idle := readFixture(t, "claude_v2_idle.txt")
	p := &fakePane{Pane: tmux.Pane{ID: "%1", Width: 120, Height: 40, Title: "✳ Claude Code"}, body: idle}
	f := newFakeTmux()
	f.addSession(tmux.Session{Name: "c-rev", Path: "/tmp", Created: time.Now().Add(-time.Hour)}, p)
	f.wire(s)
	pollNTimes(s, 2) // joined, then seen to settle
	claudeTurn(t, s, f, p)
	claudeTurn(t, s, f, p)
	f.setAttached("c-rev", true) // the user looks
	pollNTimes(s, 1)
	f.setAttached("c-rev", false)
	pollNTimes(s, 1)
	if tr := s.seen["c-rev"]; tr.promptCount != 2 || !tr.seen || tr.state != agent.StateNeedsInput {
		t.Fatalf("setup: promptCount=%d seen=%v state=%s, want 2, reviewed, needs_input", tr.promptCount, tr.seen, tr.state)
	}
	written := len(f.reviewWrites("c-rev"))

	s2, pushes, bells := restartDaemon(t, f)
	pollNTimes(s2, 3)
	tr := s2.seen["c-rev"]
	if tr.promptCount != 2 || !tr.seen {
		t.Errorf("after a restart: promptCount=%d seen=%v, want 2, reviewed", tr.promptCount, tr.seen)
	}
	if got := pushes(); got != 0 || *bells != 0 {
		t.Errorf("the restart notified: pushes=%d bells=%d", got, *bells)
	}
	if got := len(f.reviewWrites("c-rev")); got != written {
		t.Errorf("the restarted daemon rewrote an unchanged record: %d writes, want %d", got, written)
	}

	// The count carries on from where it was.
	claudeTurn(t, s2, f, p)
	if tr.promptCount != 3 || tr.seen || *bells != 1 {
		t.Errorf("the next turn: promptCount=%d seen=%v bells=%d, want 3, unreviewed, 1", tr.promptCount, tr.seen, *bells)
	}
}

// TestPollOnce_UnreviewedFinishSurvivesRestart — Codex ends a turn at its
// caret (idle, the "finished" push). Nobody has looked at it, but the
// restarted daemon's first look took any session not waiting for input
// for reviewed. The record says it isn't, and the session is still where
// the record left it.
func TestPollOnce_UnreviewedFinishSurvivesRestart(t *testing.T) {
	s := newPollTestServer(t)
	s.startedAt = time.Now()
	pushes := countPushes(t, s)
	p := &fakePane{Pane: tmux.Pane{ID: "%1", Width: 120, Height: 40, Title: "codex"}, body: codexPane("")}
	f := newFakeTmux()
	f.addSession(tmux.Session{Name: "c-codex", Path: "/tmp", Agent: "codex", Created: time.Now().Add(-time.Hour)}, p)
	f.wire(s)
	pollNTimes(s, 2)
	codexTurn(s, f, p, "fix it")
	if tr := s.seen["c-codex"]; tr.state != agent.StateIdle || tr.seen || pushes() != 1 {
		t.Fatalf("setup: state=%s seen=%v pushes=%d, want idle, unreviewed, 1", tr.state, tr.seen, pushes())
	}

	s2, pushes2, _ := restartDaemon(t, f)
	pollNTimes(s2, 2)
	if tr := s2.seen["c-codex"]; tr.seen {
		t.Error("after a restart: a finished turn nobody looked at shows reviewed")
	}
	if got := pushes2(); got != 0 {
		t.Errorf("the restart notified: pushes=%d", got)
	}
}

// TestPollOnce_ReviewRecordIsCheckedAgainstTheState — the record is read
// back only while the session is where it left it. A turn that was
// running when the daemon went away and has ended since is a prompt
// nobody has looked at, whatever the record says; a session found as the
// record describes it is restored as it was.
func TestPollOnce_ReviewRecordIsCheckedAgainstTheState(t *testing.T) {
	idle := readFixture(t, "claude_v2_idle.txt")
	for _, tc := range []struct {
		name     string
		recorded string
		wantSeen bool
	}{
		{"moved on since", string(agent.StateActive), false},
		{"still waiting", string(agent.StateNeedsInput), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeTmux()
			p := &fakePane{Pane: tmux.Pane{ID: "%1", Width: 120, Height: 40, Title: "✳ Claude Code"}, body: idle}
			rec := tmux.Review{Recorded: true, Seen: true, Prompts: 4, State: tc.recorded}
			f.addSession(tmux.Session{Name: "c-away", Path: "/tmp", Created: time.Now().Add(-time.Hour), Review: rec}, p)
			s, pushes, bells := restartDaemon(t, f)
			pollNTimes(s, 3)
			tr := s.seen["c-away"]
			if tr.state != agent.StateNeedsInput || tr.seen != tc.wantSeen || tr.promptCount != 4 {
				t.Errorf("state=%s seen=%v promptCount=%d, want needs_input, seen=%v, 4", tr.state, tr.seen, tr.promptCount, tc.wantSeen)
			}
			if got := pushes(); got != 0 || *bells != 0 {
				t.Errorf("the first look notified: pushes=%d bells=%d", got, *bells)
			}
		})
	}
}

// TestPollOnce_ReviewFollowsRawRename — a session renamed straight
// through tmux shows up to the daemon as a new name for an old session.
// It lost its reviewed flag and prompt count with its old name; tmux
// options move with the session, so the record brings them along.
func TestPollOnce_ReviewFollowsRawRename(t *testing.T) {
	s := newPollTestServer(t)
	s.startedAt = time.Now().Add(-time.Hour)
	bells := countBells(s)
	idle := readFixture(t, "claude_v2_idle.txt")
	p := &fakePane{Pane: tmux.Pane{ID: "%1", Width: 120, Height: 40, Title: "✳ Claude Code"}, body: idle}
	f := newFakeTmux()
	f.addSession(tmux.Session{Name: "c-old", Path: "/tmp", Created: time.Now().Add(-2 * time.Hour)}, p)
	f.wire(s)
	pollNTimes(s, 2)
	claudeTurn(t, s, f, p)
	f.setAttached("c-old", true)
	pollNTimes(s, 1)
	f.setAttached("c-old", false)
	pollNTimes(s, 1)
	before := *bells

	f.renameSession("c-old", "c-new")
	pollNTimes(s, 3)
	tr, ok := s.seen["c-new"]
	if !ok {
		t.Fatal("the renamed session isn't tracked")
	}
	if tr.promptCount != 1 || !tr.seen {
		t.Errorf("after a rename in tmux: promptCount=%d seen=%v, want 1, reviewed", tr.promptCount, tr.seen)
	}
	if *bells != before {
		t.Errorf("the rename rang the bell: %d → %d", before, *bells)
	}
}

// TestPollOnce_ReviewWrittenOnlyOnChange — the record is written when
// the reviewed flag, the prompt count or the state changes, never on a
// tick that changes none of them: no tmux call per session per tick.
func TestPollOnce_ReviewWrittenOnlyOnChange(t *testing.T) {
	s := newPollTestServer(t)
	idle := readFixture(t, "claude_v2_idle.txt")
	p := &fakePane{Pane: tmux.Pane{ID: "%1", Width: 120, Height: 40, Title: "✳ Claude Code"}, body: idle}
	f := newFakeTmux()
	f.addSession(tmux.Session{Name: "c-quiet", Path: "/tmp", Created: time.Now().Add(-time.Hour)}, p)
	f.wire(s)
	pollNTimes(s, 1)
	first := f.reviewWrites("c-quiet")
	if len(first) != 1 || first[0] != (tmux.Review{Recorded: true, Seen: false, Prompts: 0, State: string(agent.StateNeedsInput)}) {
		t.Fatalf("first look: writes %+v, want one record of an unreviewed session waiting", first)
	}
	pollNTimes(s, 8)
	if got := f.reviewWrites("c-quiet"); len(got) != 1 {
		t.Fatalf("a session sitting still: %d writes over 9 ticks, want 1: %+v", len(got), got)
	}
	claudeTurn(t, s, f, p) // active, then needs_input with a prompt counted
	got := f.reviewWrites("c-quiet")
	if len(got) != 3 {
		t.Fatalf("a turn: %d writes, want 3 (the first look, the turn starting, its end): %+v", len(got), got)
	}
	if last := got[len(got)-1]; last != (tmux.Review{Recorded: true, Seen: false, Prompts: 1, State: string(agent.StateNeedsInput)}) {
		t.Errorf("the record after the turn: %+v", last)
	}
}

// TestPollOnce_PlainShellIsNotRecorded — a session running a plain shell
// has nothing worth keeping; a tmux session the user made outside ccmux
// isn't written to just for being listed.
func TestPollOnce_PlainShellIsNotRecorded(t *testing.T) {
	s := newPollTestServer(t)
	p := &fakePane{Pane: tmux.Pane{ID: "%1", Width: 120, Height: 40, Title: "host.local", Command: "zsh"}, body: "user@host ~ % "}
	f := newFakeTmux()
	f.addSession(tmux.Session{Name: "scratch", Path: "/tmp", Created: time.Now().Add(-time.Hour)}, p)
	f.wire(s)
	pollNTimes(s, 4)
	if tr := s.seen["scratch"]; tr.agentID != shellAgentID {
		t.Fatalf("setup: agent = %s, want a shell", tr.agentID)
	}
	if got := f.reviewWrites("scratch"); len(got) != 0 {
		t.Errorf("a plain shell got a review record: %+v", got)
	}
}

// TestListSessions_ReviewBeforeFirstTick — GET /v1/sessions answered
// between a restart and the poll loop's first look reports a session's
// recorded prompt count and reviewed flag, not the blank defaults.
func TestListSessions_ReviewBeforeFirstTick(t *testing.T) {
	s := newPollTestServer(t)
	f := newFakeTmux()
	rec := tmux.Review{Recorded: true, Seen: false, Prompts: 5, State: string(agent.StateNeedsInput)}
	f.addSession(tmux.Session{Name: "c-listed", Path: "/tmp", Created: time.Now().Add(-time.Hour), Review: rec})
	f.addSession(tmux.Session{Name: "c-bare", Path: "/tmp", Created: time.Now().Add(-time.Hour)})
	f.wire(s)
	w := httptest.NewRecorder()
	s.listSessions(w, httptest.NewRequest(http.MethodGet, "/v1/sessions", nil))
	var out []daemon.SessionState
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	got := map[string]daemon.SessionState{}
	for _, st := range out {
		got[st.Name] = st
	}
	if st := got["c-listed"]; st.PromptCount != 5 || st.Seen {
		t.Errorf("c-listed: prompt_count=%d seen=%v, want 5, unreviewed", st.PromptCount, st.Seen)
	}
	if st := got["c-bare"]; st.PromptCount != 0 || !st.Seen {
		t.Errorf("c-bare: prompt_count=%d seen=%v, want 0, reviewed", st.PromptCount, st.Seen)
	}
}
