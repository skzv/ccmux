package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tmux"
)

// countPushes registers one (FCM) phone with s and returns a function
// that reports how many pushes have been sent to it so far.
func countPushes(t *testing.T, s *server) func() int {
	t.Helper()
	store, err := daemon.OpenDeviceStore(filepath.Join(t.TempDir(), "devices.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterWithProvider("ssh-ed25519 AAAAphone", "fcm-token", daemon.ProviderFCM, ""); err != nil {
		t.Fatal(err)
	}
	fc := &fakeFCM{}
	s.devices, s.apnsSender, s.fcmSender = store, &fakeAPNs{}, fc
	s.apnsSlots, s.fcmSlots = make(chan struct{}, 4), make(chan struct{}, 4)
	return func() int {
		// Sends run on the worker pool; wait for it to drain.
		for deadline := time.Now().Add(time.Second); len(s.fcmSlots) > 0 && time.Now().Before(deadline); {
			time.Sleep(5 * time.Millisecond)
		}
		fc.mu.Lock()
		defer fc.mu.Unlock()
		return len(fc.sends)
	}
}

// typeInto is the input box with text typed into it.
func typeInto(idle, text string) string {
	return strings.Replace(idle, "❯", "❯ "+text, 1)
}

// TestPollOnce_TypingIntoInputBoxDoesNotRenotify — the user attached to
// a waiting session types into the input box and pauses: the body
// changed, so the session read as active and, a few seconds later,
// needs input again — a bell and promptCount++ for every pause. Once a
// session has shown a working spinner, only the spinner is the agent
// working; submitting the prompt is what makes the next prompt news.
func TestPollOnce_TypingIntoInputBoxDoesNotRenotify(t *testing.T) {
	s := newPollTestServer(t)
	f, p, bells := waitingSession(t, s, true)
	idle, working := p.body, readFixture(t, "claude_v2_working.txt")

	for _, typed := range []string{"fix the", "fix the flaky poll test"} {
		f.update(func() { p.body = typeInto(idle, typed) })
		pollNTimes(s, 1) // typing: the body changed
		pollNTimes(s, 3) // the pause
	}
	tr := s.seen["c-wait"]
	if tr.state != agent.StateNeedsInput {
		t.Fatalf("state = %s after typing and pausing, want needs_input", tr.state)
	}
	if *bells != 1 || tr.promptCount != 1 {
		t.Errorf("typing re-notified: bells=%d promptCount=%d, want 1/1", *bells, tr.promptCount)
	}

	// Submitting starts a real turn; its end is news.
	f.update(func() { p.body, p.Title = working, "⠙ Fix flaky poll test" })
	pollNTimes(s, 2)
	f.update(func() { p.body, p.Title = idle, "✳ Fix flaky poll test" })
	pollNTimes(s, 3)
	if *bells != 2 || tr.promptCount != 2 {
		t.Errorf("a real turn after typing: bells=%d promptCount=%d, want 2/2", *bells, tr.promptCount)
	}
}

// TestPollOnce_DetachedRedrawDoesNotRenotify — a detached session the
// user already reviewed repaints part of its screen while it waits (a
// statusline clock, a redraw after `tmux resize-window` on another
// client's window…). That is not a turn: no push, no bell, and it stays
// reviewed.
func TestPollOnce_DetachedRedrawDoesNotRenotify(t *testing.T) {
	s := newPollTestServer(t)
	pushes := countPushes(t, s)
	f, p, bells := waitingSession(t, s, false)
	if got := pushes(); got != 1 {
		t.Fatalf("setup: turn end pushed %d times, want 1", got)
	}
	ts := tmux.Session{Name: "c-wait", Path: "/tmp", Created: time.Now().Add(-time.Hour)}
	ts.Attached = true // the user looks at it …
	f.addSession(ts, p)
	pollNTimes(s, 1)
	ts.Attached = false // … and leaves
	f.addSession(ts, p)
	pollNTimes(s, 1)

	idle := p.body
	for _, clock := range []string{"12:01", "12:02"} {
		f.update(func() { p.body = idle + "\n  " + clock })
		pollNTimes(s, 4)
	}
	tr := s.seen["c-wait"]
	if got := pushes(); got != 1 || *bells != 1 || tr.promptCount != 1 {
		t.Errorf("redraws re-notified: pushes=%d bells=%d promptCount=%d, want 1/1/1", got, *bells, tr.promptCount)
	}
	if !tr.seen {
		t.Error("a redraw marked the reviewed session unreviewed")
	}
}

// TestPollOnce_NewSessionStartupDoesNotNotify — a brand-new session's
// first classification is "active" (its content changed from nothing),
// so the next edge counted as a transition: a new Claude reaching its
// empty input box pushed "needs input", and a session whose startup
// output went quiet pushed "finished" — seconds after the user created
// it. Startup is not a turn; the first real one still notifies.
func TestPollOnce_NewSessionStartupDoesNotNotify(t *testing.T) {
	s := newPollTestServer(t)
	s.startedAt = time.Now().Add(-time.Hour) // created while the daemon runs
	pushes := countPushes(t, s)
	bells := countBells(s)
	idle, working := readFixture(t, "claude_v2_idle.txt"), readFixture(t, "claude_v2_working.txt")
	banner := strings.SplitN(idle, "────", 2)[0] // the banner before the input box is drawn
	claude := &fakePane{Pane: tmux.Pane{ID: "%1", Width: 120, Height: 40, Title: "✳ Claude Code"}, body: banner}
	logs := &fakePane{Pane: tmux.Pane{ID: "%2", Width: 120, Height: 40}, body: "starting worker…"}
	f := newFakeTmux()
	f.addSession(tmux.Session{Name: "c-new", Path: "/tmp", Created: time.Now()}, claude)
	f.addSession(tmux.Session{Name: "c-logs", Path: "/tmp", Created: time.Now()}, logs)
	f.wire(s)

	pollNTimes(s, 1)
	f.update(func() { claude.body = idle; logs.body += "\nworker ready" })
	pollNTimes(s, 4)

	for _, name := range []string{"c-new", "c-logs"} {
		tr := s.seen[name]
		if tr.state == agent.StateActive || tr.state == agent.StateUnknown {
			t.Fatalf("setup: %s still %s after startup", name, tr.state)
		}
		if !tr.seen || tr.promptCount != 0 {
			t.Errorf("%s: startup settle marked seen=%v promptCount=%d, want reviewed and 0", name, tr.seen, tr.promptCount)
		}
	}
	if got := pushes(); got != 0 || *bells != 0 {
		t.Errorf("startup notified: pushes=%d bells=%d, want 0", got, *bells)
	}

	// The first real turn notifies.
	f.update(func() { claude.body, claude.Title = working, "⠋ Fix flaky poll test" })
	pollNTimes(s, 2)
	f.update(func() { claude.body, claude.Title = idle, "✳ Fix flaky poll test" })
	pollNTimes(s, 3)
	if got := pushes(); got != 1 || *bells != 1 || s.seen["c-new"].promptCount != 1 {
		t.Errorf("first turn: pushes=%d bells=%d promptCount=%d, want 1/1/1", got, *bells, s.seen["c-new"].promptCount)
	}
}
