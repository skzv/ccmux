package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

// TestPollOnce_UntargetableNameGoesUnrecordedQuietly — the session
// option writers now refuse every name no tmux target reaches by name
// alone (tmux.ErrUntargetable: "$1", and a dotted name such as one made
// outside ccmux). The poll loop can't do anything about that, so it
// neither writes on some other session nor logs a line per change.
func TestPollOnce_UntargetableNameGoesUnrecordedQuietly(t *testing.T) {
	s := newPollTestServer(t)
	idle := readFixture(t, "claude_v2_idle.txt")
	p := &fakePane{Pane: tmux.Pane{ID: "%1", Width: 120, Height: 40, Title: "✳ Claude Code"}, body: idle}
	f := newFakeTmux()
	f.addSession(tmux.Session{Name: "c-api.v2", Path: "/tmp", Created: time.Now().Add(-time.Hour)}, p)
	f.wire(s)
	var refused []string
	s.markReview = func(ctx context.Context, name string, r tmux.Review) error {
		err := tmux.SetSessionReview(ctx, name, r) // refuses before running tmux
		refused = append(refused, "review:"+name)
		return err
	}
	s.markSpinner = func(ctx context.Context, name, id string) error {
		err := tmux.SetSessionSpinner(ctx, name, id)
		refused = append(refused, "spinner:"+name)
		return err
	}
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	pollNTimes(s, 1)
	claudeTurn(t, s, f, p)
	if len(refused) < 2 {
		t.Fatalf("setup: the ticks asked for %v, want a review record and a spinner mark", refused)
	}
	if strings.Contains(buf.String(), "record") {
		t.Errorf("refused writes were logged:\n%s", buf.String())
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

// TestSessionNotFound_SetOptionWording — tmux's set-option reports a
// missing target as "no such session", not "can't find session". A
// review record queued for a session that ended or was renamed since the
// tick's list is not an error worth logging.
func TestSessionNotFound_SetOptionWording(t *testing.T) {
	for msg, want := range map[string]bool{
		"record session review: exit status 1 (no such session: =c-gone:)":  true,
		"tmux kill-session: exit status 1 (can't find session: =c-gone:)":   true,
		"record session review: exit status 1 (server exited unexpectedly)": false,
	} {
		if got := sessionNotFound(errors.New(msg)); got != want {
			t.Errorf("sessionNotFound(%q) = %v, want %v", msg, got, want)
		}
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

// codexTurnStarts starts a Codex turn on p (the prompt echoed, the
// working spinner animating) and polls until the daemon has seen it.
func codexTurnStarts(s *server, f *fakeTmux, p *fakePane, prompt string) {
	for _, frame := range []string{"⠋", "⠙"} {
		f.update(func() { p.body, p.Title = codexPane("> "+prompt+"\n\n• Working"), frame+" codex" })
		pollNTimes(s, 1)
	}
}

// TestPollOnce_TurnEndedWhileDaemonDownStaysUnreviewed — QA's repro: a
// prompt sent to a Codex session nobody is attached to makes it active
// and unreviewed (@ccmux_seen 0, @ccmux_state active). The daemon stops
// mid-turn, and the turn ends at Codex's caret (idle) while it is down.
// Had the daemon watched, active → idle would have pushed "finished"
// and left the session unreviewed. The restarted daemon's first look
// saw the state had moved on, judged it as it stood — not waiting for
// input, so reviewed — and wrote seen=1 over the record.
func TestPollOnce_TurnEndedWhileDaemonDownStaysUnreviewed(t *testing.T) {
	s := newPollTestServer(t)
	s.startedAt = time.Now()
	p := &fakePane{Pane: tmux.Pane{ID: "%1", Width: 120, Height: 40, Title: "codex"}, body: codexPane("")}
	f := newFakeTmux()
	f.addSession(tmux.Session{Name: "c-codex", Path: "/tmp", Agent: "codex", Created: time.Now().Add(-time.Hour)}, p)
	f.wire(s)
	pollNTimes(s, 2)
	codexTurnStarts(s, f, p, "fix it")
	want := tmux.Review{Recorded: true, Seen: false, Prompts: 0, State: string(agent.StateActive)}
	if got := f.reviewWrites("c-codex"); len(got) == 0 || got[len(got)-1] != want {
		t.Fatalf("setup: the record mid-turn: %+v, want %+v last", got, want)
	}

	// The daemon stops; the turn ends while it is down.
	f.update(func() { p.body, p.Title = codexPane("> fix it\n\n• Done."), "codex" })

	s2, pushes, bells := restartDaemon(t, f)
	pollNTimes(s2, 3)
	tr := s2.seen["c-codex"]
	if tr.state != agent.StateIdle || tr.seen {
		t.Errorf("after the restart: state=%s seen=%v, want idle, unreviewed", tr.state, tr.seen)
	}
	got := f.reviewWrites("c-codex")
	if last := got[len(got)-1]; last.Seen {
		t.Errorf("the restarted daemon recorded the finished turn as reviewed: %+v", last)
	}
	if n := pushes(); n != 0 || *bells != 0 {
		t.Errorf("the restart notified: pushes=%d bells=%d", n, *bells)
	}
}

// TestPollOnce_TurnCaughtMidFlightStaysUnreviewed — a restart that
// catches an unreviewed Codex turn still running reads it idle on the
// first look (the spinner title a first look can't believe; the caret
// under the output). That flipped the session to reviewed, and the rest
// of the turn — joined, so never announced — left it so. It stays
// unreviewed throughout, and still nothing notifies.
func TestPollOnce_TurnCaughtMidFlightStaysUnreviewed(t *testing.T) {
	s := newPollTestServer(t)
	s.startedAt = time.Now()
	p := &fakePane{Pane: tmux.Pane{ID: "%1", Width: 120, Height: 40, Title: "codex"}, body: codexPane("")}
	f := newFakeTmux()
	f.addSession(tmux.Session{Name: "c-codex", Path: "/tmp", Agent: "codex", Created: time.Now().Add(-time.Hour)}, p)
	f.wire(s)
	pollNTimes(s, 2)
	codexTurnStarts(s, f, p, "fix it")

	s2, pushes, bells := restartDaemon(t, f)
	pollNTimes(s2, 1)
	tr := s2.seen["c-codex"]
	if tr.state != agent.StateIdle {
		t.Fatalf("setup: the first look read the turn as %s, want idle (the case QA hit)", tr.state)
	}
	if tr.seen {
		t.Error("the first look marked an unreviewed turn in flight reviewed")
	}
	f.update(func() { p.Title = "⠹ codex" }) // still working
	pollNTimes(s2, 1)
	f.update(func() { p.Title = "⠸ codex" })
	pollNTimes(s2, 1)
	f.update(func() { p.body, p.Title = codexPane("> fix it\n\n• Done."), "codex" })
	pollNTimes(s2, 4)
	if tr.state != agent.StateIdle || tr.seen {
		t.Errorf("after the turn ended: state=%s seen=%v, want idle, unreviewed", tr.state, tr.seen)
	}
	if n := pushes(); n != 0 || *bells != 0 {
		t.Errorf("the joined turn's end notified: pushes=%d bells=%d", n, *bells)
	}
}

// TestPollOnce_ReviewedSessionThatSettledWhileDownIsUnreviewed — a
// session the user had reviewed that settled, while no daemon watched,
// into a state a watching daemon would have notified about comes back
// unreviewed, without a bell or push: a Codex turn finishing at its
// caret, Claude crashing to the shell. One that only went quiet (Claude
// idle is no turn's end: its turns end at the input box) keeps its mark.
func TestPollOnce_ReviewedSessionThatSettledWhileDownIsUnreviewed(t *testing.T) {
	for _, tc := range []struct {
		name, agent, body string
		wantState         agent.State
		wantSeen          bool
	}{
		{"codex finished", "codex", codexPane("> fix it\n\n• Done."), agent.StateIdle, false},
		{"claude crashed", "claude", readFixture(t, "claude_crashed_shell.txt"), agent.StateError, false},
		{"claude went quiet", "claude", "Compiling…\nwrote 3 files", agent.StateIdle, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeTmux()
			p := &fakePane{Pane: tmux.Pane{ID: "%1", Width: 120, Height: 40, Title: tc.agent}, body: tc.body}
			rec := tmux.Review{Recorded: true, Seen: true, Prompts: 2, State: string(agent.StateActive)}
			f.addSession(tmux.Session{Name: "c-away", Path: "/tmp", Agent: tc.agent, Created: time.Now().Add(-time.Hour), Review: rec}, p)
			s, pushes, bells := restartDaemon(t, f)
			pollNTimes(s, 3)
			tr := s.seen["c-away"]
			if tr.state != tc.wantState || tr.seen != tc.wantSeen || tr.promptCount != 2 {
				t.Errorf("state=%s seen=%v promptCount=%d, want %s, seen=%v, 2", tr.state, tr.seen, tr.promptCount, tc.wantState, tc.wantSeen)
			}
			if n := pushes(); n != 0 || *bells != 0 {
				t.Errorf("the first look notified: pushes=%d bells=%d", n, *bells)
			}
		})
	}
}

// TestFirstLookSeen pins the reviewed flag a first look gives a session
// it joined, for every shape of record.
func TestFirstLookSeen(t *testing.T) {
	const (
		active = agent.StateActive
		idle   = agent.StateIdle
		needs  = agent.StateNeedsInput
		errSt  = agent.StateError
	)
	rec := func(seen bool, st agent.State) tmux.Review {
		return tmux.Review{Recorded: true, Seen: seen, Prompts: 1, State: string(st)}
	}
	for _, tc := range []struct {
		name     string
		rec      tmux.Review
		attached bool
		id       agent.ID
		now      agent.State
		want     bool
	}{
		// No record: judged as it stands.
		{"no record, waiting", tmux.Review{}, false, agent.IDClaude, needs, false},
		{"no record, idle codex", tmux.Review{}, false, agent.IDCodex, idle, true},
		{"no record, crashed", tmux.Review{}, false, agent.IDClaude, errSt, true},
		{"no record, plain shell", tmux.Review{}, false, shellAgentID, idle, true},
		{"no record, attached", tmux.Review{}, true, agent.IDClaude, needs, true},
		// Unreviewed: stays so, whatever the state now, unless someone looks.
		{"unreviewed, unchanged", rec(false, needs), false, agent.IDClaude, needs, false},
		{"unreviewed, codex finished since", rec(false, active), false, agent.IDCodex, idle, false},
		{"unreviewed, still working", rec(false, active), false, agent.IDCodex, active, false},
		{"unreviewed, now a shell", rec(false, needs), false, shellAgentID, idle, false},
		{"unreviewed, attached", rec(false, active), true, agent.IDCodex, idle, true},
		// Reviewed and unchanged.
		{"reviewed, unchanged", rec(true, needs), false, agent.IDClaude, needs, true},
		{"reviewed, still idle", rec(true, idle), false, agent.IDCodex, idle, true},
		// Reviewed, then settled where a watching daemon would have notified.
		{"reviewed, waiting since", rec(true, active), false, agent.IDClaude, needs, false},
		{"reviewed, codex finished since", rec(true, active), false, agent.IDCodex, idle, false},
		{"reviewed, codex finished after approval", rec(true, needs), false, agent.IDCodex, idle, false},
		{"reviewed, crashed since", rec(true, active), false, agent.IDClaude, errSt, false},
		{"reviewed, crashed since, attached", rec(true, active), true, agent.IDClaude, errSt, true},
		// Reviewed, then somewhere that isn't news.
		{"reviewed, claude went quiet", rec(true, active), false, agent.IDClaude, idle, true},
		{"reviewed, working now", rec(true, idle), false, agent.IDCodex, active, true},
		{"reviewed, agent exited to a shell", rec(true, needs), false, shellAgentID, idle, true},
		// Reviewed with no state to compare: judged as it stands.
		{"reviewed, recorded unknown", rec(true, agent.StateUnknown), false, agent.IDCodex, idle, true},
		{"reviewed, recorded unknown, waiting", rec(true, agent.StateUnknown), false, agent.IDClaude, needs, false},
		{"reviewed, no state kept", tmux.Review{Recorded: true, Seen: true}, false, agent.IDCodex, idle, true},
	} {
		if got := firstLookSeen(tc.rec, tc.attached, tc.id, tc.now); got != tc.want {
			t.Errorf("%s: firstLookSeen(%+v, attached=%v, %s, %s) = %v, want %v", tc.name, tc.rec, tc.attached, tc.id, tc.now, got, tc.want)
		}
	}
}
