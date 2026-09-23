package tui

import (
	"testing"

	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tui/styles"
)

func selHost(s *daemon.SessionState) string {
	if s == nil {
		return "<nil>"
	}
	return s.Host + "/" + s.Name
}

// TestApp_SelectionKeyedByHostAndName — with `c-ccmux` running on this
// machine AND on the mini, the selected row must survive every 2s poll
// on the host the user picked. A name-only match snapped the cursor to
// the first `c-ccmux` (the other host's row), so Enter attached to the
// wrong machine.
func TestApp_SelectionKeyedByHostAndName(t *testing.T) {
	local := daemon.SessionState{Name: "c-ccmux", Host: "local", State: "idle"}
	mini := daemon.SessionState{Name: "c-ccmux", Host: "mac-mini", State: "idle"}
	other := daemon.SessionState{Name: "c-alpha", Host: "local", State: "idle"}
	cases := []struct {
		name    string
		initial []daemon.SessionState
		pick    string // "host/name" to select before the refresh
		refresh []daemon.SessionState
	}{
		{"remote row, unchanged order", []daemon.SessionState{local, mini}, "mac-mini/c-ccmux", []daemon.SessionState{local, mini}},
		{"remote row, new session sorts above", []daemon.SessionState{local, mini}, "mac-mini/c-ccmux", []daemon.SessionState{other, local, mini}},
		{"local row, hosts reordered", []daemon.SessionState{local, mini}, "local/c-ccmux", []daemon.SessionState{mini, local}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := sendSessions(t, newSessionsApp(t), append([]daemon.SessionState(nil), tc.initial...))
			for i, s := range a.sessionsM.sessions {
				if s.Host+"/"+s.Name == tc.pick {
					a.sessionsM.cursor = i
				}
			}
			if got := selHost(a.sessionsM.Selected()); got != tc.pick {
				t.Fatalf("setup: selected %s, want %s", got, tc.pick)
			}
			// Several polls in a row — the cursor must not drift on any.
			for poll := 0; poll < 3; poll++ {
				a = sendSessions(t, a, append([]daemon.SessionState(nil), tc.refresh...))
				if got := selHost(a.sessionsM.Selected()); got != tc.pick {
					t.Fatalf("poll %d: selection drifted to %s, want %s", poll, got, tc.pick)
				}
			}
		})
	}
}

// TestPreviewSub_StaleLoadFromOtherHostDropped — a capture of the LOCAL
// `c-ccmux` must not be painted into the preview while the mini's
// same-named row is selected (and vice versa).
func TestPreviewSub_StaleLoadFromOtherHostDropped(t *testing.T) {
	m := newSessions(styles.Default(), DefaultKeymap())
	m.sessions = []daemon.SessionState{
		{Name: "c-ccmux", Host: "local", State: "idle"},
		{Name: "c-ccmux", Host: "mac-mini", State: "idle"},
	}
	m.showPreview = true
	m.cursor = 1 // the mini's row
	m.preview = "mini content"

	updated, _ := m.Update(previewLoadedMsg{Session: "c-ccmux", Host: "local", Content: "LOCAL pane"})
	if updated.preview != "mini content" {
		t.Fatalf("capture from the other host's same-named session replaced the preview: %q", updated.preview)
	}
	updated, _ = updated.Update(previewLoadedMsg{Session: "c-ccmux", Host: "mac-mini", Content: "fresh mini"})
	if updated.preview != "fresh mini" {
		t.Fatalf("capture for the selected host was dropped: %q", updated.preview)
	}
	// "" is the daemon wire default for this machine — equivalent to "local".
	updated.cursor = 0
	updated, _ = updated.Update(previewLoadedMsg{Session: "c-ccmux", Host: "", Content: "local via empty host"})
	if updated.preview != "local via empty host" {
		t.Fatalf(`host "" must match a "local" row: %q`, updated.preview)
	}
}
