package main

import (
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/tmux"
)

// codexTurn runs one Codex turn on p: its spinner title animating over
// its working transcript, then its answer at the caret.
func codexTurn(s *server, f *fakeTmux, p *fakePane, prompt string) {
	for _, frame := range []string{"⠋", "⠙"} {
		f.update(func() { p.body, p.Title = codexPane("> "+prompt+"\n\n• Working"), frame+" codex" })
		pollNTimes(s, 1)
	}
	f.update(func() { p.body, p.Title = codexPane("> "+prompt+"\n\n• Done."), "codex" })
	pollNTimes(s, 3)
}

// typeAndPause types into a Codex session's caret and pauses.
func typeAndPause(s *server, f *fakeTmux, p *fakePane, text string) {
	f.update(func() { p.body += text })
	pollNTimes(s, 1)
	pollNTimes(s, 3)
}

// TestPollOnce_SpinnerSeenSurvivesRestart — once the daemon has seen an
// agent's spinner title in a session, the session merely classifying
// as active (the user typing) is no longer a turn. That was kept in the
// daemon's memory only: after every restart, typing into each Codex
// session notified once ("finished") until the daemon saw its spinner
// again. The session records it (@ccmux_spinner), once, and a restarted
// daemon reads it back.
func TestPollOnce_SpinnerSeenSurvivesRestart(t *testing.T) {
	s := newPollTestServer(t)
	p := &fakePane{Pane: tmux.Pane{ID: "%1", Width: 120, Height: 40, Title: "codex"}, body: codexPane("")}
	f := newFakeTmux()
	f.addSession(tmux.Session{Name: "c-codex", Path: "/tmp", Agent: "codex", Created: time.Now().Add(-time.Hour)}, p)
	f.wire(s)
	pollNTimes(s, 2)
	if marks := f.spinnerMarks(); len(marks) != 0 {
		t.Fatalf("marked before any spinner: %v", marks)
	}
	codexTurn(s, f, p, "fix it")
	codexTurn(s, f, p, "and the test")
	if marks := f.spinnerMarks(); len(marks) != 1 || marks[0] != "c-codex=codex" {
		t.Fatalf("spinner marks = %v, want [c-codex=codex], written once", marks)
	}

	// The daemon restarts.
	s2 := newPollTestServer(t)
	s2.startedAt = time.Now()
	pushes := countPushes(t, s2)
	bells := countBells(s2)
	f.wire(s2)
	pollNTimes(s2, 2)
	tr := s2.seen["c-codex"]
	if !tr.spinnerSeen {
		t.Fatal("restarted daemon: spinnerSeen = false, want it read back from the session")
	}
	typeAndPause(s2, f, p, "and the docs")
	if got := pushes(); got != 0 || *bells != 0 {
		t.Errorf("typing after a restart notified: pushes=%d bells=%d, want 0", got, *bells)
	}
	codexTurn(s2, f, p, "and the docs")
	if got := pushes(); got != 1 {
		t.Errorf("a real turn after the restart: pushes=%d, want 1", got)
	}
	if marks := f.spinnerMarks(); len(marks) != 1 {
		t.Errorf("the restarted daemon wrote the mark again: %v", marks)
	}
}

// TestPollOnce_SpinnerMarkIsPerAgent — the mark names the agent that
// spun, so another agent in the same session (a shell session's
// foreground changing, a session re-tagged) doesn't inherit it.
func TestPollOnce_SpinnerMarkIsPerAgent(t *testing.T) {
	s := newPollTestServer(t)
	s.startedAt = time.Now()
	p := &fakePane{Pane: tmux.Pane{ID: "%1", Width: 120, Height: 40, Title: "cursor"}, body: "Cursor Agent\n\n> "}
	f := newFakeTmux()
	f.addSession(tmux.Session{Name: "c-other", Path: "/tmp", Agent: "cursor", Spinner: "codex", Created: time.Now().Add(-time.Hour)}, p)
	f.wire(s)
	pollNTimes(s, 1)
	if s.seen["c-other"].spinnerSeen {
		t.Error("a cursor session took codex's spinner mark for its own")
	}
}

// TestPollOnce_MarkedAgentFoundWaitingIsTrusted — joining a session until
// it is seen to settle costs the turns started right after a restart:
// the daemon can't tell them from one already running. Not so for an
// agent the session records as showing a spinner title while it works:
// found without one, it isn't working, so a turn it starts on the very
// next tick notifies — once. Found with one, it is joined as before.
func TestPollOnce_MarkedAgentFoundWaitingIsTrusted(t *testing.T) {
	for _, tc := range []struct {
		name   string
		title  string
		body   string
		pushes int
	}{
		{"waiting", "codex", codexPane("> fix it\n\n• Done."), 1},
		{"spinner on the first look", "⠋ codex", codexPane("> fix it\n\n• Working"), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newPollTestServer(t)
			s.startedAt = time.Now()
			pushes := countPushes(t, s)
			p := &fakePane{Pane: tmux.Pane{ID: "%1", Width: 120, Height: 40, Title: tc.title}, body: tc.body}
			f := newFakeTmux()
			f.addSession(tmux.Session{Name: "c-codex", Path: "/tmp", Agent: "codex", Spinner: "codex", Created: time.Now().Add(-time.Hour)}, p)
			f.wire(s)
			pollNTimes(s, 1)
			if st := s.seen["c-codex"].state; st != agent.StateIdle {
				t.Fatalf("first look: state = %s, want idle (at its caret)", st)
			}
			codexTurn(s, f, p, "and the test") // starts on the next tick
			if got := pushes(); got != tc.pushes {
				t.Errorf("pushes = %d, want %d", got, tc.pushes)
			}
		})
	}
}
