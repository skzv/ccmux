package main

import (
	"context"
	"fmt"
	"log"
	"strings"
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
		agentID := s.projectAgent(ts.Path)
		if !ok {
			t = &tracked{
				created:     ts.Created,
				lastChange:  now,
				state:       agent.StateUnknown,
				agentID:     agentID,
				projectPath: ts.Path,
				// Newly-discovered session has produced no output the
				// user could have missed yet — start at reviewed.
				seen: true,
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
		} else {
			t.agentID = agentID
			t.projectPath = ts.Path
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
		// Read the OSC-set pane title alongside the body. tmux.PaneTitle
		// swallows session-gone errors as "" so it never aborts a poll
		// tick — body classification still runs the same.
		title := ""
		if s.paneTitle != nil {
			title, _ = s.paneTitle(ctx, sn.ts.Name)
		}
		lastCh := sn.lastCh
		if pane != sn.prevLast {
			lastCh = time.Now()
		}
		// ClassifyState routes through ClassifyWithTitle when the agent
		// implements TitleAwareAgent, otherwise falls back to the
		// legacy body-only Classify. So agents that don't implement
		// the new path keep their exact pre-Phase-1 behavior.
		newSt := agent.ClassifyState(agent.ByID(sn.agentID), pane, title, lastCh, idleNeeds)
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
		// tgAlerts/tgResolved drive the Telegram bridge: alert when a
		// session starts waiting unattended, resolve when it stops
		// waiting (state moved on, or the user attached). Captured here
		// under the lock so Phase 4 can dispatch without holding it.
		tgAlerts   []tgAlert
		tgResolved []string
		anyActive  bool
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
		ts, _ := lookupTmuxSession(snaps, r.name)
		decision := decideAttention(t.state, r.newState, t.seen, ts.Attached)
		t.seen = decision.NewSeen
		if decision.IncPromptCount {
			t.promptCount++
		}
		if decision.RingBell {
			bellNames = append(bellNames, r.name)
		}
		prev := t.state
		t.state = r.newState
		if decision.EmitStateEvent {
			stateEvents = append(stateEvents, daemon.SessionEvent{
				At:   time.Now(),
				Kind: decision.StateEventKind,
				Session: daemon.SessionState{
					Name: r.name, Host: "local", State: string(r.newState),
					Path: ts.Path,
					Seen: t.seen,
				},
			})
		}
		if decision.SendPush {
			pushes = append(pushes, struct {
				name       string
				prev, next agent.State
			}{r.name, prev, r.newState})
		}
		// Telegram bridge signals. Alert on an unattended needs_input
		// transition (same condition as the bell); resolve when a
		// previously-waiting session stops waiting or gets attended.
		if s.tgBridge != nil {
			alert, resolve := telegramSignals(prev, r.newState, ts.Attached)
			if alert {
				tgAlerts = append(tgAlerts, tgAlert{
					name:     r.name,
					pane:     r.pane,
					changeID: fmt.Sprintf("%s#%d", r.name, t.promptCount),
				})
			}
			if resolve {
				tgResolved = append(tgResolved, r.name)
			}
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
	s.dispatchTelegram(ctx, tgAlerts, tgResolved)
	s.sleeper.SetActive(anyActive)
}

// tgAlert is one queued Telegram needs-input notification.
type tgAlert struct {
	name     string
	pane     string
	changeID string
}

// telegramSignals decides, for one session's state transition, whether
// to send a Telegram alert and/or resolve an outstanding one. Pure so
// the poll-loop hook condition is unit-testable without a bridge:
//
//   - alert: the session just entered needs_input while unattended
//     (the same "ring the bell" condition — an attended session doesn't
//     need a phone alert).
//   - resolve: a previously-waiting session is no longer waiting
//     unattended (state moved on, or someone attached), so any
//     outstanding alert should be marked handled.
func telegramSignals(prev, next agent.State, attached bool) (alert, resolve bool) {
	alert = next == agent.StateNeedsInput && prev != agent.StateNeedsInput && !attached
	resolve = prev == agent.StateNeedsInput && (next != agent.StateNeedsInput || attached)
	return alert, resolve
}

// dispatchTelegram fires queued Telegram alerts/resolutions off the poll
// loop's critical path. Each call is its own goroutine so a slow Bot API
// round trip can't stall polling; the bridge's alert store is
// concurrency-safe. No-op when the bridge is disabled.
func (s *server) dispatchTelegram(ctx context.Context, alerts []tgAlert, resolved []string) {
	if s.tgBridge == nil {
		return
	}
	cap := s.cfg.Telegram.EffectivePaneTailLines()
	for _, a := range alerts {
		tail := lastLines(a.pane, cap)
		go s.tgBridge.Notify(ctx, "local", a.name, tail, a.changeID)
	}
	for _, name := range resolved {
		go s.tgBridge.MarkSeen(ctx, "local", name)
	}
}

// lastLines returns the final n lines of s (the pane tail shipped to
// Telegram), trimming trailing blank lines first.
func lastLines(s string, n int) string {
	if n <= 0 {
		return s
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

// attentionDecision is the per-session outcome of one poll tick: the
// new seen bit, whether to ring the bell / send a push / emit the
// state-change event, and the event kind to use. Pulled out as a
// pure function so the lifecycle is unit-testable end-to-end without
// standing up a tmux server (the surrounding pollOnce is integration-
// tagged).
type attentionDecision struct {
	NewSeen        bool
	RingBell       bool
	SendPush       bool
	IncPromptCount bool
	EmitStateEvent bool
	StateEventKind string // "state_change" or "needs_input"
}

// decideAttention computes the per-session decision for one poll
// tick. Encodes the Phase 2 rules:
//
//   - Seen bit: an attached user is by definition watching → seen=true.
//     A state change while NOT attached produces output the user
//     should review → seen=false. Otherwise the previous seen value
//     is preserved.
//   - Bell/push suppression: the bell rings and a push is dispatched
//     ONLY when the state transitions to needs_input AND the user
//     isn't already attached. The dashboard event is still emitted
//     so the TUI updates instantly.
//   - PromptCount: incremented on every fresh needs_input transition
//     (attached or not — it's a lifetime count, not a "did we notify"
//     count). Drives the usage/quota panel.
func decideAttention(prev, next agent.State, prevSeen, attached bool) attentionDecision {
	d := attentionDecision{NewSeen: prevSeen}
	if attached {
		d.NewSeen = true
	}
	if next == agent.StateNeedsInput && prev != agent.StateNeedsInput {
		d.IncPromptCount = true
		d.RingBell = !attached
	}
	if next != prev {
		d.EmitStateEvent = true
		d.StateEventKind = "state_change"
		if next == agent.StateNeedsInput {
			d.StateEventKind = "needs_input"
		}
		if !attached {
			d.NewSeen = false
			d.SendPush = true
		}
	}
	return d
}

func (s *server) projectAgent(projectPath string) agent.ID {
	if s.readAgent != nil {
		return s.readAgent(projectPath)
	}
	return project.ReadAgent(projectPath)
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
