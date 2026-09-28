package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/tmux"
)

// countBells makes s count needs-input bells.
func countBells(s *server) *int {
	n := 0
	s.cfg.Notifications.Bell = true
	s.bell = func(context.Context, string) error { n++; return nil }
	return &n
}

// TestPollOnce_ReadsAgentPaneNotActivePane — a session's bare target is
// its active pane. With the agent in window 0 and the user in a shell
// window next to it, the daemon read the shell: the session showed as a
// crashed agent (error) and the agent's turn end never rang. The poll
// loop must read the pane the agent runs in, whichever one is active.
func TestPollOnce_ReadsAgentPaneNotActivePane(t *testing.T) {
	s := newPollTestServer(t)
	bells := countBells(s)
	working, idle := readFixture(t, "claude_v2_working.txt"), readFixture(t, "claude_v2_idle.txt")
	agentP := &fakePane{Pane: tmux.Pane{ID: "%3", Width: 120, Height: 40, Title: "✳ Claude Code"}, body: idle}
	shellP := &fakePane{Pane: tmux.Pane{ID: "%4", Window: 1, Width: 120, Height: 40, Active: true, Title: "host.local"}, body: "user@host ~ % "}
	f := newFakeTmux()
	f.addSession(tmux.Session{Name: "c-multi", Path: "/tmp", Created: time.Now().Add(-time.Hour)}, agentP, shellP)
	f.wire(s)

	pollNTimes(s, 2) // the first look joins it; the second sees it settled (turn.joined)
	f.update(func() { agentP.body, agentP.Title = working, "⠋ Fix flaky poll test" })
	pollNTimes(s, 2)
	if st := s.seen["c-multi"].state; st != agent.StateActive {
		t.Fatalf("agent working in window 0 while a shell window is active: state = %s, want active", st)
	}
	// The agent's turn ends while the user sits in the shell window.
	f.update(func() { agentP.body, agentP.Title = idle, "✳ Fix flaky poll test" })
	pollNTimes(s, 3)

	if st := s.seen["c-multi"].state; st != agent.StateNeedsInput {
		t.Errorf("state = %s, want needs_input (the agent's pane, not the active shell window)", st)
	}
	if *bells != 1 {
		t.Errorf("bells = %d, want 1 for the agent's turn ending", *bells)
	}
	for _, id := range f.paneReads() {
		if id != "%3" {
			t.Errorf("captured pane %s, want only the agent's %%3", id)
		}
	}
}

// TestPollOnce_AgentPaneSticksAcrossSplits — the agent pane is resolved
// once (the session's oldest pane) and then kept while it exists, even
// when a pane with a lower id and index lands in front of it
// (join-pane, or split-window -b); it is re-resolved only when it goes
// away.
func TestPollOnce_AgentPaneSticksAcrossSplits(t *testing.T) {
	s := newPollTestServer(t)
	agentP := &fakePane{Pane: tmux.Pane{ID: "%5", Width: 80, Height: 24}, body: "agent output"}
	f := newFakeTmux()
	ts := tmux.Session{Name: "c-split", Path: "/tmp", Created: time.Now().Add(-time.Hour)}
	f.addSession(ts, agentP)
	f.wire(s)
	pollNTimes(s, 1)

	// The pane the session was created with exits and a newer one takes
	// over; then a lower-id pane is joined in front of it: the daemon
	// keeps reading %8.
	f.addSession(ts, &fakePane{Pane: tmux.Pane{ID: "%8", Width: 80, Height: 24, Active: true}, body: "replacement"})
	pollNTimes(s, 1)
	f.addSession(ts,
		&fakePane{Pane: tmux.Pane{ID: "%2", Width: 80, Height: 12}, body: "joined from elsewhere"},
		&fakePane{Pane: tmux.Pane{ID: "%8", Index: 1, Width: 80, Height: 12, Active: true}, body: "replacement"})
	pollNTimes(s, 1)

	reads := f.paneReads()
	if len(reads) != 3 || reads[0] != "%5" || reads[1] != "%8" || reads[2] != "%8" {
		t.Errorf("pane reads = %v, want [%%5 %%8 %%8]", reads)
	}
}

// shortSpinnerStaleness shrinks the spinner-staleness floor so an idle
// threshold of 50ms (pollNTimes) makes a spinner stale after 150ms.
func shortSpinnerStaleness(t *testing.T) {
	t.Helper()
	prev := spinnerStaleFloor
	spinnerStaleFloor = 0
	t.Cleanup(func() { spinnerStaleFloor = prev })
}

// crashedPane is what a Claude session looks like after the agent
// exited mid-turn and its launch chain fell back to a shell.
const crashedPane = "⏺ Refactoring the poll loop…\nzsh: abort      claude\nuser@host ~ % "

// TestPollOnce_StaleSpinnerTitleDoesNotPinActive — an agent that crashed
// mid-turn left its braille-spinner OSC title behind (tmux keeps
// #{pane_title} after the process exits). The spinner rule outranks the
// shell-prompt rule, so the session read as active forever and the
// sleep lock (held while any session is active) was never released. A
// spinner nothing is animating any more must stop counting as work —
// and one the daemon has never seen move, from its very first look.
func TestPollOnce_StaleSpinnerTitleDoesNotPinActive(t *testing.T) {
	shortSpinnerStaleness(t)
	s := newPollTestServer(t)
	p := &fakePane{Pane: tmux.Pane{ID: "%1", Width: 80, Height: 24, Title: "✳ Refactor poll loop"}, body: readFixture(t, "claude_v2_idle.txt")}
	f := newFakeTmux()
	f.addSession(tmux.Session{Name: "c-crash", Path: "/tmp", Created: time.Now()}, p)
	f.wire(s)
	pollNTimes(s, 1)

	// The agent starts a turn (the spinner is set: a change the daemon
	// sees) and crashes to a shell.
	f.update(func() { p.body, p.Title = crashedPane, "⠋ Refactor poll loop" })
	pollNTimes(s, 1)
	if st := s.seen["c-crash"].state; st != agent.StateActive {
		t.Fatalf("a spinner just set: state = %s, want active", st)
	}
	pollNTimes(s, 5) // 300ms with nothing changing: well past 150ms
	if st := s.seen["c-crash"].state; st != agent.StateError {
		t.Errorf("stale spinner over a shell prompt: state = %s, want error (not active, which holds the sleep lock)", st)
	}
}

// TestPollOnce_RestartWithLeftoverSpinnerStaysQuiet — a session whose
// agent crashed mid-turn keeps the dead agent's spinner title. After a
// daemon restart the first look set the title's "last change" to that
// moment, so the spinner was believed: the session showed active for
// the stale window (sleep lock held), then settled into error or
// needs_input with a NEW bell, push and prompt count — on every
// restart. A spinner the daemon hasn't seen move is not believed, and
// a session it joined doesn't notify before it has settled once.
func TestPollOnce_RestartWithLeftoverSpinnerStaysQuiet(t *testing.T) {
	shortSpinnerStaleness(t)
	for _, tc := range []struct {
		tag  string
		want agent.State
	}{
		{"claude", agent.StateError},
		{"codex", agent.StateNeedsInput}, // codex has no shell-prompt rule: quiet reads as waiting
	} {
		t.Run(tc.tag, func(t *testing.T) {
			s := newPollTestServer(t)
			s.startedAt = time.Now()
			pushes := countPushes(t, s)
			bells := countBells(s)
			f := newFakeTmux()
			f.addSession(tmux.Session{Name: "c-crash", Path: "/tmp", Agent: tc.tag, Created: time.Now().Add(-time.Hour)},
				&fakePane{Pane: tmux.Pane{ID: "%1", Width: 80, Height: 24, Title: "⠙ Refactor poll loop"}, body: crashedPane})
			f.wire(s)

			for i := 0; i < 6; i++ { // well past the 150ms stale window
				pollNTimes(s, 1)
				if st := s.seen["c-crash"].state; st != tc.want {
					t.Fatalf("tick %d: state = %s, want %s from the first look (active holds the sleep lock)", i, st, tc.want)
				}
			}
			tr := s.seen["c-crash"]
			if got := pushes(); got != 0 || *bells != 0 || tr.promptCount != 0 {
				t.Errorf("restart re-notified: pushes=%d bells=%d promptCount=%d, want 0", got, *bells, tr.promptCount)
			}
		})
	}
}

// TestPollOnce_JoinedMidTurnFirstSettleIsQuiet — a live Claude caught
// mid-turn by a daemon restart shows active (its status line says so),
// and the end of that turn is not announced: the daemon didn't watch it
// start. Its next turn is.
func TestPollOnce_JoinedMidTurnFirstSettleIsQuiet(t *testing.T) {
	s := newPollTestServer(t)
	s.startedAt = time.Now()
	pushes := countPushes(t, s)
	bells := countBells(s)
	working, idle := readFixture(t, "claude_v2_working.txt"), readFixture(t, "claude_v2_idle.txt")
	p := &fakePane{Pane: tmux.Pane{ID: "%1", Width: 120, Height: 40, Title: "⠋ Fix flaky poll test"}, body: working}
	f := newFakeTmux()
	f.addSession(tmux.Session{Name: "c-busy", Path: "/tmp", Created: time.Now().Add(-time.Hour)}, p)
	f.wire(s)

	pollNTimes(s, 1)
	if st := s.seen["c-busy"].state; st != agent.StateActive {
		t.Fatalf("first look at a working Claude: state = %s, want active", st)
	}
	f.update(func() { p.Title = "⠙ Fix flaky poll test" })
	pollNTimes(s, 1)
	f.update(func() { p.body, p.Title = idle, "✳ Fix flaky poll test" })
	pollNTimes(s, 3)
	tr := s.seen["c-busy"]
	if tr.state != agent.StateNeedsInput {
		t.Fatalf("state = %s, want needs_input", tr.state)
	}
	if got := pushes(); got != 0 || *bells != 0 || tr.promptCount != 0 {
		t.Errorf("the turn in flight at restart notified: pushes=%d bells=%d promptCount=%d, want 0", got, *bells, tr.promptCount)
	}

	f.update(func() { p.body, p.Title = working, "⠋ Next task" })
	pollNTimes(s, 2)
	f.update(func() { p.body, p.Title = idle, "✳ Next task" })
	pollNTimes(s, 3)
	if got := pushes(); got != 1 || *bells != 1 || tr.promptCount != 1 {
		t.Errorf("the next turn: pushes=%d bells=%d promptCount=%d, want 1/1/1", got, *bells, tr.promptCount)
	}
}

// TestLiveTitle pins when a spinner title is believed, and that a stale
// one is handed to the classifier without anything the spinner rule (or
// the legacy trimmed check) would still read as working.
func TestLiveTitle(t *testing.T) {
	now := time.Now()
	const stale = 10 * time.Second
	for _, tc := range []struct {
		title   string
		life    time.Duration // since the last sign of life
		want    string
		working bool
	}{
		{"⠋ Refactor poll loop", time.Second, "⠋ Refactor poll loop", true},
		{"⠋ Refactor poll loop", time.Minute, "Refactor poll loop", false},
		{"⠋⠙ x", time.Minute, "x", false},
		{"✳ Claude Code", time.Minute, "✳ Claude Code", false}, // not a spinner
		{" ⠋ x", time.Second, " ⠋ x", false},                   // the rule anchors at the start
		{" ⠋ x", time.Minute, "x", false},                      // … but the legacy check trims
		{"", time.Minute, "", false},
	} {
		got := liveTitle(tc.title, now.Add(-tc.life), now, stale)
		if got != tc.want || isSpinnerTitle(got) != tc.working {
			t.Errorf("liveTitle(%q, %s ago) = %q (working %v), want %q (working %v)",
				tc.title, tc.life, got, isSpinnerTitle(got), tc.want, tc.working)
		}
	}
}

// TestPollOnce_AnimatedSpinnerStaysActive — the other side: a live agent
// keeps its spinner fresh by animating the title (or repainting the
// body), however long the turn runs.
func TestPollOnce_AnimatedSpinnerStaysActive(t *testing.T) {
	shortSpinnerStaleness(t)
	s := newPollTestServer(t)
	working := readFixture(t, "claude_v2_working.txt")
	p := &fakePane{Pane: tmux.Pane{ID: "%1", Width: 120, Height: 40, Title: "⠋ Fix flaky poll test"}, body: working}
	f := newFakeTmux()
	f.addSession(tmux.Session{Name: "c-live", Path: "/tmp", Created: time.Now().Add(-time.Hour)}, p)
	f.wire(s)

	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧"}
	for i := 0; i < 8; i++ { // ~500ms: several staleness windows
		f.update(func() { p.Title = frames[i%len(frames)] + " Fix flaky poll test" })
		pollNTimes(s, 1)
		if st := s.seen["c-live"].state; st != agent.StateActive {
			t.Fatalf("tick %d: animated spinner, state = %s, want active", i, st)
		}
	}
	for i := 0; i < 8; i++ { // a static title over a repainting body
		f.update(func() { p.body = working + strings.Repeat(" ", i+1) })
		pollNTimes(s, 1)
		if st := s.seen["c-live"].state; st != agent.StateActive {
			t.Fatalf("tick %d: repainting body under a spinner, state = %s, want active", i, st)
		}
	}
}
