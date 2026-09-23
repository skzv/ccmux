package main

import (
	"context"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/tmux"
)

// pollNTimes runs pollOnce n times with a short idle threshold, sleeping
// past it between ticks so idle-gated classifiers settle.
func pollNTimes(s *server, n int) {
	for i := 0; i < n; i++ {
		s.pollOnce(context.Background(), 50*time.Millisecond)
		time.Sleep(60 * time.Millisecond)
	}
}

const promptPane = "done.\n╭──────────╮\n│ >        │\n╰──────────╯"

// TestPollOnce_RestartDoesNotRenotify — after a daemon restart every
// session is new to the daemon; seeing it must not ring the bell, send
// a push, bump promptCount or mark it unreviewed-by-transition.
func TestPollOnce_RestartDoesNotRenotify(t *testing.T) {
	s := newPollTestServer(t)
	s.cfg.Notifications.Bell = true
	s.startedAt = time.Now()
	bells := 0
	s.bell = func(context.Context, string) error { bells++; return nil }
	s.list = func(context.Context) ([]tmux.Session, error) {
		return []tmux.Session{{Name: "c-proj", Path: "/tmp", Created: time.Now().Add(-time.Hour)}}, nil
	}
	s.capture = func(context.Context, string, int) (string, error) { return promptPane, nil }

	pollNTimes(s, 4)

	tr := s.seen["c-proj"]
	if bells != 0 || tr.promptCount != 0 {
		t.Errorf("restart re-notified: bells=%d promptCount=%d", bells, tr.promptCount)
	}
	if tr.state != agent.StateNeedsInput {
		t.Errorf("state = %s, want needs_input recorded as the baseline", tr.state)
	}
}

// TestPollOnce_DirectTmuxRenameDoesNotRenotify — a rename done straight
// through tmux (TUI/CLI) shows up as a new name for an old session.
func TestPollOnce_DirectTmuxRenameDoesNotRenotify(t *testing.T) {
	s := newPollTestServer(t)
	s.cfg.Notifications.Bell = true
	s.startedAt = time.Now().Add(-time.Hour)
	bells := 0
	s.bell = func(context.Context, string) error { bells++; return nil }
	name := "c-old"
	created := time.Now().Add(-2 * time.Hour)
	s.list = func(context.Context) ([]tmux.Session, error) {
		return []tmux.Session{{Name: name, Path: "/tmp", Created: created}}, nil
	}
	s.capture = func(context.Context, string, int) (string, error) { return promptPane, nil }
	pollNTimes(s, 3)
	before := bells

	name = "c-renamed"
	pollNTimes(s, 3)
	if bells != before {
		t.Errorf("rename re-rang the bell: before=%d after=%d", before, bells)
	}
}

// TestPollOnce_FreshSessionStillNotifies — a session created while the
// daemon runs is real news: reaching needs_input must still ring.
func TestPollOnce_FreshSessionStillNotifies(t *testing.T) {
	s := newPollTestServer(t)
	s.cfg.Notifications.Bell = true
	s.startedAt = time.Now().Add(-time.Hour)
	bells := 0
	s.bell = func(context.Context, string) error { bells++; return nil }
	s.list = func(context.Context) ([]tmux.Session, error) {
		return []tmux.Session{{Name: "c-new", Path: "/tmp", Created: time.Now()}}, nil
	}
	pane := "working..."
	s.capture = func(context.Context, string, int) (string, error) { return pane, nil }
	pollNTimes(s, 2)
	pane = promptPane
	pollNTimes(s, 3)
	if bells != 1 {
		t.Errorf("fresh session reaching needs_input: bells=%d, want 1", bells)
	}
}
