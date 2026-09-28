package main

import (
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/tmux"
)

// answered is a Claude pane that finished a turn: the prompt and the
// answer above the input box.
func answered(idle, prompt, answer string) string {
	return strings.Replace(idle, "  Get to finished", "❯ "+prompt+"\n\n⏺ "+answer+"\n\n  Get to finished", 1)
}

// TestPollOnce_TypingBeforeAnySpinnerDoesNotNotify — typing into a
// Claude input box and pausing notified (a bell, a push when detached,
// promptCount+1) on every pause until the daemon had seen a spinner
// title in that session: in every fresh session, and in every session
// after a daemon restart. Typing only changes the input box; that is
// not the agent working, spinner or not.
func TestPollOnce_TypingBeforeAnySpinnerDoesNotNotify(t *testing.T) {
	for _, tc := range []struct {
		name    string
		created time.Time
	}{
		{"new session", time.Now()},
		{"after a daemon restart", time.Now().Add(-time.Hour)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newPollTestServer(t)
			s.startedAt = time.Now().Add(-time.Minute)
			pushes := countPushes(t, s)
			bells := countBells(s)
			idle := readFixture(t, "claude_v2_idle.txt")
			p := &fakePane{Pane: tmux.Pane{ID: "%1", Width: 120, Height: 40, Title: "✳ Claude Code"}, body: idle}
			f := newFakeTmux()
			f.addSession(tmux.Session{Name: "c-fresh", Path: "/tmp", Created: tc.created}, p)
			f.wire(s)
			pollNTimes(s, 3) // settled at the input box
			// (A session found waiting after a restart starts unreviewed.)
			seen := s.seen["c-fresh"].seen

			for _, text := range []string{"fix", "fix the flaky", "fix the flaky poll test", "fix the flaky poll test\n  and its helper"} {
				f.update(func() { p.body = typeInto(idle, text) })
				pollNTimes(s, 1) // typing
				pollNTimes(s, 3) // the pause
			}
			tr := s.seen["c-fresh"]
			if tr.state != agent.StateNeedsInput {
				t.Fatalf("state = %s after typing and pausing, want needs_input", tr.state)
			}
			if got := pushes(); got != 0 || *bells != 0 || tr.promptCount != 0 || tr.seen != seen {
				t.Errorf("typing notified: pushes=%d bells=%d promptCount=%d seen=%v, want 0/0/0/%v", got, *bells, tr.promptCount, tr.seen, seen)
			}

			// Submitting is a turn, spinner title or not: its answer
			// lands above the box.
			f.update(func() { p.body = answered(idle, "fix the flaky poll test", "Fixed.") })
			pollNTimes(s, 1)
			pollNTimes(s, 3)
			if got := pushes(); got != 1 || *bells != 1 || tr.promptCount != 1 {
				t.Errorf("a turn after typing: pushes=%d bells=%d promptCount=%d, want 1/1/1", got, *bells, tr.promptCount)
			}
		})
	}
}

// TestPollOnce_TurnShorterThanAPollNotifies — once the daemon had seen a
// session's spinner title, only the spinner counted as work, so a turn
// that started and ended between two polls (a quick answer, a 0.25s
// spinner) never notified. Its answer is still above the input box.
func TestPollOnce_TurnShorterThanAPollNotifies(t *testing.T) {
	s := newPollTestServer(t)
	pushes := countPushes(t, s)
	f, p, bells := waitingSession(t, s, false) // one spinner turn: bell 1, push 1
	idle := p.body
	for i, answer := range []string{"Yes.", "No.", "Done."} {
		f.update(func() { p.body = answered(idle, "quick question", answer) })
		pollNTimes(s, 1) // the whole turn happened between two polls
		pollNTimes(s, 3)
		tr := s.seen["c-wait"]
		if want := i + 2; *bells != want || tr.promptCount != want || pushes() != want {
			t.Fatalf("short turn %d: bells=%d promptCount=%d pushes=%d, want %d each", i+1, *bells, tr.promptCount, pushes(), want)
		}
	}
}

// TestTurn_BusyBodyIsWork — a body showing a turn running (Claude's
// status line, an agent's working footer) is work like a spinner title,
// during startup too: an agent launched with a first prompt is working
// on it.
func TestTurn_BusyBodyIsWork(t *testing.T) {
	busy := evidence{busy: true, separated: true}
	bells, pushes := runEvidence(&turn{startup: true}, false, []evTick{
		{agent.StateActive, busy}, {agent.StateActive, busy}, {agent.StateNeedsInput, evidence{separated: true}},
	})
	if bells != 1 || pushes != 1 {
		t.Errorf("first prompt worked on at startup: bells=%d pushes=%d, want 1/1", bells, pushes)
	}
	// New output above the box during startup is the agent drawing its
	// banner and tips, not a turn.
	out := evidence{separated: true, output: true}
	bells, pushes = runEvidence(&turn{startup: true}, false, []evTick{
		{agent.StateActive, out}, {agent.StateNeedsInput, evidence{separated: true}},
	})
	if bells != 0 || pushes != 0 {
		t.Errorf("startup output: bells=%d pushes=%d, want 0/0", bells, pushes)
	}
}
