package tui

import (
	"reflect"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/daemon"
)

// TestSortByAttention_NeedsInputOnTop — the headline behaviour: a
// session waiting on the user surfaces above every still-working
// row, so the dashboard's first line tells you where to look.
func TestSortByAttention_NeedsInputOnTop(t *testing.T) {
	ss := []daemon.SessionState{
		{Name: "c-a", State: string(agent.StateActive), Seen: true},
		{Name: "c-b", State: string(agent.StateNeedsInput), Seen: false},
		{Name: "c-c", State: string(agent.StateIdle), Seen: true},
	}
	sortByAttention(ss)
	gotNames := []string{ss[0].Name, ss[1].Name, ss[2].Name}
	want := []string{"c-b", "c-a", "c-c"}
	if !reflect.DeepEqual(gotNames, want) {
		t.Errorf("order = %v, want %v", gotNames, want)
	}
}

// TestSortByAttention_DoneUnreviewedBeatsWorking — the UX promise the
// audit highlighted: a finished agent the user hasn't reviewed yet
// outranks a still-working one. The finished agent is the one whose
// output the user needs to read before issuing the next prompt.
func TestSortByAttention_DoneUnreviewedBeatsWorking(t *testing.T) {
	ss := []daemon.SessionState{
		{Name: "c-working", State: string(agent.StateActive), Seen: true},
		{Name: "c-done-unseen", State: string(agent.StateIdle), Seen: false},
		{Name: "c-done-seen", State: string(agent.StateIdle), Seen: true},
	}
	sortByAttention(ss)
	if ss[0].Name != "c-done-unseen" {
		t.Errorf("first row = %q, want c-done-unseen (done-unreviewed must outrank working)", ss[0].Name)
	}
	// And reviewed-idle drops to the bottom of the meaningful states.
	if ss[2].Name != "c-done-seen" {
		t.Errorf("last row = %q, want c-done-seen (reviewed-idle is quietest non-zero)", ss[2].Name)
	}
}

// TestSortByAttention_StableOnNameTieBreak — when two rows share a
// priority, sort is deterministic by Name so the order doesn't
// flicker between auto-refresh ticks.
func TestSortByAttention_StableOnNameTieBreak(t *testing.T) {
	ss := []daemon.SessionState{
		{Name: "c-z", State: string(agent.StateActive)},
		{Name: "c-a", State: string(agent.StateActive)},
		{Name: "c-m", State: string(agent.StateActive)},
	}
	sortByAttention(ss)
	want := []string{"c-a", "c-m", "c-z"}
	for i, w := range want {
		if ss[i].Name != w {
			t.Errorf("position %d = %q, want %q", i, ss[i].Name, w)
		}
	}
}

// TestSortByAttention_Idempotent — sorting an already-sorted slice
// doesn't change anything; the secondary stable key guarantees this.
func TestSortByAttention_Idempotent(t *testing.T) {
	ss := []daemon.SessionState{
		{Name: "c-blocked", State: string(agent.StateNeedsInput)},
		{Name: "c-active", State: string(agent.StateActive)},
		{Name: "c-idle", State: string(agent.StateIdle), Seen: true},
	}
	sortByAttention(ss)
	first := []string{ss[0].Name, ss[1].Name, ss[2].Name}
	sortByAttention(ss)
	second := []string{ss[0].Name, ss[1].Name, ss[2].Name}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("re-sort changed order: %v then %v", first, second)
	}
}
