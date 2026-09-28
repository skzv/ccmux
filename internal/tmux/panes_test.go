package tmux

import (
	"context"
	"errors"
	"testing"
)

func TestParsePanes(t *testing.T) {
	out := "%3\t0\t1\t80\t12\t10\t⠋ Refactor\tpoll loop\n" + // a tab inside the title
		"%1\t0\t0\t80\t11\t11\thost.local\n" +
		"garbage line\n" +
		"x9\t1\t0\t80\t24\t00\tnot a pane id\n" +
		"%7\t1\t0\t100\t30\t00\t\n"
	got := parsePanes([]byte(out))
	want := []Pane{
		{ID: "%3", Window: 0, Index: 1, Width: 80, Height: 12, Title: "⠋ Refactor\tpoll loop"},
		{ID: "%1", Window: 0, Index: 0, Width: 80, Height: 11, Active: true, Title: "host.local"},
		{ID: "%7", Window: 1, Index: 0, Width: 100, Height: 30},
	}
	if len(got) != len(want) {
		t.Fatalf("parsePanes = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("pane %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestOldestPane — the lowest pane id is the session's original pane,
// whatever window or pane index it now has: `split-window -b` gives the
// new pane index 0, and a moved window can take index 0.
func TestOldestPane(t *testing.T) {
	panes := []Pane{
		{ID: "%12", Window: 0, Index: 0}, // split in front of the agent
		{ID: "%9", Window: 0, Index: 1},  // the agent
		{ID: "%10", Window: 1, Index: 0},
		{ID: "bogus"},
	}
	p, ok := OldestPane(panes)
	if !ok || p.ID != "%9" {
		t.Errorf("OldestPane = %+v, %v; want %%9", p, ok)
	}
	if _, ok := OldestPane(nil); ok {
		t.Error("OldestPane(nil) reported a pane")
	}
	if _, ok := OldestPane([]Pane{{ID: "%x"}}); ok {
		t.Error("OldestPane accepted an invalid id")
	}
}

// TestCapturePaneID_RefusesNonPaneTargets — the id is passed to -t as
// is, so anything else (a session or window spec) must never get there.
func TestCapturePaneID_RefusesNonPaneTargets(t *testing.T) {
	for _, id := range []string{"", "%", "=c-foo:", "c-foo", "%1.2", "%1;kill-server", "-t"} {
		if _, err := CapturePaneID(context.Background(), id, 10); !errors.Is(err, errBadPaneID) {
			t.Errorf("CapturePaneID(%q) err = %v, want errBadPaneID", id, err)
		}
		if err := SendKeysPane(context.Background(), id, "y"); !errors.Is(err, errBadPaneID) {
			t.Errorf("SendKeysPane(%q) err = %v, want errBadPaneID", id, err)
		}
	}
}
