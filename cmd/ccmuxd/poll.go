package main

import (
	"context"
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
	baseline bool
}

// freshSessionWindow is how recently a session must have been created
// (while this daemon is running) for its first observation to count as
// news. Anything older that the daemon hasn't tracked yet — sessions
// from before a restart, or renamed behind its back — is a baseline.
const freshSessionWindow = 30 * time.Second

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
	// Bound the whole tick. Every shell-out below inherits this
	// deadline, so one wedged subprocess (SIGSTOP'd tmux, frozen
	// client TTY on the bell path) costs at most one budget's worth
	// of polling instead of stalling the loop forever.
	budget := s.pollBudget
	if budget <= 0 {
		budget = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	tss, err := s.list(ctx)
	s.ensureClipboard(ctx, err == nil && len(tss) > 0)
	if err != nil {
		// Surface the failure (rate-limited — this fires every tick
		// while tmux is unreachable). Historically swallowed, which
		// made "tmux missing from launchd's PATH" look like a healthy
		// daemon with an empty dashboard and zero diagnostics.
		if time.Since(s.listErrLoggedAt) >= 30*time.Second {
			s.listErrLoggedAt = time.Now()
			log.Printf("ccmuxd: tmux list-sessions failed (polling degraded): %v", err)
		}
		return
	}
	// Keep the moshi state cache warm — it drives the tmux status-bar
	// "moshi reachable" badge in applyChrome.
	s.refreshMoshiStateCached(ctx)

	// Phase 1.
	now := time.Now()
	// tickStart anchors the Phase-3 GC: entries touched (renamed)
	// after this instant post-date the live-name snapshot below and
	// must survive this tick even though live[] doesn't know them.
	tickStart := now
	live := make(map[string]bool, len(tss))
	snaps := make([]pollSnap, 0, len(tss))
	var createdEvents []daemon.SessionEvent
	s.mu.Lock()
	for _, ts := range tss {
		live[ts.Name] = true
		t, ok := s.seen[ts.Name]
		agentID := s.sessionAgent(ts)
		if !ok {
			t = &tracked{
				created:     ts.Created,
				lastChange:  now,
				state:       agent.StateUnknown,
				agentID:     agentID,
				projectPath: ts.Path,
				// Newly-discovered session has produced no output the
				// user could have missed yet — start at reviewed.
				seen:     true,
				baseline: s.preExisting(ts, now),
			}
			if t.baseline {
				// Treat the pane as already settled so the first
				// classification is the real steady state rather than
				// "active because the content just changed from nothing".
				t.lastChange = now.Add(-idleNeeds - time.Second)
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
			baseline: t.baseline,
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
		if pane != sn.prevLast && !sn.baseline {
			lastCh = time.Now()
		}
		// ClassifyState routes through ClassifyWithTitle when the agent
		// implements TitleAwareAgent, otherwise falls back to the
		// legacy body-only Classify. So agents that don't implement
		// the new path keep their exact pre-Phase-1 behavior.
		newSt := agent.StateIdle // a plain shell has no agent state to detect
		if sn.agentID != shellAgentID {
			newSt = agent.ClassifyState(agent.ByID(sn.agentID), pane, title, lastCh, idleNeeds)
		}
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
		ts, _ := lookupTmuxSession(snaps, r.name)
		if t.baseline {
			// First look at a pre-existing session: record where it
			// stands, with no bell, push or prompt count. It keeps its
			// "reviewed" mark unless it is sitting waiting for input.
			t.baseline = false
			t.last = r.pane
			t.state = r.newState
			t.seen = ts.Attached || r.newState != agent.StateNeedsInput
			stateEvents = append(stateEvents, daemon.SessionEvent{
				At:   time.Now(),
				Kind: "state_change",
				Session: daemon.SessionState{
					Name: r.name, Host: "local", State: string(r.newState),
					Path: ts.Path,
					Seen: t.seen,
				},
			})
			if r.newState == agent.StateActive {
				anyActive = true
			}
			continue
		}
		if r.pane != t.last {
			t.last = r.pane
			t.lastChange = time.Now()
		}
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
		if r.newState == agent.StateActive {
			anyActive = true
		}
	}
	for name, t := range s.seen {
		if !live[name] && !t.touched.After(tickStart) {
			delete(s.seen, name)
			// The session ended on its own or was killed outside the
			// daemon (TUI/CLI `tmux kill`, a rename done in tmux).
			// Event-stream subscribers build their session list from
			// these events, so they need the removal too.
			stateEvents = append(stateEvents, daemon.SessionEvent{
				At:      time.Now(),
				Kind:    "killed",
				Session: daemon.SessionState{Name: name, Host: "local"},
			})
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

// ensureClipboard re-applies the tmux clipboard setup once per tmux
// server. At login ccmuxd usually starts before any tmux server exists,
// so the startup attempt fails; and a server that exits takes the
// options with it. serverUp is whether this tick found live sessions —
// no server (or an empty one) forgets the applied state so the next
// server gets the setup again.
func (s *server) ensureClipboard(ctx context.Context, serverUp bool) {
	if !serverUp {
		s.clipboardApplied = false
		return
	}
	if s.clipboardApplied || s.enableClipboard == nil {
		return
	}
	s.clipboardApplied = s.enableClipboard(ctx) == nil
}

// preExisting reports whether a session the daemon is only now seeing
// was created before it could have been watching: before this daemon
// started, or longer ago than freshSessionWindow (a rename done directly
// through tmux shows up as an "old" session under a new name).
func (s *server) preExisting(ts tmux.Session, now time.Time) bool {
	if ts.Created.IsZero() {
		return !s.startedAt.IsZero() && now.Sub(s.startedAt) < freshSessionWindow
	}
	return ts.Created.Before(s.startedAt) || now.Sub(ts.Created) > freshSessionWindow
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
//   - Bell: rings on EVERY fresh needs_input transition, attached or
//     not. Delivery self-limits: tmux.RingBell writes BEL only to the
//     clients attached to that session, so an unattached session is a
//     natural no-op. Gating on !attached here (as PR #156 did) made
//     the ring condition and the delivery set mutually exclusive —
//     the bell never reached anyone.
//   - Push suppression: a push is dispatched ONLY when the state
//     changes AND the user isn't already attached (an attached user is
//     watching; their phone doesn't need to buzz). The dashboard event
//     is still emitted so the TUI updates instantly.
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
		d.RingBell = true
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

// renameTracked moves the tracked entry oldName→newName, stamping it
// touched so a poll tick already in flight (whose Phase-1 live-set
// snapshot predates the rename) doesn't GC the entry in Phase 3 —
// which would reset promptCount, clear the seen bit, and emit a
// spurious "created" event on the next tick. Called by handleRename
// after the tmux rename succeeds.
func (s *server) renameTracked(oldName, newName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.seen[oldName]; ok {
		t.touched = time.Now()
		s.seen[newName] = t
		delete(s.seen, oldName)
	}
}

// shellAgentID marks a session tagged as a plain shell (tmux.ShellAgentTag).
// It is not a real agent: ByID would fall back to Claude, so callers
// check for it before classifying.
const shellAgentID agent.ID = tmux.ShellAgentTag

// sessionAgent resolves what runs in a session: its explicit
// @ccmux_agent tag (a resumed conversation's agent, or "shell" for a
// bare shell), else the project's .ccmux/agent sidecar.
func (s *server) sessionAgent(ts tmux.Session) agent.ID {
	if strings.TrimSpace(ts.Agent) == tmux.ShellAgentTag {
		return shellAgentID
	}
	if explicit, ok := agent.ParseID(ts.Agent); ok {
		return explicit
	}
	return s.projectAgent(ts.Path)
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
	s.sleeper.RevertStaleOverride()
	log.Printf("ccmuxd: sleep manager initialized (mode=%s, low_battery_cutoff=%d%%)",
		s.sleeper.Requested(), cutoff)
}
