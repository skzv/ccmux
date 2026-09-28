package main

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/daemon"
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
	follow   bool
	baseline bool
	pane     paneMemory
	// attached is whether a client was attached as of the previous tick.
	attached bool
}

// pollTrack is the poll loop's per-session memory beyond the wire
// state, embedded in tracked.
type pollTrack struct {
	pane paneMemory
	turn
	// project is the session's project (sessionProject) as of the path
	// projectOf; projectKnown once it has been worked out.
	project      string
	projectOf    string
	projectKnown bool
	// agentTagged: tracked.agentID came from the session's @ccmux_agent
	// tag rather than being resolved (see noteSessionLocked).
	agentTagged bool
	// follow: the session has no agent of its own (a shell, or one the
	// user made outside ccmux), so tracked.agentID is whatever agent runs
	// in its foreground this tick — shellAgentID when none does. See
	// sessionAgent.
	follow bool
	// listed is the session's list-sessions row as of the last tick
	// (attached, windows, created — for sessionState).
	listed tmux.Session
}

// paneMemory is what the poll loop remembers between ticks about the
// pane it reads for a session. Phase 2 updates a copy (no lock held)
// and Phase 3 stores it back.
type paneMemory struct {
	// id is the tmux id of the pane read last tick — the pane the agent
	// runs in, resolved on first sight and kept while it exists (see
	// agentPane) — or "" when the read fell back to the active pane.
	id       string
	observed bool // a tick has read the session at all
	// width and height are the pane's size last tick (0 when unknown),
	// and redrawTicks how many more ticks a body change still counts
	// as a redraw after a resize or pane switch (see track).
	width, height int
	redrawTicks   int
	// title is the pane's OSC title last tick, and titleChange when the
	// daemon last saw it change between two reads (zero until then): the
	// evidence that a working-spinner title belongs to a live agent (see
	// liveTitle).
	title       string
	titleChange time.Time
	// output is the tail of what the agent printed above its input area
	// last tick, and hasInput whether that input area was on screen (see
	// agent.TurnView) — for agents that keep the two apart.
	output   string
	hasInput bool
}

// redrawGraceTicks is how many ticks after a resize or pane switch a
// body change is still taken for a redraw. The agent repaints on
// SIGWINCH a moment after tmux reflows the pane, so the capture that
// sees the new size can precede the repaint that the next one sees.
const redrawGraceTicks = 1

// track folds one tick's pane identity and size into m and reports
// whether a body change this tick is a redraw rather than the agent's
// own output: the pane was resized (a client of another size attached,
// `tmux resize-window`) or a different pane was read — this tick or
// within redrawGraceTicks before it.
//
// A redraw is not activity. Counted as a change, a resize reflowing a
// session that sat waiting for input made it "active" and, a few
// seconds later, "needs input" again: a second bell and push, another
// prompt counted and the session marked unreviewed, for a prompt the
// user had already seen.
func (m *paneMemory) track(obs observation) (redraw bool) {
	switched := m.observed && obs.paneID != m.id
	resized := m.width > 0 && obs.width > 0 && (obs.width != m.width || obs.height != m.height)
	switch {
	case switched || resized:
		redraw, m.redrawTicks = true, redrawGraceTicks
	case m.redrawTicks > 0:
		redraw = true
		m.redrawTicks--
	}
	m.id, m.observed = obs.paneID, true
	if obs.width > 0 && obs.height > 0 {
		m.width, m.height = obs.width, obs.height
	}
	return redraw
}

// captureLines is how much scrollback each poll tick reads above the
// visible screen.
const captureLines = 60

// freshSessionWindow is how recently a session must have been created
// (while this daemon is running) for its first observation to count as
// news. Anything older that the daemon hasn't tracked yet — sessions
// from before a restart, or renamed behind its back — is a baseline.
const freshSessionWindow = 30 * time.Second

// pollLoop is the heartbeat: capture-pane on each tmux session, derive
// state, and trigger bell when transitioning to NEEDS_INPUT.
func (s *server) pollLoop(ctx context.Context) {
	d := s.cfg.Daemon
	applyDaemonDefaults(&d)
	interval := time.Duration(d.PollIntervalSeconds) * time.Second
	idleNeeds := time.Duration(d.IdleSecondsForNeedsInput) * time.Second
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
//   - Phase 2 (no lock): shell out to list each session's panes and
//     capture the agent's (observe), and classify. This is the slow
//     part — used to run under the lock and stall every IPC handler.
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

	// listedAt anchors everything this tick decides from the session
	// list: a kill or rename stamped after it may not be reflected in
	// tss (Phase 1 skips the old name), and an entry renamed after it
	// must survive Phase 3's GC even though tss doesn't list it.
	listedAt := time.Now()
	tss, err := s.list(ctx)
	if errors.Is(ctx.Err(), context.Canceled) {
		return // shutting down
	}
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

	// Phase 1.
	now := time.Now()
	live := make(map[string]bool, len(tss))
	snaps := make([]pollSnap, 0, len(tss))
	var createdEvents []daemon.SessionEvent
	s.mu.Lock()
	s.pruneTombstonesLocked(now)
	for _, ts := range tss {
		if s.gone[ts.Name].After(listedAt) {
			// Killed or renamed away through the daemon after this
			// tick's list ran: tss is stale for this name. Tracking it
			// again brought the old name back as a ghost, announced
			// with a "created" event.
			continue
		}
		live[ts.Name] = true
		t, ok := s.seen[ts.Name]
		wasAttached := ok && t.listed.Attached
		if !ok {
			t = &tracked{
				lastChange: now,
				state:      agent.StateUnknown,
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
			} else {
				// Created while we watched: its startup isn't a turn.
				t.startup = true
			}
			s.seen[ts.Name] = t
		}
		s.noteSessionLocked(t, ts)
		if !ok {
			createdEvents = append(createdEvents, daemon.SessionEvent{
				At:      now,
				Kind:    "created",
				Session: t.sessionState(ts.Name),
			})
		}
		snaps = append(snaps, pollSnap{
			ts:       ts,
			prevLast: t.last,
			lastCh:   t.lastChange,
			prevSt:   t.state,
			agentID:  t.agentID,
			follow:   t.follow,
			baseline: t.baseline,
			pane:     t.pane,
			attached: wasAttached,
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
		lastCh   time.Time // when the body last changed, redraws aside
		mem      paneMemory
		newState agent.State
		ev       evidence
		agentID  agent.ID // what the session was classified as
		switched bool     // a different agent (or none) runs in the foreground now
	}
	results := make([]result, 0, len(snaps))
	staleAfter := spinnerStaleAfter(idleNeeds)
	for _, sn := range snaps {
		mem := sn.pane
		obs, err := s.observe(ctx, sn.ts.Name, mem.id)
		if err != nil {
			if errors.Is(ctx.Err(), context.Canceled) {
				// The daemon is shutting down (the tick's own budget ends
				// in DeadlineExceeded): every capture left fails the same
				// way, so there is nothing to log, and nothing to decide.
				return
			}
			log.Printf("ccmuxd: capture-pane %s: %v", sn.ts.Name, err)
			continue
		}
		now := time.Now()
		first := !mem.observed
		redraw := mem.track(obs)
		pane := obs.body
		lastCh := sn.lastCh
		if pane != sn.prevLast && !sn.baseline && !redraw {
			lastCh = now
		}
		// A title counts as changed only between two of our own reads:
		// the one found on a first look may be a dead agent's leftover.
		if !first && obs.title != mem.title {
			mem.titleChange = now
		}
		mem.title = obs.title
		id := sn.agentID
		if sn.follow {
			id = shellAgentID
			if fg, ok := agent.InForeground(obs.command, pane, obs.title); ok {
				id = fg
			}
		}
		switched := id != sn.agentID && !first
		if switched {
			mem.output, mem.hasInput = "", false // nothing to compare the new agent's output with
		}
		newSt := agent.StateIdle // a plain shell has no agent state to detect
		var ev evidence
		if id != shellAgentID {
			a := agent.ByID(id)
			view := agent.ReadTurn(a, pane)
			life := mem.titleChange
			if view.Busy {
				life = now
			}
			title := liveTitle(obs.title, life, now, staleAfter)
			// ClassifyStateFrom routes through ClassifyWithTitle when the
			// agent implements TitleAwareAgent, otherwise falls back to
			// the legacy body-only Classify. It also takes the previous
			// state, which a skip_state_update rule (a transient overlay)
			// keeps instead of reclassifying.
			newSt = agent.ClassifyStateFrom(a, sn.prevSt, pane, title, lastCh, idleNeeds)
			ev = evidence{spinning: isSpinnerTitle(title), busy: view.Busy, separated: view.Separated}
			if view.Separated {
				// New output above the input area since last tick. Not on
				// a first look (nothing to compare with), a redraw (a
				// resize re-wraps the output), or when either tick had no
				// input area to measure from (the agent starting up in a
				// shell, or gone from one).
				ev.output = !first && !redraw && view.HasInput && mem.hasInput && view.Output != mem.output
				mem.output, mem.hasInput = view.Output, view.HasInput
			}
		}
		results = append(results, result{name: sn.ts.Name, pane: pane, lastCh: lastCh, mem: mem, newState: newSt, ev: ev, agentID: id, switched: switched})
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
		sn, _ := lookupSnap(snaps, r.name)
		ts := sn.ts
		t.pane = r.mem
		t.agentID = r.agentID
		if t.baseline {
			// First look at a pre-existing session: record where it
			// stands, with no bell, push or prompt count. It keeps its
			// "reviewed" mark unless it is sitting waiting for input.
			t.baseline = false
			t.last = r.pane
			t.state = r.newState
			t.seen = ts.Attached || r.newState != agent.StateNeedsInput
			// Whatever it is in the middle of — a turn, a startup — is not
			// news: until it first settles, nothing it does notifies.
			t.spinnerSeen = r.ev.spinning
			t.joined = !settled(r.newState)
			stateEvents = append(stateEvents, daemon.SessionEvent{
				At:      time.Now(),
				Kind:    "state_change",
				Session: t.sessionState(r.name),
			})
			if r.newState == agent.StateActive {
				anyActive = true
			}
			continue
		}
		t.last = r.pane
		t.lastChange = r.lastCh
		if r.switched {
			// An agent started in the session's foreground, or exited
			// from it. Its startup is not a turn, and nothing the previous
			// one did carries over.
			t.turn = turn{startup: true}
		}
		prevSeen := t.seen
		decision := t.attend(t.state, r.newState, r.ev, t.seen, ts.Attached)
		t.seen = decision.NewSeen
		if decision.RingBell {
			bellNames = append(bellNames, r.name)
		}
		prev := t.state
		t.state = r.newState
		if decision.IncPromptCount {
			t.promptCount++
		}
		// Besides a state change: the agent in the foreground changed, or
		// a client attached or detached (which also marks the session
		// reviewed) — fields event-stream clients show, which they'd
		// otherwise only see on the next state change.
		if decision.EmitStateEvent || r.switched || ts.Attached != sn.attached || t.seen != prevSeen {
			kind := decision.StateEventKind
			if kind == "" {
				kind = "state_change"
			}
			stateEvents = append(stateEvents, daemon.SessionEvent{
				At:      time.Now(),
				Kind:    kind,
				Session: t.sessionState(r.name),
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
		if !live[name] && !t.touched.After(listedAt) {
			delete(s.seen, name)
			// The session ended on its own or was killed outside the
			// daemon (TUI/CLI `tmux kill`, a rename done in tmux).
			// Event-stream subscribers build their session list from
			// these events, so they need the removal too.
			stateEvents = append(stateEvents, daemon.SessionEvent{
				At:      time.Now(),
				Kind:    "killed",
				Session: t.sessionState(name), // as last seen
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

// observation is one tick's reading of a session's agent pane.
type observation struct {
	body   string
	title  string // the pane's OSC-set title (#{pane_title})
	paneID string // "" when read through the session's active pane
	// command is the pane's foreground process name
	// (#{pane_current_command}; see tmux.Pane.Command), "" when read
	// through the session's active pane.
	command string
	// width and height are the pane's size; 0 when read through the
	// active pane.
	width, height int
}

// observe reads the pane a session's agent runs in: its body and the
// OSC title the agent sets (a second, higher-quality signal — a
// braille spinner while working).
//
// A bare session target means its *active* pane, the active pane of
// the active window. With the agent in window 0 and the user in a
// shell window opened next to it, the daemon read the shell: the
// session showed as a crashed agent (error) and the agent's turns
// ended unnoticed. So the agent pane is resolved from the session's
// pane list (agentPane) and read by id. When that fails — or the seams
// aren't wired — the active pane is read as before.
func (s *server) observe(ctx context.Context, name, paneID string) (observation, error) {
	if s.panes != nil && s.capturePane != nil {
		if panes, err := s.panes(ctx, name); err == nil {
			if p, ok := agentPane(panes, paneID); ok {
				if body, err := s.capturePane(ctx, p.ID, captureLines); err == nil {
					return observation{body: body, title: p.Title, paneID: p.ID, width: p.Width, height: p.Height, command: p.Command}, nil
				}
			}
		}
	}
	body, err := s.capture(ctx, name, captureLines)
	if err != nil {
		return observation{}, err
	}
	// tmux.PaneTitle swallows session-gone errors as "" so it never
	// aborts a poll tick — body classification still runs the same.
	title := ""
	if s.paneTitle != nil {
		title, _ = s.paneTitle(ctx, name)
	}
	return observation{body: body, title: title}, nil
}

// agentPane picks the pane the session's agent runs in: the one read
// last tick while it still exists, else the session's oldest pane —
// the pane it was created with, which for a session ccmux started is
// the agent's, whatever windows and splits the user added since
// (tmux.OldestPane). Sticking to the resolved pane keeps a later
// `split-window -b` or window swap from moving the daemon's eye.
func agentPane(panes []tmux.Pane, last string) (tmux.Pane, bool) {
	if last != "" {
		for _, p := range panes {
			if p.ID == last {
				return p, true
			}
		}
	}
	return tmux.OldestPane(panes)
}

// agentPaneID resolves the pane a request meant for a session's agent
// must reach (/send-keys, /preview): the pane the poll loop classifies
// — the one it read last tick while that pane is still in the session,
// else the one agentPane picks from the session's panes (a session no
// tick has read yet). "" when it can't be resolved — the panes seam is
// unset or list-panes failed, usually because there is no such session
// — for the caller to fall back to the session target.
//
// The pane list is read even when the poll loop remembers an id: pane
// ids are unique only while the tmux server lives, so a remembered id
// is used only once the session's own list confirms it.
func (s *server) agentPaneID(ctx context.Context, name string) string {
	if s.panes == nil {
		return ""
	}
	var last string
	s.mu.Lock()
	if t := s.seen[name]; t != nil {
		last = t.pane.id
	}
	s.mu.Unlock()
	panes, err := s.panes(ctx, name)
	if err != nil {
		return ""
	}
	p, ok := agentPane(panes, last)
	if !ok {
		return ""
	}
	return p.ID
}

// spinnerStaleFloor is the shortest time a working-spinner title is
// believed without a sign of life; a variable so tests can shorten it.
var spinnerStaleFloor = 10 * time.Second

// spinnerStaleAfter is how long a working-spinner title is believed
// after the last sign of life in its pane — a few idle thresholds, and
// never less than spinnerStaleFloor. A working agent shows one far more
// often than that: its title spinner animates, and Claude's status line
// is on screen for the whole turn.
func spinnerStaleAfter(idle time.Duration) time.Duration {
	return max(spinnerStaleFloor, 3*idle)
}

// liveTitle is the title to classify a pane by. Every agent's rules
// read a braille spinner at the start of the OSC title as "working",
// ahead of any body rule. But tmux keeps #{pane_title} after the
// program that set it exits, so an agent that crashed mid-turn left
// its spinner behind: the session read as active forever, over the
// shell prompt its launch chain fell back to, and the sleep lock was
// never released.
//
// So a spinner only counts while there is evidence of life (lastLife)
// within staleAfter of now: the title changed between two of the
// daemon's reads (the spinner animating, or just set), or the body
// shows a turn running (agent.TurnView.Busy). Past that, the pane is
// classified as if the title carried no spinner, and the body decides
// (a shell prompt: the agent crashed).
//
// Neither a first look nor a body change is life. The title found on a
// first look — a new session, or every session after a daemon restart
// — may be a dead agent's leftover: believing it showed a crashed
// session active for the whole stale window, holding the sleep lock,
// and then announced its "turn" ending with a bell and push, on every
// restart. And a body change is as likely the user typing into the
// shell the dead agent fell back to (`claude`, to bring it back) as
// the agent working.
func liveTitle(title string, lastLife, now time.Time, staleAfter time.Duration) string {
	if now.Sub(lastLife) < staleAfter {
		return title
	}
	// Past indentation too: claude.ClassifyWithTitle trims before it looks.
	if r, _ := utf8.DecodeRuneInString(strings.TrimLeftFunc(title, unicode.IsSpace)); !isBraille(r) {
		return title
	}
	return strings.TrimLeftFunc(title, func(r rune) bool { return unicode.IsSpace(r) || isBraille(r) })
}

// isSpinnerTitle reports whether title opens with a braille spinner
// glyph: what the title_spinner_working rule in every agent's rule file
// matches (`^[\x{2800}-\x{28FF}]`).
func isSpinnerTitle(title string) bool {
	r, _ := utf8.DecodeRuneInString(title)
	return isBraille(r)
}

func isBraille(r rune) bool { return r >= 0x2800 && r <= 0x28FF }

// later returns the later of two times.
func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
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

// attentionInput is one session's poll tick as decideAttention sees it.
type attentionInput struct {
	Prev, Next agent.State
	PrevSeen   bool // the reviewed flag before this tick
	Attached   bool // a tmux client is attached
	// Work is whether this tick shows the agent working, and Worked
	// whether it has worked since its last turn ended — since it last
	// settled into needs_input, idle or error (this tick included). See
	// turn.
	Work, Worked bool
	// Joined: the daemon first saw this session already running and it
	// hasn't settled since (see turn). Nothing it does is news yet.
	Joined bool
}

// decideAttention computes the per-session decision for one poll
// tick:
//
//   - Event: every state change is published (state_change, or
//     needs_input for a change into needs_input), news or not, so
//     dashboards always show the current state.
//   - News: whether the change is something to tell the user about —
//     see isNews. Only news has the side effects below. A redraw, the
//     user typing into the input box, or a new session settling after
//     startup changes the state but is not a turn of the agent's.
//   - Bell and prompt count: on news that enters needs_input, attached
//     or not. Bell delivery self-limits: tmux.RingBell writes BEL only
//     to the clients attached to that session, so ringing an
//     unattached one is a no-op. (Gating on !attached, as PR #156 did,
//     made the ring condition and the delivery set mutually exclusive:
//     the bell never reached anyone.) The prompt count is a lifetime
//     count of the agent's turns ending in a prompt.
//   - Push and seen: on news while nobody is attached, the push is
//     sent (maybePushForStateTransition picks the transitions worth a
//     notification: needs_input, active → idle) and the session is
//     marked unreviewed. An attached user is watching: no push, and
//     the session counts as reviewed.
func decideAttention(in attentionInput) attentionDecision {
	d := attentionDecision{NewSeen: in.PrevSeen || in.Attached}
	if in.Next == in.Prev {
		return d
	}
	d.EmitStateEvent = true
	d.StateEventKind = "state_change"
	if in.Next == agent.StateNeedsInput {
		d.StateEventKind = "needs_input"
	}
	if in.Joined || !isNews(in) {
		return d
	}
	if in.Next == agent.StateNeedsInput {
		d.RingBell = true
		d.IncPromptCount = true
	}
	if !in.Attached {
		d.NewSeen = false
		d.SendPush = true
	}
	return d
}

// isNews reports whether a state change tells the user something:
//
//   - Into needs_input or idle — the agent stopped: only after it
//     worked (Worked). A waiting session that "comes back" to its
//     prompt after a redraw or the user's typing, and a new session
//     whose startup output settles, haven't done anything.
//   - Out of unknown — a new session's first classification: never
//     on its own (into needs_input, as above: only after work).
//   - Into active: only when this tick is work (Work).
//   - Anything else — into error (the agent crashed to a shell), or
//     back to unknown (the pane emptied) — always.
func isNews(in attentionInput) bool {
	switch {
	case in.Next == agent.StateNeedsInput, in.Next == agent.StateIdle && in.Prev != agent.StateUnknown:
		return in.Worked
	case in.Prev == agent.StateUnknown:
		return false
	case in.Next == agent.StateActive:
		return in.Work
	default:
		return true
	}
}

// evidence is what one tick's capture says about the agent working, for
// turn.attend.
type evidence struct {
	spinning bool // a live working-spinner title (see liveTitle)
	// busy: the body shows a turn running in a way typing can't produce
	// (agent.TurnView.Busy — Claude's `esc to interrupt` status line, an
	// agent's working footer).
	busy bool
	// separated: the agent keeps its output apart from the user's input
	// (agent.TurnReader), and output reports new output above its input
	// area since last tick.
	separated, output bool
}

// turn is a session's turn bookkeeping: what decides whether the agent
// did real work since the user last heard from it, so that only the
// end of a real turn notifies. Typing into the agent's input box, a
// redraw, and a new session's startup change the pane too; none of
// them is work.
//
// Evidence of work, strongest first:
//
//   - A live working-spinner title (see liveTitle) — the agent itself
//     broadcasting that it's working — or a body that shows a turn
//     running (evidence.busy). These count even during startup: an
//     agent launched with a first prompt is working on it.
//   - For an agent that keeps its output apart from its input box
//     (Claude Code), new output above the box. Typing only changes the
//     box, so this tells a turn from the user typing, however short the
//     turn — one that starts and ends between two polls still left its
//     answer above the box. Nothing else about the body counts.
//   - For any other agent, the session classifying as active — the
//     generic fallback, which can't tell the agent's output from the
//     user's typing. Once the session has shown a spinner (spinnerSeen)
//     the agent is known to announce its turns, so this no longer
//     counts: typing then doesn't notify, at the cost of missing a turn
//     that ends between two polls.
//
// A session created while the daemon watched is in startup until it
// first settles (needs_input, idle or error): only the strongest
// evidence counts then, since its startup output is not a turn.
//
// A session the daemon joined — one it first saw already running, after
// a restart or a rename done straight through tmux — is recorded as it
// stands (the baseline) and stays joined until it first settles.
// Nothing is news until then: not the end of a turn it was caught in
// the middle of, and not a crash. Its next turn is.
//
// Each notification needs a turn of its own: worked is cleared whenever
// the session settles into needs_input, idle or error, whether or not
// that settle was announced. A turn that ended in idle ("finished") or
// error (a crash) left it set before, so the next thing that merely
// changed the state — the user typing and pausing, or relaunching the
// agent in the crashed pane — notified again.
type turn struct {
	spinnerSeen bool
	worked      bool
	startup     bool
	joined      bool
}

// settled reports whether st is a state a turn ends in.
func settled(st agent.State) bool {
	return st == agent.StateNeedsInput || st == agent.StateIdle || st == agent.StateError
}

// attend folds one classified tick into the bookkeeping and decides the
// attention side effects.
func (tn *turn) attend(prev, next agent.State, ev evidence, prevSeen, attached bool) attentionDecision {
	work := tn.isWork(next, ev)
	tn.worked = tn.worked || work
	d := decideAttention(attentionInput{
		Prev: prev, Next: next, PrevSeen: prevSeen, Attached: attached,
		Work: work, Worked: tn.worked, Joined: tn.joined,
	})
	if settled(next) {
		if next != prev {
			// The turn ended, into whatever state — decided just now,
			// announced or not. The next notification needs a turn of
			// its own.
			tn.worked = false
		}
		tn.startup, tn.joined = false, false
	}
	return d
}

// isWork reports whether this tick is evidence of the agent working
// (see turn).
func (tn *turn) isWork(next agent.State, ev evidence) bool {
	if ev.spinning {
		tn.spinnerSeen = true
	}
	switch {
	case tn.joined:
		return false
	case ev.spinning || ev.busy:
		return true
	case tn.startup:
		return false
	case ev.separated:
		return ev.output
	default:
		return !tn.spinnerSeen && next == agent.StateActive
	}
}

// renameTracked moves the tracked entry oldName→newName, stamping it
// touched so a poll tick already in flight (whose Phase-1 live-set
// snapshot predates the rename) doesn't GC the entry in Phase 3 —
// which would reset promptCount, clear the seen bit, and emit a
// spurious "created" event on the next tick — and tombstoning the old
// name so that tick doesn't re-track it either. Called by handleRename
// after the tmux rename succeeds.
//
// It returns the renamed session's state, read under the lock, for the
// "created" event that announces the new name. A same-name rename
// changes nothing (storing then deleting the one key used to drop the
// entry altogether).
func (s *server) renameTracked(oldName, newName string) daemon.SessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if oldName == newName {
		return s.trackedStateLocked(newName)
	}
	now := time.Now()
	s.buryLocked(oldName, now)
	delete(s.gone, newName)
	if t, ok := s.seen[oldName]; ok {
		t.touched = now
		s.seen[newName] = t
		delete(s.seen, oldName)
	}
	return s.trackedStateLocked(newName)
}

// forgetKilled drops a session the daemon just killed from tracking and
// tombstones its name, so a poll tick that listed it before the kill
// doesn't bring it back. It returns the session as last seen, for the
// "killed" event.
func (s *server) forgetKilled(name string) daemon.SessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	last := s.trackedStateLocked(name)
	delete(s.seen, name)
	s.buryLocked(name, time.Now())
	return last
}

// tombstoneTTL is how long a killed or renamed-away name stays
// tombstoned. Only a poll tick whose list started before the kill can
// still see the name, and every tick ends within its budget (10s).
const tombstoneTTL = time.Minute

// buryLocked tombstones name as of at. Caller holds s.mu.
func (s *server) buryLocked(name string, at time.Time) {
	if s.gone == nil {
		s.gone = map[string]time.Time{}
	}
	s.gone[name] = at
}

// pruneTombstonesLocked drops tombstones older than tombstoneTTL.
// Caller holds s.mu.
func (s *server) pruneTombstonesLocked(now time.Time) {
	for name, at := range s.gone {
		if now.Sub(at) > tombstoneTTL {
			delete(s.gone, name)
		}
	}
}

// trackedStateLocked is the SessionState for a tracked session, or an
// "unknown", reviewed one when the daemon isn't tracking it yet (the
// same default listSessions uses). Caller holds s.mu.
func (s *server) trackedStateLocked(name string) daemon.SessionState {
	if t, ok := s.seen[name]; ok {
		return t.sessionState(name)
	}
	return daemon.SessionState{Name: name, Host: "local", State: string(agent.StateUnknown), Seen: true}
}

// sessionState is the wire view of a tracked session: what the daemon
// knows of it plus the list-sessions row it was last seen with. Every
// event — created, state_change, needs_input, killed (the session as
// last seen) — carries it, so an event-stream client gets the same
// fields GET /v1/sessions reports rather than a name and a bare state.
func (t *tracked) sessionState(name string) daemon.SessionState {
	st := t.state
	if st == "" {
		st = agent.StateUnknown
	}
	path := t.projectPath
	if path == "" {
		path = t.listed.Path
	}
	return daemon.SessionState{
		Name: name, Host: "local", Project: t.project, Path: path,
		State: string(st), Attached: t.listed.Attached, Windows: t.listed.Windows,
		Created: t.listed.Created, LastChange: t.lastChange, PromptCount: t.promptCount,
		Agent: string(t.agentID), Seen: t.seen,
	}
}

// shellAgentID marks a session tagged as a plain shell (tmux.ShellAgentTag).
// It is not a real agent: ByID would fall back to Claude, so callers
// check for it before classifying.
const shellAgentID agent.ID = tmux.ShellAgentTag

// ccmuxSessionPrefix starts the name of every session ccmux creates:
// a project's (tmux.SessionNameForBase) and a bare shell's ("c-shell-…").
const ccmuxSessionPrefix = "c-"

// noteSessionLocked folds this tick's list-sessions row into t: its
// project (recomputed only when its path changes — it stats the
// filesystem) and what runs there (see sessionAgent). Caller holds s.mu.
func (s *server) noteSessionLocked(t *tracked, ts tmux.Session) {
	if !t.projectKnown || t.projectOf != ts.Path {
		t.project, t.projectOf, t.projectKnown = s.sessionProject(ts.Path), ts.Path, true
	}
	t.projectPath = ts.Path
	t.listed = ts
	// A tag names what runs in this very session, so it is read every
	// tick and may change. A sidecar agent is resolved once and kept for
	// the tracked session's lifetime: the sidecar names the agent a
	// project's NEXT session starts with, and rewriting it (the Projects
	// screen's `a`) used to reclassify the sessions already running there
	// by another agent's rules — a Claude session shown as a Codex one
	// waiting for input. A session that follows its foreground has its
	// agent set by each tick (pollOnce).
	id, fixed := s.fixedAgent(ts)
	_, tagged := taggedAgent(ts)
	switch {
	case !fixed:
		if !t.follow {
			t.agentID = shellAgentID // until a tick sees what runs there
		}
		t.follow = true
	case tagged:
		t.agentID, t.follow = id, false
	case t.agentID == "" || t.agentTagged || t.follow:
		t.agentID, t.follow = id, false
	}
	t.agentTagged = tagged
}

// sessionAgent resolves what runs in a session, as far as its tmux
// metadata tells (see fixedAgent): shellAgentID for a session that
// follows its foreground, which only a poll tick can see into.
func (s *server) sessionAgent(ts tmux.Session) agent.ID {
	id, _ := s.fixedAgent(ts)
	return id
}

// fixedAgent resolves the agent a session was started for, and fixed
// reports whether it has one:
//
//   - Its explicit @ccmux_agent tag naming an agent (a project session,
//     a resumed conversation, a bare agent session): authoritative.
//   - No tag, and named with the "c-" prefix: a session an older ccmux
//     created before it tagged them — the project's .ccmux/agent
//     sidecar, Claude when there is none (the back-compat default for
//     projects that predate the sidecar).
//
// Anything else — a session tagged "shell" (`ccmux shell`, a bare
// session), or one the user made outside ccmux wherever it runs — has
// no agent of its own: it follows its foreground (fixed false, id
// shellAgentID). A poll tick classifies it by the agent running in its
// pane's foreground (agent.InForeground over #{pane_current_command})
// while one does, and treats it as a shell otherwise. That is what
// someone running `claude` by hand in a plain tmux session or a ccmux
// shell gets notified for; and a log tail or a zsh prompt — even in a
// project directory — is never judged by an agent's rules (that turned
// a log tail into an active/idle flapper that pushed on every line, and
// a zsh prompt into a "crashed" error).
//
// ccmux's own sessions keep their tag or sidecar because the agent they
// launch runs under `$SHELL -c "agent || fallback"`: a non-interactive
// shell without job control, so their foreground process is that shell.
func (s *server) fixedAgent(ts tmux.Session) (id agent.ID, fixed bool) {
	if id, ok := taggedAgent(ts); ok {
		return id, id != shellAgentID
	}
	if strings.HasPrefix(ts.Name, ccmuxSessionPrefix) {
		return s.projectAgent(ts.Path), true
	}
	return shellAgentID, false
}

// taggedAgent is a session's explicit @ccmux_agent tag, if it has one.
func taggedAgent(ts tmux.Session) (agent.ID, bool) {
	if strings.TrimSpace(ts.Agent) == tmux.ShellAgentTag {
		return shellAgentID, true
	}
	return agent.ParseID(ts.Agent)
}

// sessionProject names the project a session at path belongs to, the
// way list_projects, the TUI and the CLI name projects
// (project.Discover): the non-hidden directory directly under the
// projects root that contains path. A path outside the root counts only
// when it carries a .ccmux/agent sidecar (a session ccmux started at an
// explicit path), and is named by its basename. Anything else: "".
func (s *server) sessionProject(path string) string {
	return projectName(project.ResolveRoot(s.cfg.Projects.Root), path)
}

func projectName(root, path string) string {
	if path == "" {
		return ""
	}
	if name, ok := dirUnder(root, path); ok {
		return name
	}
	// Either side may reach the same directory through a symlink (a
	// root on an external drive, macOS's /tmp → /private/tmp).
	if r, err := filepath.EvalSymlinks(root); err == nil {
		if p, err := filepath.EvalSymlinks(path); err == nil {
			if name, ok := dirUnder(r, p); ok {
				return name
			}
		}
	}
	if fi, err := os.Stat(project.AgentSidecarPath(path)); err == nil && fi.Mode().IsRegular() {
		return filepath.Base(filepath.Clean(path))
	}
	return ""
}

// dirUnder returns the first path element of path below root, when
// path is inside root and that element is a project name (non-hidden).
func dirUnder(root, path string) (string, bool) {
	if root == "" || !filepath.IsAbs(root) || !filepath.IsAbs(path) {
		return "", false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil || rel == "." {
		return "", false
	}
	first, _, _ := strings.Cut(rel, string(filepath.Separator))
	if first == ".." || strings.HasPrefix(first, ".") {
		return "", false
	}
	return first, true
}

func (s *server) projectAgent(projectPath string) agent.ID {
	if s.readAgent != nil {
		return s.readAgent(projectPath)
	}
	return project.ReadAgent(projectPath)
}

// lookupSnap returns the Phase 1 snapshot for `name` — its tmux.Session
// and what the session looked like before this tick — so Phase 3 can
// use them without re-locking or re-shelling out.
func lookupSnap(snaps []pollSnap, name string) (pollSnap, bool) {
	for _, sn := range snaps {
		if sn.ts.Name == name {
			return sn, true
		}
	}
	return pollSnap{}, false
}

// moshiRefreshInterval is how often moshiLoop re-detects Moshi. It only
// drives the status-bar badge, so a minute of staleness is invisible.
const moshiRefreshInterval = 60 * time.Second

// moshiLoop keeps the moshi.Status cache warm for applyChrome's
// "reachable via Moshi" badge until ctx is cancelled. Detection runs
// moshi-hook and `brew services list` with multi-second timeouts, so it
// has its own ticker instead of running inside the poll tick (where it
// could use up the tick's budget and fail every capture), and it holds
// moshiMu only to store the result, so a create-session handler never
// waits on it.
func (s *server) moshiLoop(ctx context.Context) {
	if s.detectMoshi == nil {
		return
	}
	refresh := func() {
		st := s.detectMoshi(ctx)
		if ctx.Err() != nil {
			return // cut short by shutdown: a partial answer, don't keep it
		}
		s.moshiMu.Lock()
		s.moshiState = st
		s.moshiMu.Unlock()
	}
	refresh()
	t := time.NewTicker(moshiRefreshInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			refresh()
		}
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
	// The marker lives next to the socket, so a daemon that died holding
	// the very_dangerous override is cleaned up by the next one even if
	// the user has since switched modes.
	if sock, err := daemon.SocketPath(); err == nil {
		s.sleeper.SetOverrideMarker(filepath.Join(filepath.Dir(sock), "sleep-override"))
	}
	s.sleeper.RevertStaleOverride()
	log.Printf("ccmuxd: sleep manager initialized (mode=%s, low_battery_cutoff=%d%%)",
		s.sleeper.Requested(), cutoff)
}
