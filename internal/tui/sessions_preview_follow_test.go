package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/daemon"
)

// stubPreviewTick makes previewTickCmd deliver its tick immediately so a
// test can drain the chain without waiting a real second.
func stubPreviewTick(t *testing.T) {
	t.Helper()
	orig := previewTickCmd
	previewTickCmd = func(gen int) tea.Cmd {
		return func() tea.Msg { return previewTickMsg{gen: gen} }
	}
	t.Cleanup(func() { previewTickCmd = orig })
}

func msgsHaveTick(msgs []tea.Msg) bool {
	for _, m := range msgs {
		if _, ok := m.(previewTickMsg); ok {
			return true
		}
	}
	return false
}

// TestE2E_PreviewTickSurvivesOpenForm — the rename (R) and new-session
// (n) forms used to receive every message first, so a preview tick that
// landed while one was open was swallowed and never re-armed: the
// preview froze for good, even after the form was cancelled.
func TestE2E_PreviewTickSurvivesOpenForm(t *testing.T) {
	stubPreviewTick(t)
	for _, key := range []string{"R", "n"} {
		t.Run(key, func(t *testing.T) {
			a := buildPreviewApp(t, "x", nil)
			a, _ = updateApp(t, a, keyMsg("p"))
			a, _ = updateApp(t, a, keyMsg(key))
			if !a.sessionsM.capturesInput() {
				t.Fatalf("%s did not open its form", key)
			}
			_, cmd := updateApp(t, a, previewTickMsg{gen: a.sessionsM.previewGen})
			if !msgsHaveTick(drainCmd(cmd)) {
				t.Fatalf("preview tick swallowed while the %s form was open — the preview freezes", key)
			}
			// A capture that lands while the form is open still updates
			// the pane underneath.
			a = feedCapture(t, a, "fresh while form open", nil)
			if a.sessionsM.preview != "fresh while form open" {
				t.Errorf("capture landing under the %s form was dropped", key)
			}
		})
	}
}

// TestE2E_PreviewFollowsSelectionAfterKill — when the previewed session
// disappears from the list (killed here or elsewhere), the cursor moves
// to another row; the pane must drop the dead session's capture and
// start a fresh one for the new selection instead of showing the dead
// pane until the next tick.
func TestE2E_PreviewFollowsSelectionAfterKill(t *testing.T) {
	a := buildPreviewApp(t, "", nil)
	a, _ = updateApp(t, a, keyMsg("p"))
	dead := selectedSession(t, a)
	a = feedCapture(t, a, "DEAD SESSION PANE", nil)

	var remaining []daemon.SessionState
	for _, s := range a.sessions {
		if s.Name != dead {
			remaining = append(remaining, s)
		}
	}
	a, cmd := updateApp(t, a, sessionsLoadedMsg{Sessions: remaining})
	next := selectedSession(t, a)
	if next == dead {
		t.Fatalf("selection still on the killed session %q", dead)
	}
	out := a.homeView(a.width, a.height)
	if strings.Contains(out, "DEAD SESSION PANE") {
		t.Errorf("preview still shows the killed session's pane under %q:\n%s", next, out)
	}
	sawCapture := false
	for _, m := range drainCmd(cmd) {
		if lm, ok := m.(previewLoadedMsg); ok && lm.Session == next {
			sawCapture = true
		}
	}
	if !sawCapture {
		t.Errorf("no fresh capture scheduled for the new selection %q", next)
	}
}

// TestSessionsRename_KeepsCursorOnRenamedRow — SetSessions finds the
// selection by name; after a rename that name is gone, so the cursor
// used to jump to whichever row slid into its slot.
func TestSessionsRename_KeepsCursorOnRenamedRow(t *testing.T) {
	a := buildPreviewApp(t, "", nil)
	list := []daemon.SessionState{
		{Name: "a-one", Host: "local", State: "idle"},
		{Name: "b-two", Host: "local", State: "idle"},
		{Name: "c-three", Host: "local", State: "idle"},
	}
	a, _ = updateApp(t, a, sessionsLoadedMsg{Sessions: list})
	a, _ = updateApp(t, a, keyMsg("down"))
	if got := selectedSession(t, a); got != "b-two" {
		t.Fatalf("setup: selected %q, want b-two", got)
	}
	a, _ = updateApp(t, a, sessionRenamedMsg{OldName: "b-two", NewName: "zz-renamed"})
	a, _ = updateApp(t, a, sessionsLoadedMsg{Sessions: []daemon.SessionState{
		{Name: "a-one", Host: "local", State: "idle"},
		{Name: "c-three", Host: "local", State: "idle"},
		{Name: "zz-renamed", Host: "local", State: "idle"},
	}})
	if got := selectedSession(t, a); got != "zz-renamed" {
		t.Errorf("after rename the cursor is on %q, want the renamed session zz-renamed", got)
	}
}

// TestSessionsRename_StaleRefreshCannotMoveCursor — with a slow host, a
// refresh started before the rename can land after it, carrying the
// pre-rename list. Applying it re-attached the cursor to the old name,
// and the next (post-rename) list no longer had that name, so the
// cursor jumped to another row. Refreshes older than the rename are now
// dropped.
func TestSessionsRename_StaleRefreshCannotMoveCursor(t *testing.T) {
	a := buildPreviewApp(t, "", nil)
	// Fresh slices per delivery: the model keeps (and renames in) the
	// slice it is handed.
	before := func() []daemon.SessionState {
		return []daemon.SessionState{
			{Name: "a-one", Host: "local", State: "idle"},
			{Name: "b-two", Host: "local", State: "idle"},
			{Name: "c-three", Host: "local", State: "idle"},
		}
	}
	after := []daemon.SessionState{
		{Name: "a-one", Host: "local", State: "idle"},
		{Name: "c-three", Host: "local", State: "idle"},
		{Name: "zz-renamed", Host: "local", State: "idle"},
	}
	a, _ = updateApp(t, a, sessionsLoadedMsg{Sessions: before()})
	a, _ = updateApp(t, a, keyMsg("down"))
	if got := selectedSession(t, a); got != "b-two" {
		t.Fatalf("setup: selected %q, want b-two", got)
	}
	// A tick refresh starts (a slow host holds it up) …
	a.refreshSessionsCmd()
	stale := a.sessionsLoadGen
	// … the rename lands and starts its own refresh …
	a, _ = updateApp(t, a, sessionRenamedMsg{OldName: "b-two", NewName: "zz-renamed"})
	fresh := a.sessionsLoadGen
	if fresh <= stale {
		t.Fatalf("rename started no new refresh (gen %d after %d)", fresh, stale)
	}
	// … then the stale refresh finishes with the pre-rename list.
	a, _ = updateApp(t, a, sessionsLoadedMsg{Sessions: before(), Gen: stale})
	if got := selectedSession(t, a); got != "zz-renamed" {
		t.Errorf("stale refresh moved the cursor to %q, want zz-renamed", got)
	}
	a, _ = updateApp(t, a, sessionsLoadedMsg{Sessions: after, Gen: fresh})
	if got := selectedSession(t, a); got != "zz-renamed" {
		t.Errorf("after the post-rename refresh the cursor is on %q, want zz-renamed", got)
	}
}
