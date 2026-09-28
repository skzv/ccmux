package main

import (
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/tmux"
)

// TestPollOnce_CrashUnderAFrozenFrameIsAnError — Claude killed mid-turn
// leaves its last frame on screen (status line, input box, footer) and
// its launch chain prints under it: the relaunch's error, then the
// shell's prompt. With a short error the frozen box read as Claude
// waiting for input: the crash ended in needs_input with a bell, a
// "needs input" push and a prompt counted. It is a crash: error, which
// neither rings nor pushes.
func TestPollOnce_CrashUnderAFrozenFrameIsAnError(t *testing.T) {
	for _, fixture := range []string{"claude_crashed_frozen_frame.txt", "claude_crashed_frozen_frame_bare.txt"} {
		t.Run(fixture, func(t *testing.T) {
			shortSpinnerStaleness(t)
			s := newPollTestServer(t)
			pushes := countPushes(t, s)
			bells := countBells(s)
			idle, working := readFixture(t, "claude_v2_idle.txt"), readFixture(t, "claude_v2_working.txt")
			p := &fakePane{Pane: tmux.Pane{ID: "%1", Width: 120, Height: 40, Title: "✳ Claude Code"}, body: idle}
			f := newFakeTmux()
			f.addSession(tmux.Session{Name: "c-frozen", Path: "/tmp", Created: time.Now().Add(-time.Hour)}, p)
			f.wire(s)
			pollNTimes(s, 2)

			// A turn runs, then Claude is killed: its frame stays, the
			// spinner title it last set too.
			f.update(func() { p.body, p.Title = working, "⠋ Fix flaky poll test" })
			pollNTimes(s, 1)
			f.update(func() { p.Title = "⠙ Fix flaky poll test" })
			pollNTimes(s, 1)
			f.update(func() { p.body = readFixture(t, fixture) })
			pollNTimes(s, 6) // past the shortened spinner staleness

			tr := s.seen["c-frozen"]
			if tr.state != agent.StateError {
				t.Errorf("state = %s, want error (Claude crashed; its box is frozen)", tr.state)
			}
			if got := pushes(); got != 0 || *bells != 0 || tr.promptCount != 0 {
				t.Errorf("the crash notified as a prompt: pushes=%d bells=%d promptCount=%d, want 0", got, *bells, tr.promptCount)
			}
		})
	}
}
