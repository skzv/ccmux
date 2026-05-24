package main

import (
	"context"
	"log"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/moshi"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/sleeplock"
	"github.com/skzv/ccmux/internal/tmux"
)

// pollSnap is the per-session snapshot pollOnce takes under the lock
// before shelling out to capture-pane.
type pollSnap struct {
	ts       tmux.Session
	prevLast string
	lastCh   time.Time
	prevSt   agent.State
	agentID  agent.ID
}

// pollLoop is the heartbeat: capture-pane on each tmux session, derive
// state, and trigger bell when transitioning to NEEDS_INPUT.
func (s *server) pollLoop(ctx context.Context) {
	interval := time.Duration(s.cfg.Daemon.PollIntervalSeconds) * time.Second
	idleNeeds := time.Duration(s.cfg.Daemon.IdleSecondsForNeedsInput) * time.Second
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.pollOnce(ctx, idleNeeds)
		}
	}
}

// pollOnce runs one polling cycle. Structured in four phases so the
// lock is held only across the cheap map operations — captures and
// side effects (bell, events, push) all happen lock-released:
//
//   - Phase 1 (lock): seed missing tracked entries, snapshot per-session
//     state for the classifier.
//   - Phase 2 (no lock): shell out to capture-pane for every session
//     and classify. This is the slow part — used to run under the
//     lock and stall every IPC handler.
//   - Phase 3 (lock): fold the captures back into tracked state, decide
//     bell + events + push transitions, garbage-collect dead sessions.
//   - Phase 4 (no lock): fire bell shell-out, publish events, dispatch
//     APNs sends; update the sleep-manager.
func (s *server) pollOnce(ctx context.Context, idleNeeds time.Duration) {
	tss, err := tmux.List(ctx)
	if err != nil {
		return
	}
	// Keep the moshi state cache warm — it drives the tmux status-bar
	// "moshi reachable" badge in applyChrome.
	s.refreshMoshiStateCached(ctx)

	// Phase 1.
	now := time.Now()
	live := make(map[string]bool, len(tss))
	snaps := make([]pollSnap, 0, len(tss))
	var createdEvents []daemon.SessionEvent
	s.mu.Lock()
	for _, ts := range tss {
		live[ts.Name] = true
		t, ok := s.seen[ts.Name]
		if !ok {
			t = &tracked{
				created:     ts.Created,
				lastChange:  now,
				state:       agent.StateUnknown,
				agentID:     project.ReadAgent(ts.Path),
				projectPath: ts.Path,
			}
			s.seen[ts.Name] = t
			createdEvents = append(createdEvents, daemon.SessionEvent{
				At:   now,
				Kind: "created",
				Session: daemon.SessionState{
					Name: ts.Name, Host: "local", State: string(agent.StateUnknown),
					Path: ts.Path,
				},
			})
		}
		snaps = append(snaps, pollSnap{
			ts:       ts,
			prevLast: t.last,
			lastCh:   t.lastChange,
			prevSt:   t.state,
			agentID:  t.agentID,
		})
	}
	s.mu.Unlock()

	for _, ev := range createdEvents {
		s.events.Publish(ev)
	}

	// Phase 2.
	type result struct {
		name     string
		pane     string
		newState agent.State
	}
	results := make([]result, 0, len(snaps))
	for _, sn := range snaps {
		pane, err := s.capture(ctx, sn.ts.Name, 60)
		if err != nil {
			log.Printf("ccmuxd: capture-pane %s: %v", sn.ts.Name, err)
			continue
		}
		lastCh := sn.lastCh
		if pane != sn.prevLast {
			lastCh = time.Now()
		}
		newSt := agent.ByID(sn.agentID).Classify(pane, lastCh, idleNeeds)
		results = append(results, result{name: sn.ts.Name, pane: pane, newState: newSt})
	}

	// Phase 3.
	var (
		stateEvents []daemon.SessionEvent
		bellNames   []string
		pushes      []struct {
			name       string
			prev, next agent.State
		}
		anyActive bool
	)
	s.mu.Lock()
	for _, r := range results {
		t, ok := s.seen[r.name]
		if !ok {
			continue
		}
		if r.pane != t.last {
			t.last = r.pane
			t.lastChange = time.Now()
		}
		if r.newState == agent.StateNeedsInput && t.state != agent.StateNeedsInput {
			bellNames = append(bellNames, r.name)
			t.promptCount++
		}
		prev := t.state
		t.state = r.newState
		if r.newState != prev {
			kind := "state_change"
			if r.newState == agent.StateNeedsInput {
				kind = "needs_input"
			}
			ts, _ := lookupTmuxSession(snaps, r.name)
			stateEvents = append(stateEvents, daemon.SessionEvent{
				At:   time.Now(),
				Kind: kind,
				Session: daemon.SessionState{
					Name: r.name, Host: "local", State: string(r.newState),
					Path: ts.Path,
				},
			})
			pushes = append(pushes, struct {
				name       string
				prev, next agent.State
			}{r.name, prev, r.newState})
		}
		if r.newState == agent.StateActive {
			anyActive = true
		}
	}
	for name := range s.seen {
		if !live[name] {
			delete(s.seen, name)
		}
	}
	s.mu.Unlock()

	// Phase 4.
	if s.cfg.Notifications.Bell {
		for _, name := range bellNames {
			_ = s.bell(ctx, name)
		}
	}
	for _, ev := range stateEvents {
		s.events.Publish(ev)
	}
	for _, p := range pushes {
		s.maybePushForStateTransition(p.name, p.prev, p.next)
	}
	s.sleeper.SetActive(anyActive)
}

// lookupTmuxSession returns the snapshotted tmux.Session for `name`
// from the Phase 1 snaps, so Phase 3 can attach ts.Path to events
// without re-locking or re-shelling out.
func lookupTmuxSession(snaps []pollSnap, name string) (tmux.Session, bool) {
	for _, sn := range snaps {
		if sn.ts.Name == name {
			return sn.ts, true
		}
	}
	return tmux.Session{}, false
}

// refreshMoshiStateCached keeps the moshi.Status cache warm for the
// tmux status-bar badge. Cached for 60s so we don't shell out to
// moshi-hook every 2-second poll tick.
func (s *server) refreshMoshiStateCached(ctx context.Context) {
	s.moshiMu.Lock()
	defer s.moshiMu.Unlock()
	if time.Since(s.moshiCheckAt) > 60*time.Second {
		s.moshiState = moshi.Detect(ctx)
		s.moshiCheckAt = time.Now()
	}
}

// startSleepManager constructs the sleeplock.Manager from config. The
// backward-compat shim: if Mode is empty AND the legacy
// DangerousKeepAwakeOnBattery flag is true, we treat that as
// Mode="dangerous". The legacy flag is otherwise honored only as the
// "off" interpretation for safe.
func (s *server) startSleepManager() {
	modeStr := s.cfg.Sleep.Mode
	if modeStr == "" && s.cfg.Sleep.DangerousKeepAwakeOnBattery {
		modeStr = "dangerous"
	}
	cutoff := s.cfg.Sleep.LowBatteryCutoff
	if cutoff <= 0 {
		cutoff = 20
	}
	s.sleeper = sleeplock.NewManager(sleeplock.ParseMode(modeStr), cutoff)
	log.Printf("ccmuxd: sleep manager initialized (mode=%s, low_battery_cutoff=%d%%)",
		s.sleeper.Requested(), cutoff)
}
