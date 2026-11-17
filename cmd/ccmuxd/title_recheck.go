package main

import (
	"context"
	"strings"
	"time"
	"unicode"
)

// A spinner that cycles in step with the poll.
//
// A working-spinner title counts only while the daemon sees it move
// (liveTitle): tmux keeps the title a crashed agent left behind, and a
// spinner nothing animates any more must not read as work. But a spinner
// whose frames come round once per poll interval (ten frames at 200ms,
// against the 2s tick) shows the same glyph on every tick. It looks
// frozen, goes stale, and the turn it announces — when it is the agent's
// only sign of work, as for an agent the daemon knows to show one (see
// turn) — is missed, or a long one reads as finished half-way through.
//
// So when a pane's title still shows the spinner glyph it showed last
// tick, and nothing else says the agent is alive, the daemon reads that
// title once more a moment later, within the same tick. A glyph that
// moved in between is a live spinner. A dead agent's title doesn't move,
// so it still goes stale.

// defaultTitleRecheckDelay is how long after a pane's read its title is
// read again (server.titleRecheckDelay, which tests shorten): several
// frames of any common spinner (80–200ms a frame), and short of a whole
// cycle of the ones that alias with a 2s poll (8–10 frames of 200–250ms).
const defaultTitleRecheckDelay = 400 * time.Millisecond

// maxTitleRechecks caps the titles re-read per tick, so a tick with many
// such panes costs one delay and a handful of list-panes calls at most.
const maxTitleRechecks = 8

// recheckDue reports whether l's title is worth a second read this tick:
// an agent's live pane (read by its id) whose title opens with the same
// spinner glyph as last tick, that liveTitle would no longer believe
// (no change within staleAfter, no body showing a turn running) — while
// the pane shows something that may be work: its body changed this tick,
// or the spinner was believed last tick (a turn in progress whose frames
// have fallen in step with the poll). A pane sitting idle under a frozen
// leftover spinner isn't re-read on every tick.
func (l *look) recheckDue(staleAfter time.Duration) bool {
	switch {
	case l.id == shellAgentID, l.exited, l.first, l.obs.paneID == "", l.titleChanged, l.view.Busy:
		return false
	case !isSpinnerTitle(strings.TrimLeftFunc(l.obs.title, unicode.IsSpace)):
		return false
	case l.at.Sub(l.mem.titleChange) < staleAfter:
		return false // still believed
	}
	return l.obs.body != l.sn.prevLast || l.mem.spinnerLive
}

// recheckTitles gives the panes recheckDue picks (at most
// maxTitleRechecks) a second read of their title, s.titleRecheckDelay
// after the latest of their first reads. A title that changed in between is
// a live spinner: its pane's titleChange moves to the second read, which
// liveTitle believes. The tick's own reading (look.obs.title) is what the
// pane is classified by; the second one is remembered as the pane's
// title, for the next tick to compare with.
func (s *server) recheckTitles(ctx context.Context, looks []look, staleAfter time.Duration) {
	if s.panes == nil {
		return
	}
	var due []*look
	var latest time.Time
	for i := range looks {
		if l := &looks[i]; l.recheckDue(staleAfter) && len(due) < maxTitleRechecks {
			due = append(due, l)
			if l.at.After(latest) {
				latest = l.at
			}
		}
	}
	if len(due) == 0 {
		return
	}
	delay := s.titleRecheckDelay
	if delay <= 0 {
		delay = defaultTitleRecheckDelay
	}
	if wait := time.Until(latest.Add(delay)); wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
	for _, l := range due {
		panes, err := s.panes(ctx, l.sn.ts.Name)
		if err != nil {
			continue
		}
		for _, p := range panes {
			if p.ID == l.obs.paneID && p.Title != l.obs.title {
				l.mem.title, l.mem.titleChange = p.Title, time.Now()
			}
		}
	}
}
