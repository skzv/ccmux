package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/sleeplock"
	"github.com/skzv/ccmux/internal/tmux"
)

// newPollTestServer builds a server whose poll seams are all injectable
// and whose side-effect surfaces (sleep manager, moshi cache, bell) are
// inert, so pollOnce can run hermetically without tmux, caffeinate, or
// moshi-hook. list/capture are left nil — each test injects its own.
func newPollTestServer(t *testing.T) *server {
	t.Helper()
	s := &server{
		cfg:     config.Config{},
		seen:    map[string]*tracked{},
		events:  daemon.NewEventBus(),
		sleeper: sleeplock.NewManager(sleeplock.ModeOff, 20),
		readAgent: func(string) agent.ID {
			return agent.IDClaude
		},
		bell: func(context.Context, string) error { return nil },
	}
	// Pre-warm the moshi cache timestamp so refreshMoshiStateCached
	// never shells out during the test.
	s.moshiCheckAt = time.Now()
	return s
}

// TestPollOnce_BudgetBoundsBlockedCapture — finding: no deadline on any
// poll-loop shell-out, so one wedged subprocess (a SIGSTOP'd tmux)
// stalls polling forever. pollOnce now wraps the tick in a budget;
// a capture that blocks until its context dies must not hang the tick.
// Before the fix this test times out (capture blocks on the root
// context, which is only cancelled at daemon shutdown).
func TestPollOnce_BudgetBoundsBlockedCapture(t *testing.T) {
	s := newPollTestServer(t)
	s.pollBudget = 150 * time.Millisecond
	s.list = func(ctx context.Context) ([]tmux.Session, error) {
		return []tmux.Session{{Name: "c-stuck", Path: "/tmp"}}, nil
	}
	s.capture = func(ctx context.Context, name string, lines int) (string, error) {
		<-ctx.Done() // a SIGSTOP'd tmux: cmd.Output() never returns on its own
		return "", ctx.Err()
	}

	done := make(chan struct{})
	go func() {
		s.pollOnce(context.Background(), time.Second)
		close(done)
	}()
	select {
	case <-done:
		// returned within the budget — the loop can tick again
	case <-time.After(5 * time.Second):
		t.Fatal("pollOnce did not return within its budget — a blocked capture wedged the poll loop")
	}
}

// TestPollOnce_RenameDuringTickPreservesTracked — finding 5a: a rename
// landing between a tick's Phase-1 live-set snapshot and its Phase-3
// GC saw live[new]==false and deleted the renamed session's tracked
// state (promptCount reset, seen bit cleared, spurious "created" event
// on the next tick). The rename is injected mid-Phase-2 via the
// capture seam — exactly where the real race window sits.
func TestPollOnce_RenameDuringTickPreservesTracked(t *testing.T) {
	s := newPollTestServer(t)
	s.seen["old"] = &tracked{
		promptCount: 3,
		seen:        false,
		state:       agent.StateActive,
		lastChange:  time.Now(),
	}
	s.list = func(ctx context.Context) ([]tmux.Session, error) {
		return []tmux.Session{{Name: "old", Path: "/tmp"}}, nil
	}
	s.capture = func(ctx context.Context, name string, lines int) (string, error) {
		// The rename handler runs while Phase 2 is capturing.
		s.renameTracked("old", "new")
		return "pane content", nil
	}

	s.pollOnce(context.Background(), time.Second)

	got, ok := s.seen["new"]
	if !ok {
		t.Fatal("renamed session's tracked state was GC'd by the racing poll tick")
	}
	if got.promptCount != 3 {
		t.Errorf("promptCount = %d, want 3 (must survive the rename)", got.promptCount)
	}
	if got.seen {
		t.Error("seen bit flipped to true — the unreviewed flag must survive the rename")
	}
	if _, still := s.seen["old"]; still {
		t.Error("old name still tracked after rename")
	}
}

// TestPollOnce_GCStillCollectsDeadSessions — the touched-guard must not
// break normal GC: a session that disappears (and was not renamed this
// tick) is still collected.
func TestPollOnce_GCStillCollectsDeadSessions(t *testing.T) {
	s := newPollTestServer(t)
	s.seen["gone"] = &tracked{state: agent.StateIdle, lastChange: time.Now().Add(-time.Hour)}
	s.list = func(ctx context.Context) ([]tmux.Session, error) { return nil, nil }
	s.capture = func(ctx context.Context, name string, lines int) (string, error) { return "", nil }

	s.pollOnce(context.Background(), time.Second)

	if _, still := s.seen["gone"]; still {
		t.Error("dead session survived GC — the touched-guard is too broad")
	}
}

// TestPollOnce_LogsListFailureRateLimited — finding 5c: tmux.List
// failures were silently swallowed, so "tmux missing from launchd's
// PATH" looked like a healthy daemon with an empty dashboard. pollOnce
// must log the failure, and rate-limit it (one line per 30s, not one
// per 2s tick).
func TestPollOnce_LogsListFailureRateLimited(t *testing.T) {
	s := newPollTestServer(t)
	s.list = func(ctx context.Context) ([]tmux.Session, error) {
		return nil, errors.New(`exec: "tmux": executable file not found in $PATH`)
	}

	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	s.pollOnce(context.Background(), time.Second)
	if !strings.Contains(buf.String(), "list-sessions failed") {
		t.Fatalf("pollOnce swallowed the tmux.List failure; log output: %q", buf.String())
	}
	first := strings.Count(buf.String(), "list-sessions failed")

	s.pollOnce(context.Background(), time.Second) // immediately again
	if got := strings.Count(buf.String(), "list-sessions failed"); got != first {
		t.Errorf("second immediate failure logged again (%d lines) — want rate-limited (%d)", got, first)
	}
}
