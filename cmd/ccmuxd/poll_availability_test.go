package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/sleeplock"
	"github.com/skzv/ccmux/internal/tmux"
)

type blockedSessionWriter struct {
	header           http.Header
	entered, release chan struct{}
}

func (w *blockedSessionWriter) Header() http.Header { return w.header }
func (w *blockedSessionWriter) WriteHeader(int)     {}
func (w *blockedSessionWriter) Write(p []byte) (int, error) {
	close(w.entered)
	<-w.release
	return len(p), nil
}

func TestListSessions_ReleasesStateLockBeforeWriting(t *testing.T) {
	s := newPollTestServer(t)
	s.list = func(context.Context) ([]tmux.Session, error) { return nil, nil }
	w := &blockedSessionWriter{header: http.Header{}, entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan struct{})
	go func() { s.listSessions(w, httptest.NewRequest("GET", "/v1/sessions", nil)); close(done) }()
	defer func() { close(w.release); <-done }()
	select {
	case <-w.entered:
	case <-time.After(time.Second):
		t.Fatal("response never reached writer")
	}
	if !s.mu.TryLock() {
		t.Fatal("blocked response writer holds shared daemon state lock")
	}
	s.mu.Unlock()
}

func TestPollOnce_CaptureFailurePreservesSleepLock(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("sleep-lock subprocess platform")
	}
	bin := t.TempDir()
	for _, name := range []string{"caffeinate", "systemd-inhibit"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexec /bin/sleep 30\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	s := newPollTestServer(t)
	s.sleeper = sleeplock.NewManager(sleeplock.ModeSafe, 0)
	defer s.sleeper.Stop()
	s.sleeper.SetActive(true)
	if s.sleeper.Effective() != sleeplock.ModeSafe {
		t.Fatal("fake sleep lock did not start")
	}
	s.seen["c-busy"] = &tracked{state: agent.StateActive, agentID: agent.IDClaude, lastChange: time.Now()}
	s.list = func(context.Context) ([]tmux.Session, error) {
		return []tmux.Session{{Name: "c-busy", Agent: "claude", Path: bin}}, nil
	}
	s.capture = func(context.Context, string, int) (string, error) { return "", errors.New("temporary capture failure") }
	s.pollOnce(context.Background(), time.Second)
	if s.seen["c-busy"].state != agent.StateActive {
		t.Fatal("expected existing active state to be retained")
	}
	if mode := s.sleeper.Effective(); mode != sleeplock.ModeSafe {
		t.Fatalf("active session retained, but sleep prevention changed to %s", mode)
	}
	s.list = func(context.Context) ([]tmux.Session, error) { return nil, nil }
	s.pollOnce(context.Background(), time.Second)
	if mode := s.sleeper.Effective(); mode != sleeplock.ModeOff {
		t.Fatalf("ended session retained sleep lock: %s", mode)
	}
}
