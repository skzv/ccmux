package agent

import "testing"

// TestAttentionPriority — the headline UX move is that a done-but-
// unreviewed agent OUTRANKS a still-working one (it's the one asking
// for the user's eyes; the working agent is fine on its own). Pin
// the full ranking so a future tweak can't quietly invert it.
func TestAttentionPriority(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state State
		seen  bool
		want  int
	}{
		{"needs_input always loudest", StateNeedsInput, true, 4},
		{"needs_input even when seen", StateNeedsInput, false, 4},
		{"error rolls up with needs_input", StateError, true, 4},
		{"done-unreviewed beats working", StateIdle, false, 3},
		{"working", StateActive, true, 2},
		{"working same seen=false", StateActive, false, 2},
		{"reviewed-idle quietest non-zero", StateIdle, true, 1},
		{"unknown is the floor", StateUnknown, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := AttentionPriority(tc.state, tc.seen); got != tc.want {
				t.Errorf("AttentionPriority(%v, seen=%v) = %d, want %d",
					tc.state, tc.seen, got, tc.want)
			}
		})
	}

	// Cross-pair: done-unreviewed > working > reviewed-idle. The
	// strict-ordering check makes the UX promise explicit.
	if AttentionPriority(StateIdle, false) <= AttentionPriority(StateActive, true) {
		t.Error("done-unreviewed must outrank a still-working session")
	}
	if AttentionPriority(StateActive, true) <= AttentionPriority(StateIdle, true) {
		t.Error("working must outrank reviewed-idle")
	}
}
