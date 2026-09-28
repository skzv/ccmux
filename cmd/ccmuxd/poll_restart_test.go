package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/tmux"
)

// restartTurn is a turn as a test drives it through a fake pane: its
// frames, one per poll tick, and the pane it ends on.
type restartTurn struct {
	frames []func(p *fakePane)
	end    func(p *fakePane)
}

// run plays the turn, then lets the pane sit through a pause.
func (tn restartTurn) run(s *server, f *fakeTmux, p *fakePane) {
	for _, frame := range tn.frames {
		f.update(func() { frame(p) })
		pollNTimes(s, 1)
	}
	f.update(func() { tn.end(p) })
	pollNTimes(s, 4)
}

// TestPollOnce_TurnInFlightAtRestartIsNotAnnounced — QA restarted the
// daemon a few seconds into 30-second turns. Unless Claude showed both
// its spinner title and its `esc to interrupt` status line, the first
// look classified the session as settled (it backdates the pane's last
// change, and doesn't believe a spinner title it hasn't seen move), so
// the daemon stopped treating it as joined, took the rest of the turn
// for a new one, and announced its end: a bell and "needs input" push,
// or a "finished" push. A session the daemon joined isn't announced
// until it has been seen to settle after the first look; the turn after
// that is, once.
func TestPollOnce_TurnInFlightAtRestartIsNotAnnounced(t *testing.T) {
	idle, working := readFixture(t, "claude_v2_idle.txt"), readFixture(t, "claude_v2_working.txt")
	statusLine := func(secs int) func(p *fakePane) {
		return func(p *fakePane) {
			p.body = strings.Replace(working, "(12s ·", fmt.Sprintf("(%ds ·", secs), 1)
		}
	}
	output := func(prompt string, steps int) func(p *fakePane) {
		return func(p *fakePane) { p.body = answered(idle, prompt, strings.Repeat("Step done. ", steps)) }
	}
	spinner := func(frame string, body string) func(p *fakePane) {
		return func(p *fakePane) { p.body, p.Title = body, frame+" codex" }
	}
	for _, tc := range []struct {
		name  string
		tag   string
		title string
		// joined is the turn in flight when the daemon restarts (its first
		// frame is what the first look sees); next is one the daemon
		// watches start, after that.
		joined, next restartTurn
		firstLook    agent.State
		// bells is what next rings: a turn ending at Codex's caret is
		// "finished" (idle), which pushes but doesn't ring.
		bells int
	}{
		{
			name: "claude, status line only", tag: "claude", title: "host.local",
			joined: restartTurn{
				frames: []func(*fakePane){statusLine(5), statusLine(6), statusLine(7), statusLine(8), statusLine(9)},
				end:    func(p *fakePane) { p.body = idle },
			},
			next: restartTurn{
				frames: []func(*fakePane){statusLine(1), statusLine(2)},
				end:    func(p *fakePane) { p.body = idle },
			},
			// The status line over a live box says a turn is running.
			firstLook: agent.StateActive,
			bells:     1,
		},
		{
			name: "claude, output only", tag: "claude", title: "host.local",
			joined: restartTurn{
				frames: []func(*fakePane){output("fix it", 1), output("fix it", 2), output("fix it", 3), output("fix it", 4)},
				end:    output("fix it", 5),
			},
			next: restartTurn{
				frames: []func(*fakePane){output("and the test", 1)},
				end:    output("and the test", 2),
			},
			// Nothing on screen says it's working: one capture can't tell.
			firstLook: agent.StateNeedsInput,
			bells:     1,
		},
		{
			name: "codex, spinner title only", tag: "codex", title: "⠋ codex",
			joined: restartTurn{
				frames: []func(*fakePane){
					spinner("⠙", codexPane("> fix it\n\n• Working")), spinner("⠹", codexPane("> fix it\n\n• Working")),
					spinner("⠸", codexPane("> fix it\n\n• Working")), spinner("⠼", codexPane("> fix it\n\n• Working")),
				},
				end: func(p *fakePane) { p.body, p.Title = codexPane("> fix it\n\n• Fixed."), "codex" },
			},
			next: restartTurn{
				frames: []func(*fakePane){spinner("⠋", codexPane("> and the test\n\n• Working")), spinner("⠙", codexPane("> and the test\n\n• Working"))},
				end:    func(p *fakePane) { p.body, p.Title = codexPane("> and the test\n\n• Fixed."), "codex" },
			},
			// A spinner title found on a first look isn't believed (a
			// crashed agent leaves one behind): quiet at its caret.
			firstLook: agent.StateIdle,
			bells:     0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newPollTestServer(t)
			s.startedAt = time.Now()
			pushes := countPushes(t, s)
			bells := countBells(s)
			p := &fakePane{Pane: tmux.Pane{ID: "%1", Width: 120, Height: 40, Title: tc.title}}
			tc.joined.frames[0](p)
			f := newFakeTmux()
			f.addSession(tmux.Session{Name: "c-busy", Path: "/tmp", Agent: tc.tag, Created: time.Now().Add(-time.Hour)}, p)
			f.wire(s)

			pollNTimes(s, 1)
			tr := s.seen["c-busy"]
			if tr.state != tc.firstLook {
				t.Errorf("first look: state = %s, want %s", tr.state, tc.firstLook)
			}
			if tr.state == agent.StateActive && !tr.seen {
				t.Error("first look at a session working: marked unreviewed")
			}
			tc.joined.frames = tc.joined.frames[1:]
			tc.joined.run(s, f, p)
			if !settled(tr.state) {
				t.Fatalf("the turn in flight at the restart ended in %s, want a settled state", tr.state)
			}
			if got := pushes(); got != 0 || *bells != 0 || tr.promptCount != 0 {
				t.Errorf("the turn in flight at the restart notified: pushes=%d bells=%d promptCount=%d, want 0", got, *bells, tr.promptCount)
			}

			tc.next.run(s, f, p)
			if got := pushes(); got != 1 || *bells != tc.bells || tr.promptCount != tc.bells {
				t.Errorf("the next turn: pushes=%d bells=%d promptCount=%d, want 1/%d/%d", got, *bells, tr.promptCount, tc.bells, tc.bells)
			}
		})
	}
}

// TestTurn_JoinedEndsOnlyOnAStillSettle — a joined session stays joined
// through settled ticks until the daemon has seen its pane still
// (evidence.still): the turn it was caught in ends unannounced, and the
// next one notifies.
func TestTurn_JoinedEndsOnlyOnAStillSettle(t *testing.T) {
	spin := evidence{spinning: true}
	bells, pushes := runEvidence(&turn{joined: true}, false, []evTick{
		{agent.StateNeedsInput, evidence{}},                  // the first look read it as waiting
		{agent.StateActive, spin}, {agent.StateActive, spin}, // it was in a turn
		{agent.StateNeedsInput, evidence{}},                  // which ends
		{agent.StateNeedsInput, evidence{still: true}},       // and is seen to settle
		{agent.StateActive, spin}, {agent.StateActive, spin}, // the next turn
		{agent.StateNeedsInput, evidence{still: true}},
	})
	if bells != 1 || pushes != 1 {
		t.Errorf("bells=%d pushes=%d, want 1/1 (the next turn only)", bells, pushes)
	}
}

// TestPollOnce_RestartWhileWaitingStaysQuiet — the other side of joining
// a session until it is seen to settle: one found waiting at its input
// box shows needs_input from the first look (unreviewed: nobody has seen
// that prompt through this daemon), and nothing it does while the daemon
// watches it sit there — typing into the box included — notifies.
func TestPollOnce_RestartWhileWaitingStaysQuiet(t *testing.T) {
	s := newPollTestServer(t)
	s.startedAt = time.Now()
	pushes := countPushes(t, s)
	bells := countBells(s)
	idle := readFixture(t, "claude_v2_idle.txt")
	p := &fakePane{Pane: tmux.Pane{ID: "%1", Width: 120, Height: 40, Title: "✳ Claude Code"}, body: idle}
	f := newFakeTmux()
	f.addSession(tmux.Session{Name: "c-wait", Path: "/tmp", Created: time.Now().Add(-time.Hour)}, p)
	f.wire(s)

	pollNTimes(s, 1)
	tr := s.seen["c-wait"]
	if tr.state != agent.StateNeedsInput || tr.seen {
		t.Fatalf("first look: state=%s seen=%v, want needs_input, unreviewed", tr.state, tr.seen)
	}
	for _, text := range []string{"fix", "fix the flaky poll test"} {
		f.update(func() { p.body = typeInto(idle, text) })
		pollNTimes(s, 1)
	}
	pollNTimes(s, 4)
	if got := pushes(); got != 0 || *bells != 0 || tr.promptCount != 0 || tr.state != agent.StateNeedsInput {
		t.Errorf("waiting at restart: state=%s pushes=%d bells=%d promptCount=%d, want needs_input, 0/0/0", tr.state, got, *bells, tr.promptCount)
	}
}
