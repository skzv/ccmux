package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/moshi"
	"github.com/skzv/ccmux/internal/tmux"
)

// TestStartBackground_StopWaitsForPollTick — shutdown used to call
// sleeper.Stop straight away (the deferred cancel ran after it) and
// never waited for the poll loop, so a tick still in flight finished
// with SetActive(true) after Stop and re-engaged the sleep lock — in
// very_dangerous mode, re-running `pmset disablesleep 1` with no daemon
// left to revert it. stop must cancel the loops and wait for the tick
// before it returns.
func TestStartBackground_StopWaitsForPollTick(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // the sleep-override marker lives next to the socket
	s := newPollTestServer(t)
	s.cfg.Sleep.Mode = "off" // never spawn a real caffeinate
	s.cfg.Daemon.PollIntervalSeconds = 1
	s.pollBudget = 30 * time.Second // the tick must end on cancellation, not its budget
	s.enableClipboard = func(context.Context) error { return nil }
	s.list = func(context.Context) ([]tmux.Session, error) {
		return []tmux.Session{{Name: "c-a", Path: "/tmp"}}, nil
	}
	entered := make(chan struct{})
	var once sync.Once
	var finished atomic.Bool
	s.capture = func(ctx context.Context, _ string, _ int) (string, error) {
		once.Do(func() { close(entered) })
		<-ctx.Done()
		time.Sleep(100 * time.Millisecond) // the tick's tail: events, pushes, SetActive
		finished.Store(true)
		return "", ctx.Err()
	}

	stop := startBackground(s, context.Background())
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		stop()
		t.Fatal("poll loop never ran a tick")
	}
	stop()
	if !finished.Load() {
		t.Error("stop returned while a poll tick was still running — its SetActive can land after sleeper.Stop")
	}
}

// TestPollLoop_NonPositiveIntervalsUseDefaults — a negative (or zero)
// poll_interval_seconds reached time.NewTicker, which panics, so the
// daemon crash-looped under launchd.
func TestPollLoop_NonPositiveIntervalsUseDefaults(t *testing.T) {
	s := newPollTestServer(t)
	s.cfg.Daemon.PollIntervalSeconds = -1
	s.cfg.Daemon.IdleSecondsForNeedsInput = -5
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.pollLoop(ctx) // must return, not panic

	d := s.cfg.Daemon
	applyDaemonDefaults(&d)
	if d.PollIntervalSeconds != 2 || d.IdleSecondsForNeedsInput != 3 {
		t.Errorf("defaults = %d/%d, want 2/3", d.PollIntervalSeconds, d.IdleSecondsForNeedsInput)
	}
}

// TestPollOnce_LeavesMoshiDetectionAlone — Moshi detection runs
// moshi-hook and `brew services list` with multi-second timeouts. Inside
// the poll tick it could use up the tick's 10s budget so every capture
// failed; it now has its own loop.
func TestPollOnce_LeavesMoshiDetectionAlone(t *testing.T) {
	s := newPollTestServer(t)
	s.detectMoshi = func(context.Context) moshi.Status {
		t.Error("pollOnce ran Moshi detection")
		return moshi.Status{}
	}
	s.list = func(context.Context) ([]tmux.Session, error) {
		return []tmux.Session{{Name: "c-a", Path: "/tmp"}}, nil
	}
	s.capture = func(context.Context, string, int) (string, error) { return "", nil }
	s.pollOnce(context.Background(), time.Second)
}

// TestMoshiLoop_DetectionDoesNotHoldLock — detection used to run under
// moshiMu, so a create-session handler (applyChrome reads the cached
// state under that lock) waited out every slow moshi-hook call.
func TestMoshiLoop_DetectionDoesNotHoldLock(t *testing.T) {
	s := newPollTestServer(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var once, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	s.detectMoshi = func(context.Context) moshi.Status {
		once.Do(func() { close(entered) })
		<-release
		return moshi.Status{Paired: true, HooksInstalled: true, ServiceRunning: true}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.moshiLoop(ctx)
		close(done)
	}()
	defer func() {
		unblock()
		cancel()
		<-done
	}()

	<-entered
	locked := make(chan struct{})
	go func() {
		// What applyChrome does: read the cached state under moshiMu.
		s.moshiMu.Lock()
		_ = s.moshiState.Paired
		s.moshiMu.Unlock()
		close(locked)
	}()
	select {
	case <-locked:
	case <-time.After(2 * time.Second):
		t.Fatal("moshiMu held while detection runs — applyChrome would block on it")
	}
	unblock()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.moshiMu.Lock()
		paired := s.moshiState.Paired
		s.moshiMu.Unlock()
		if paired {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("moshiLoop never stored the detected state")
}
