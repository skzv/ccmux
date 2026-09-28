package main

import (
	"testing"

	"github.com/skzv/ccmux/internal/agent"
)

// TestDecideAttention is the table that locks the attention lifecycle.
// Each row is one poll tick: previous state, new state, previous seen
// bit, whether the user is attached, and the turn evidence (work this
// tick; work since the session last entered needs_input). The expected
// decision captures whether seen flips, whether the bell/push fire
// (push is suppressed for the attached session; the bell rings on
// every notified needs_input transition because tmux only delivers BEL
// to attached clients anyway), and whether the state-change event is
// emitted.
func TestDecideAttention(t *testing.T) {
	type want struct {
		newSeen        bool
		bell           bool
		push           bool
		emit           bool
		eventKind      string
		incPromptCount bool
	}
	for _, tc := range []struct {
		name               string
		prev, next         agent.State
		prevSeen, attached bool
		work, worked       bool
		want               want
	}{
		// === HAPPY PATHS: not attached, the agent's turn ended. ===
		{
			name: "needs_input after work → bell + push + emit + unseen",
			prev: agent.StateActive, next: agent.StateNeedsInput,
			prevSeen: true, worked: true,
			want: want{
				newSeen: false, bell: true, push: true,
				emit: true, eventKind: "needs_input", incPromptCount: true,
			},
		},
		{
			name: "active→idle after work while unattended → unseen + push, no bell",
			prev: agent.StateActive, next: agent.StateIdle,
			prevSeen: true, worked: true,
			want: want{
				newSeen: false, bell: false, push: true,
				emit: true, eventKind: "state_change", incPromptCount: false,
			},
		},
		// === ATTACHED USER: push suppressed; the bell still rings (tmux
		//     delivers BEL only to attached clients, so ringing here is
		//     exactly how the attached terminal gets its cue — gating on
		//     !attached made the bell unreachable, the PR #156 regression). ===
		{
			name: "needs_input after work while attached → bell rings, no push, still emit, seen=true",
			prev: agent.StateActive, next: agent.StateNeedsInput,
			prevSeen: true, attached: true, worked: true,
			want: want{
				newSeen: true, bell: true, push: false,
				emit: true, eventKind: "needs_input", incPromptCount: true,
			},
		},
		{
			name: "active→idle while attached → seen=true, no notif",
			prev: agent.StateActive, next: agent.StateIdle,
			prevSeen: true, attached: true, worked: true,
			want: want{
				newSeen: true, bell: false, push: false,
				emit: true, eventKind: "state_change",
			},
		},
		// === NO TRANSITION: nothing to do beyond keeping seen aligned with attached. ===
		{
			name: "no transition, attached marks seen",
			prev: agent.StateActive, next: agent.StateActive,
			prevSeen: false, attached: true, work: true, worked: true,
			want: want{newSeen: true},
		},
		{
			name: "no transition, not attached preserves seen",
			prev: agent.StateActive, next: agent.StateActive,
			prevSeen: false, work: true, worked: true,
			want: want{newSeen: false},
		},
		// === INTO ACTIVE: unreviewed output only when it's the agent working. ===
		{
			name: "idle→active with work while unattended → unseen, event",
			prev: agent.StateIdle, next: agent.StateActive,
			prevSeen: true, work: true, worked: true,
			want: want{newSeen: false, push: true, emit: true, eventKind: "state_change"},
		},
		{
			name: "needs_input→active without work (typing, a redraw) → event only",
			prev: agent.StateNeedsInput, next: agent.StateActive,
			prevSeen: true,
			want:     want{newSeen: true, emit: true, eventKind: "state_change"},
		},
		// === NOT A TURN: the state comes back to needs_input/idle with no
		//     work since the last needs_input — the user typed into the
		//     input box and paused, the pane was redrawn, or a new session
		//     settled after startup. The state is published; nothing else. ===
		{
			name: "active→needs_input without work → event only",
			prev: agent.StateActive, next: agent.StateNeedsInput,
			prevSeen: true,
			want:     want{newSeen: true, emit: true, eventKind: "needs_input"},
		},
		{
			name: "active→idle without work → event only",
			prev: agent.StateActive, next: agent.StateIdle,
			prevSeen: true,
			want:     want{newSeen: true, emit: true, eventKind: "state_change"},
		},
		// === FIRST CLASSIFICATION of a new session (out of Unknown): it
		//     starts reviewed, and its first state isn't news — waiting
		//     for input included, unless it got there by working. ===
		{
			name: "unknown→active while unattended keeps seen, no push",
			prev: agent.StateUnknown, next: agent.StateActive,
			prevSeen: true, work: true, worked: true,
			want: want{newSeen: true, emit: true, eventKind: "state_change"},
		},
		{
			name: "unknown→idle while unattended keeps seen, no push",
			prev: agent.StateUnknown, next: agent.StateIdle,
			prevSeen: true,
			want:     want{newSeen: true, emit: true, eventKind: "state_change"},
		},
		{
			name: "unknown→needs_input at startup is not news",
			prev: agent.StateUnknown, next: agent.StateNeedsInput,
			prevSeen: true,
			want:     want{newSeen: true, emit: true, eventKind: "needs_input"},
		},
		// === INTO ERROR: the agent crashed to a shell — always worth a look. ===
		{
			name: "active→error while unattended → unseen, no bell",
			prev: agent.StateActive, next: agent.StateError,
			prevSeen: true,
			want:     want{newSeen: false, push: true, emit: true, eventKind: "state_change"},
		},
		// === SECOND NEEDS_INPUT IN A ROW: doesn't re-bell. ===
		{
			name: "needs_input→needs_input is not a fresh transition",
			prev: agent.StateNeedsInput, next: agent.StateNeedsInput,
			prevSeen: true, worked: true,
			want: want{newSeen: true},
		},
		// === DETACHING from a needs_input: state didn't change, no event, but
		//     seen stays false because we never auto-flip it true off-attach. ===
		{
			name: "stay needs_input while attached marks seen",
			prev: agent.StateNeedsInput, next: agent.StateNeedsInput,
			prevSeen: false, attached: true,
			want: want{newSeen: true},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := decideAttention(attentionInput{
				Prev: tc.prev, Next: tc.next, PrevSeen: tc.prevSeen, Attached: tc.attached,
				Work: tc.work, Worked: tc.worked,
			})
			if got.NewSeen != tc.want.newSeen {
				t.Errorf("NewSeen = %v, want %v", got.NewSeen, tc.want.newSeen)
			}
			if got.RingBell != tc.want.bell {
				t.Errorf("RingBell = %v, want %v", got.RingBell, tc.want.bell)
			}
			if got.SendPush != tc.want.push {
				t.Errorf("SendPush = %v, want %v", got.SendPush, tc.want.push)
			}
			if got.EmitStateEvent != tc.want.emit {
				t.Errorf("EmitStateEvent = %v, want %v", got.EmitStateEvent, tc.want.emit)
			}
			if got.EmitStateEvent && got.StateEventKind != tc.want.eventKind {
				t.Errorf("StateEventKind = %q, want %q", got.StateEventKind, tc.want.eventKind)
			}
			if got.IncPromptCount != tc.want.incPromptCount {
				t.Errorf("IncPromptCount = %v, want %v", got.IncPromptCount, tc.want.incPromptCount)
			}
		})
	}
}

// tick is one poll tick for a turn-bookkeeping sequence test.
type tick struct {
	next     agent.State
	spinning bool
}

// runTurns feeds a tick sequence through turn.attend the way pollOnce
// does and counts bells and the pushes maybePushForStateTransition
// would send (it only notifies needs_input and active → idle).
func runTurns(tn *turn, attached bool, ticks []tick) (bells, pushes int) {
	state, seen := agent.StateUnknown, true
	for _, tk := range ticks {
		d := tn.attend(state, tk.next, tk.spinning, seen, attached)
		if d.RingBell {
			bells++
		}
		if d.SendPush && (tk.next == agent.StateNeedsInput || tk.next == agent.StateIdle && state == agent.StateActive) {
			pushes++
		}
		seen, state = d.NewSeen, tk.next
	}
	return bells, pushes
}

// TestDecideAttention_AttachedSuppressesPushNotBell — an attached user
// through two turns must never receive a push (their phone doesn't need
// to buzz while they're watching), but the bell must ring at the end of
// each turn — BEL is delivered by tmux to attached clients only, so the
// attached terminal is precisely who the ring is for. (The pre-fix
// expectation of zero bells while attached, combined with
// delivery-to-attached-only, made the bell path dead code — the PR #156
// regression.)
func TestDecideAttention_AttachedSuppressesPushNotBell(t *testing.T) {
	bells, pushes := runTurns(&turn{}, true, []tick{
		{agent.StateActive, false},
		{agent.StateActive, false},
		{agent.StateNeedsInput, false},
		{agent.StateActive, false},
		{agent.StateIdle, false},
		{agent.StateNeedsInput, false},
	})
	if bells != 2 {
		t.Errorf("attached session rang the bell %d times across the sequence; expected 2 (one per turn)", bells)
	}
	if pushes != 0 {
		t.Errorf("attached session sent %d pushes across the sequence; expected 0", pushes)
	}
}

// TestDecideAttention_BellFiresRegardlessOfAttachState pins the fix for
// the dead-bell regression: RingBell must be true at a turn's end
// whether or not a client is attached. Delivery (BEL to attached
// clients only, i.e. a no-op for unattached sessions) is
// tmux.RingBell's concern, not the decision's.
func TestDecideAttention_BellFiresRegardlessOfAttachState(t *testing.T) {
	for _, attached := range []bool{true, false} {
		d := decideAttention(attentionInput{Prev: agent.StateActive, Next: agent.StateNeedsInput, PrevSeen: true, Attached: attached, Worked: true})
		if !d.RingBell {
			t.Errorf("attached=%v: RingBell = false at a turn's end, want true", attached)
		}
	}
}

// TestTurn_SpinnerAgentTypingIsNotATurn — once a session has shown a
// working spinner, only the spinner counts as work: the user typing
// into the input box (body active) and pausing brings the session back
// to needs_input without a bell; submitting (spinner) and the turn's
// end rings.
func TestTurn_SpinnerAgentTypingIsNotATurn(t *testing.T) {
	tn := &turn{}
	bells, _ := runTurns(tn, true, []tick{
		{agent.StateActive, true}, // working
		{agent.StateNeedsInput, false},
	})
	if bells != 1 {
		t.Fatalf("setup: first turn rang %d bells, want 1", bells)
	}
	bells, _ = runTurns(tn, true, []tick{
		{agent.StateNeedsInput, false},
		{agent.StateActive, false}, // typing
		{agent.StateNeedsInput, false},
		{agent.StateActive, false}, // more typing
		{agent.StateNeedsInput, false},
	})
	if bells != 0 {
		t.Errorf("typing into the input box rang %d bells, want 0", bells)
	}
	bells, _ = runTurns(tn, true, []tick{
		{agent.StateNeedsInput, false},
		{agent.StateActive, true}, // submitted: working
		{agent.StateNeedsInput, false},
	})
	if bells != 1 {
		t.Errorf("a real turn after typing rang %d bells, want 1", bells)
	}
}

// TestTurn_StartupSettleIsNotNews — a new session's startup is not a
// turn: its settle (needs_input for an agent reaching its input box,
// idle for startup output going quiet) neither rings nor pushes. For an
// agent without a title signal the next active period is a turn; for
// one that works on a first prompt (spinner) the settle is news.
func TestTurn_StartupSettleIsNotNews(t *testing.T) {
	for _, tc := range []struct {
		name          string
		ticks         []tick
		bells, pushes int
	}{
		{"agent reaches its input box", []tick{
			{agent.StateUnknown, false}, {agent.StateActive, false}, {agent.StateNeedsInput, false},
		}, 0, 0},
		{"startup output goes quiet", []tick{
			{agent.StateActive, false}, {agent.StateActive, false}, {agent.StateIdle, false},
		}, 0, 0},
		{"straight to a prompt", []tick{{agent.StateNeedsInput, false}}, 0, 0},
		{"no title signal: the turn after startup notifies", []tick{
			{agent.StateActive, false}, {agent.StateNeedsInput, false},
			{agent.StateActive, false}, {agent.StateNeedsInput, false},
		}, 1, 1},
		{"a first prompt worked on at startup notifies", []tick{
			{agent.StateActive, true}, {agent.StateActive, true}, {agent.StateNeedsInput, false},
		}, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bells, pushes := runTurns(&turn{startup: true}, false, tc.ticks)
			if bells != tc.bells || pushes != tc.pushes {
				t.Errorf("bells=%d pushes=%d, want %d/%d", bells, pushes, tc.bells, tc.pushes)
			}
		})
	}
}
