package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/tmux"
)

// aliasingSession is a Codex session the daemon knows to show a spinner
// title while it works (its @ccmux_spinner mark: only the spinner and the
// body's working rules count as work then, not the session merely
// classifying as active), sitting at its caret under the spinner frame
// it last showed — a title that never moves while it waits. Settled:
// joined and past the spinner's stale window.
func aliasingSession(t *testing.T, s *server, name string) (*fakeTmux, *fakePane) {
	t.Helper()
	p := &fakePane{Pane: tmux.Pane{ID: "%1", Width: 120, Height: 40, Title: "⠋ codex"}, body: codexPane("")}
	f := newFakeTmux()
	f.addSession(tmux.Session{Name: name, Path: "/tmp", Agent: "codex", Spinner: "codex", Created: time.Now().Add(-time.Hour)}, p)
	f.wire(s)
	pollNTimes(s, 5)
	tr := s.seen[name]
	if tr.state != agent.StateIdle || tr.joined || !tr.spinnerSeen {
		t.Fatalf("setup: state=%s joined=%v spinnerSeen=%v, want idle, settled, spinner known", tr.state, tr.joined, tr.spinnerSeen)
	}
	return f, p
}

// TestPollOnce_SpinnerInStepWithThePollIsWork — an agent whose spinner
// frames come round once per poll interval shows the same glyph on
// every tick. The daemon took that for a frozen title (a dead agent's
// leftover) and the turn — whose spinner was its only sign of work —
// ended without a notification. Read again a moment later within the
// tick, the glyph has moved: the spinner is live, the turn is work, and
// its end notifies once.
func TestPollOnce_SpinnerInStepWithThePollIsWork(t *testing.T) {
	shortSpinnerStaleness(t)
	s := newPollTestServer(t)
	pushes := countPushes(t, s)
	f, p := aliasingSession(t, s, "c-alias")

	for _, body := range []string{codexPane("> fix it\n\n• Working"), codexPane("> fix it\n\n• Working.")} {
		// Each tick catches the same frame; the frame has moved on by the
		// time the title is read again.
		f.update(func() { p.body, p.Title, p.titleAfterRead = body, "⠋ codex", "⠙ codex" })
		pollNTimes(s, 1)
	}
	if st := s.seen["c-alias"].state; st != agent.StateActive {
		t.Errorf("mid-turn: state = %s, want active (a live spinner)", st)
	}
	f.update(func() { p.body, p.Title, p.titleAfterRead = codexPane("> fix it\n\n• Done."), "⠋ codex", "" })
	pollNTimes(s, 6)
	tr := s.seen["c-alias"]
	if tr.state != agent.StateIdle {
		t.Fatalf("after the turn: state = %s, want idle at the caret", tr.state)
	}
	if got := pushes(); got != 1 {
		t.Errorf("a turn shown only by a spinner in step with the poll: pushes=%d, want 1", got)
	}
}

// TestPollOnce_FrozenSpinnerTitleStillGoesStale — the re-read must not
// bring a dead title back to life: one that reads the same a moment
// later stays stale, so typing under it is no turn and the pane doesn't
// show active.
func TestPollOnce_FrozenSpinnerTitleStillGoesStale(t *testing.T) {
	shortSpinnerStaleness(t)
	s := newPollTestServer(t)
	pushes := countPushes(t, s)
	bells := countBells(s)
	f, p := aliasingSession(t, s, "c-frozen")
	before := f.listCalls("c-frozen")

	for _, typed := range []string{"fix", "fix the test"} {
		f.update(func() { p.body = codexPane("") + typed })
		pollNTimes(s, 1) // typing: the body changed, the title didn't
		pollNTimes(s, 3)
	}
	if got := f.listCalls("c-frozen") - before; got <= 8 {
		t.Errorf("list-panes calls over 8 ticks = %d, want the title re-read on the ticks the body changed", got)
	}
	if got := pushes(); got != 0 || *bells != 0 {
		t.Errorf("typing under a frozen spinner notified: pushes=%d bells=%d", got, *bells)
	}
	if tr := s.seen["c-frozen"]; !tr.pane.titleChange.IsZero() {
		t.Errorf("titleChange = %v, want none: the title never moved", tr.pane.titleChange)
	}
}

// TestPollOnce_TitleRechecksOnlyWhenDueAndCapped — a second read costs a
// list-panes call and a wait, so only panes that need one get it, at
// most maxTitleRechecks a tick, with one wait for all of them.
func TestPollOnce_TitleRechecksOnlyWhenDueAndCapped(t *testing.T) {
	shortSpinnerStaleness(t)
	s := newPollTestServer(t)
	s.titleRecheckDelay = 100 * time.Millisecond
	f := newFakeTmux()
	const n = maxTitleRechecks + 4
	panes := make([]*fakePane, n)
	for i := range panes {
		panes[i] = &fakePane{Pane: tmux.Pane{ID: fmt.Sprintf("%%%d", i+1), Width: 80, Height: 24, Title: "⠋ codex"}, body: codexPane("")}
		f.addSession(tmux.Session{Name: fmt.Sprintf("c-%02d", i), Path: "/tmp", Agent: "codex", Spinner: "codex", Created: time.Now().Add(-time.Hour)}, panes[i])
	}
	moving := &fakePane{Pane: tmux.Pane{ID: "%99", Width: 80, Height: 24, Title: "⠋ codex"}, body: codexPane("")}
	f.addSession(tmux.Session{Name: "c-moving", Path: "/tmp", Agent: "codex", Spinner: "codex", Created: time.Now().Add(-time.Hour)}, moving)
	f.wire(s)
	pollNTimes(s, 5)
	calls := func() (total int) {
		for i := range panes {
			total += f.listCalls(fmt.Sprintf("c-%02d", i))
		}
		return total
	}

	// A quiet tick: nothing changed anywhere, nothing is re-read.
	before := calls()
	start := time.Now()
	s.pollOnce(context.Background(), 50*time.Millisecond)
	if d, got := time.Since(start), calls()-before; got != n || d >= s.titleRecheckDelay {
		t.Errorf("quiet tick: %d list-panes calls in %v, want %d (one each, no wait)", got, d, n)
	}

	// Every frozen pane's body changes, and one pane's title moves on its
	// own: that one isn't due (its title changed), the rest are, capped.
	f.update(func() {
		for _, p := range panes {
			p.body += "x"
		}
		moving.Title = "⠙ codex"
	})
	before = calls()
	movingBefore := f.listCalls("c-moving")
	start = time.Now()
	s.pollOnce(context.Background(), 50*time.Millisecond)
	d := time.Since(start)
	if got := calls() - before; got != n+maxTitleRechecks {
		t.Errorf("list-panes calls = %d, want %d reads and %d re-reads", got, n, maxTitleRechecks)
	}
	if got := f.listCalls("c-moving") - movingBefore; got != 1 {
		t.Errorf("a title that changed this tick was re-read: %d calls, want 1", got)
	}
	if d < s.titleRecheckDelay || d >= 2*s.titleRecheckDelay {
		t.Errorf("tick took %v, want one %v wait for all re-reads", d, s.titleRecheckDelay)
	}
}
