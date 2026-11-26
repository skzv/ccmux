package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tmux"
)

func TestHandleMute_UpdatesTrackedStateAndPersists(t *testing.T) {
	cfg := config.Defaults()
	s := newServer(cfg)
	var calledName string
	var calledMuted bool
	s.setMuted = func(_ context.Context, name string, muted bool) error {
		calledName = name
		calledMuted = muted
		return nil
	}
	s.seen["c-api"] = &tracked{
		state:   agent.StateActive,
		agentID: agent.IDClaude,
		seen:    true,
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/c-api/mute", strings.NewReader(`{"muted":true}`))
	rec := httptest.NewRecorder()
	s.handleSessionsItem(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	if calledName != "c-api" || !calledMuted {
		t.Fatalf("setMuted called with (%q, %v), want (c-api, true)", calledName, calledMuted)
	}
	var got daemon.SessionState
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !got.Muted {
		t.Errorf("response Muted = false, want true")
	}
	if !s.seen["c-api"].muted {
		t.Errorf("tracked.muted = false, want true")
	}
}

func TestPollOnce_MutedSessionSuppressesBell(t *testing.T) {
	cfg := config.Defaults()
	cfg.Notifications.Bell = true
	s := newServer(cfg)
	s.enableClipboard = nil
	s.procs = nil
	s.panes = nil
	s.capturePane = nil
	s.markSpinner = nil
	s.markReview = nil
	s.readAgent = func(string) agent.ID { return agent.IDClaude }

	bellCount := 0
	s.bell = func(_ context.Context, _ string) error {
		bellCount++
		return nil
	}

	created := time.Now().Add(-10 * time.Minute)
	pane := "working..."
	muted := true
	s.list = func(context.Context) ([]tmux.Session, error) {
		return []tmux.Session{{Name: "c-muted", Path: "/tmp/p", Created: created, Muted: muted}}, nil
	}
	s.capture = func(context.Context, string, int) (string, error) { return pane, nil }
	s.paneTitle = func(context.Context, string) (string, error) { return "", nil }

	// Tick 1: baseline active turn.
	s.pollOnce(context.Background(), time.Second)
	// Force session into an active, worked state so transition to needs_input would ring bell.
	s.mu.Lock()
	s.seen["c-muted"].baseline = false
	s.seen["c-muted"].joined = false
	s.seen["c-muted"].worked = true
	s.seen["c-muted"].state = agent.StateActive
	s.seen["c-muted"].lastChange = time.Now().Add(-5 * time.Minute)
	s.mu.Unlock()

	// Tick 2: prompt frame settled -> needs_input, but muted so bell must stay 0.
	pane = "done\n╭────────────╮\n│ >          │\n╰────────────╯"
	s.pollOnce(context.Background(), time.Second)
	if bellCount != 0 {
		t.Fatalf("muted session rang bell %d times, want 0", bellCount)
	}
	s.mu.Lock()
	st := s.seen["c-muted"].sessionState("c-muted")
	s.mu.Unlock()
	if !st.Muted {
		t.Errorf("sessionState.Muted = false, want true")
	}
}
