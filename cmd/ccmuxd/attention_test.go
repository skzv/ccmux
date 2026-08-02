package main

import (
	"testing"

	"github.com/skzv/ccmux/internal/agent"
)

// TestDecideAttention is the table that locks the Phase 2 lifecycle.
// Each row is one poll tick: previous state, new state, previous seen
// bit, and whether the user is currently attached. The expected
// decision captures whether seen flips, whether the bell/push fire
// (the suppression rule for the attached session), and whether the
// state-change event is emitted.
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
		want               want
	}{
		// === HAPPY PATHS: not attached, agent reports something to look at. ===
		{
			name: "fresh needs_input → bell + push + emit + unseen",
			prev: agent.StateActive, next: agent.StateNeedsInput,
			prevSeen: true, attached: false,
			want: want{
				newSeen: false, bell: true, push: true,
				emit: true, eventKind: "needs_input", incPromptCount: true,
			},
		},
		{
			name: "active→idle while unattended → unseen + push, no bell",
			prev: agent.StateActive, next: agent.StateIdle,
			prevSeen: true, attached: false,
			want: want{
				newSeen: false, bell: false, push: true,
				emit: true, eventKind: "state_change", incPromptCount: false,
			},
		},
		// === ATTACHED USER: bell and push suppressed even on big transitions. ===
		{
			name: "needs_input while attached → seen=true, no bell, no push, still emit",
			prev: agent.StateActive, next: agent.StateNeedsInput,
			prevSeen: true, attached: true,
			want: want{
				newSeen: true, bell: false, push: false,
				emit: true, eventKind: "needs_input", incPromptCount: true,
			},
		},
		{
			name: "active→idle while attached → seen=true, no notif",
			prev: agent.StateActive, next: agent.StateIdle,
			prevSeen: true, attached: true,
			want: want{
				newSeen: true, bell: false, push: false,
				emit: true, eventKind: "state_change",
			},
		},
		// === NO TRANSITION: nothing to do beyond keeping seen aligned with attached. ===
		{
			name: "no transition, attached marks seen",
			prev: agent.StateActive, next: agent.StateActive,
			prevSeen: false, attached: true,
			want: want{newSeen: true},
		},
		{
			name: "no transition, not attached preserves seen",
			prev: agent.StateActive, next: agent.StateActive,
			prevSeen: false, attached: false,
			want: want{newSeen: false},
		},
		// === SECOND NEEDS_INPUT IN A ROW: doesn't re-bell. ===
		{
			name: "needs_input→needs_input is not a fresh transition",
			prev: agent.StateNeedsInput, next: agent.StateNeedsInput,
			prevSeen: true, attached: false,
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
			got := decideAttention(tc.prev, tc.next, tc.prevSeen, tc.attached)
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

// TestDecideAttention_AttachedSuppressesBellAcrossEntireSequence —
// integration-style: an attached user transitioning active → working
// → needs_input → idle → needs_input must never trigger a bell. The
// audit-flagged UX promise.
func TestDecideAttention_AttachedSuppressesBellAcrossEntireSequence(t *testing.T) {
	state := agent.StateUnknown
	seen := true
	bells := 0
	pushes := 0
	for _, next := range []agent.State{
		agent.StateActive,
		agent.StateActive,
		agent.StateNeedsInput,
		agent.StateActive,
		agent.StateIdle,
		agent.StateNeedsInput,
	} {
		d := decideAttention(state, next, seen, true /* attached */)
		if d.RingBell {
			bells++
		}
		if d.SendPush {
			pushes++
		}
		seen = d.NewSeen
		state = next
	}
	if bells != 0 {
		t.Errorf("attached session rang the bell %d times across the sequence; expected 0", bells)
	}
	if pushes != 0 {
		t.Errorf("attached session sent %d pushes across the sequence; expected 0", pushes)
	}
}
