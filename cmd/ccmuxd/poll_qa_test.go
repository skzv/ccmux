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
	agentP := &fakePane{Pane: tmux.Pane{ID: "%3", Width: 120, Height: 40, Title: "⠋ Fix flaky poll test"}, body: working}
	shellP := &fakePane{Pane: tmux.Pane{ID: "%4", Window: 1, Width: 120, Height: 40, Active: true, Title: "host.local"}, body: "user@host ~ % "}
	f := newFakeTmux()
	f.addSession(tmux.Session{Name: "c-multi", Path: "/tmp", Created: time.Now().Add(-time.Hour)}, agentP, shellP)
	f.wire(s)

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
// spinner nothing is animating any more must stop counting as work.
func TestPollOnce_StaleSpinnerTitleDoesNotPinActive(t *testing.T) {
	shortSpinnerStaleness(t)
	s := newPollTestServer(t)
	f := newFakeTmux()
	f.addSession(tmux.Session{Name: "c-crash", Path: "/tmp", Created: time.Now()},
		&fakePane{Pane: tmux.Pane{ID: "%1", Width: 80, Height: 24, Title: "⠋ Refactor poll loop"}, body: crashedPane})
	f.wire(s)

	pollNTimes(s, 1)
	if st := s.seen["c-crash"].state; st != agent.StateActive {
		t.Fatalf("first look with a fresh spinner: state = %s, want active", st)
	}
	pollNTimes(s, 5) // 300ms with nothing changing: well past 150ms
	if st := s.seen["c-crash"].state; st != agent.StateError {
		t.Errorf("stale spinner over a shell prompt: state = %s, want error (not active, which holds the sleep lock)", st)
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
