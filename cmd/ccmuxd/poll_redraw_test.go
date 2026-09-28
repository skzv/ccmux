package main

import (
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/tmux"
)

// reflow is roughly what a pane looks like after a resize: tmux
// re-wraps it and the agent repaints its full-width rules at the new
// width.
func reflow(body string, from, to int) string {
	return strings.ReplaceAll(body, strings.Repeat("─", from), strings.Repeat("─", to))
}

// waitingSession sets up a Claude session in a fake tmux that worked
// (spinner title) and then went back to its input box — one turn,
// announced with one bell.
func waitingSession(t *testing.T, s *server, attached bool) (*fakeTmux, *fakePane, *int) {
	t.Helper()
	bells := countBells(s)
	working, idle := readFixture(t, "claude_v2_working.txt"), readFixture(t, "claude_v2_idle.txt")
	p := &fakePane{Pane: tmux.Pane{ID: "%1", Width: 120, Height: 40, Title: "⠋ Fix flaky poll test"}, body: working}
	f := newFakeTmux()
	f.addSession(tmux.Session{Name: "c-wait", Path: "/tmp", Attached: attached, Created: time.Now().Add(-time.Hour)}, p)
	f.wire(s)
	pollNTimes(s, 2)
	f.update(func() { p.body, p.Title = idle, "✳ Fix flaky poll test" })
	pollNTimes(s, 3)
	if tr := s.seen["c-wait"]; tr.state != agent.StateNeedsInput || *bells != 1 || tr.promptCount != 1 {
		t.Fatalf("setup: turn end: state=%s bells=%d promptCount=%d, want needs_input/1/1", tr.state, *bells, tr.promptCount)
	}
	return f, p, bells
}

// TestPollOnce_ResizeReflowDoesNotRenotify — a session waiting for
// input gets its pane resized: a client of another size attaches, or
// `tmux resize-window` on a detached session. The reflow changed the
// captured body, which read as activity: "active", then a few seconds
// later "needs input" again — a second bell and push, promptCount++ and
// the session flagged unreviewed for a prompt already announced. Here
// the agent's own repaint lands a tick after tmux's reflow, as it does
// when the capture races SIGWINCH.
func TestPollOnce_ResizeReflowDoesNotRenotify(t *testing.T) {
	s := newPollTestServer(t)
	f, p, bells := waitingSession(t, s, false)
	idle := p.body
	ch := s.events.Subscribe()
	defer s.events.Unsubscribe(ch)

	f.update(func() { p.Width, p.Height, p.body = 80, 24, reflow(idle, 120, 81) })
	pollNTimes(s, 1)
	f.update(func() { p.body = reflow(idle, 120, 80) }) // the agent's repaint
	pollNTimes(s, 4)

	tr := s.seen["c-wait"]
	if tr.state != agent.StateNeedsInput {
		t.Errorf("state = %s after a resize, want needs_input", tr.state)
	}
	if *bells != 1 || tr.promptCount != 1 {
		t.Errorf("resize re-notified: bells=%d promptCount=%d, want 1/1", *bells, tr.promptCount)
	}
	if evs := publishedEvents(ch); len(evs) != 0 {
		t.Errorf("resize published %+v, want no state change", evs)
	}
}

// TestPaneMemoryTrack pins which ticks count as redraws.
func TestPaneMemoryTrack(t *testing.T) {
	var m paneMemory
	steps := []struct {
		obs    observation
		redraw bool
	}{
		{observation{paneID: "%1", width: 80, height: 24}, false},  // first look
		{observation{paneID: "%1", width: 80, height: 24}, false},  // unchanged
		{observation{paneID: "%1", width: 100, height: 30}, true},  // resized
		{observation{paneID: "%1", width: 100, height: 30}, true},  // grace tick: the agent's repaint
		{observation{paneID: "%1", width: 100, height: 30}, false}, // settled
		{observation{paneID: "%2", width: 100, height: 30}, true},  // another pane read
		{observation{paneID: "%2", width: 100, height: 30}, true},  // grace
		{observation{paneID: "%2", width: 100, height: 30}, false}, // settled
		{observation{}, true}, // fell back to the active pane
	}
	for i, st := range steps {
		if got := m.track(st.obs); got != st.redraw {
			t.Errorf("step %d (%+v): redraw = %v, want %v", i, st.obs, got, st.redraw)
		}
	}
}
