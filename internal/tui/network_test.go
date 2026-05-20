package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNetwork_CursorClampedOnSetHosts(t *testing.T) {
	m := newNetwork(mustStyles(t), DefaultKeymap())
	m.SetHosts([]hostStatus{
		{Name: "a"}, {Name: "b"}, {Name: "c"}, {Name: "d"}, {Name: "e"},
	})
	m.cursor = 4
	// Replace with shorter list; cursor should clamp to last index.
	m.SetHosts([]hostStatus{{Name: "x"}, {Name: "y"}})
	if m.cursor != 1 {
		t.Errorf("cursor = %d, want 1 (last of new shorter list)", m.cursor)
	}
	// Replace with empty list; cursor should clamp to 0.
	m.SetHosts(nil)
	if m.cursor != 0 {
		t.Errorf("cursor = %d on empty list, want 0", m.cursor)
	}
}

func TestNetwork_NavigationKeys(t *testing.T) {
	m := newNetwork(mustStyles(t), DefaultKeymap())
	m.SetHosts([]hostStatus{{Name: "a"}, {Name: "b"}, {Name: "c"}})

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor != 1 {
		t.Errorf("after one down: cursor = %d, want 1", m.cursor)
	}
	// Beyond the last row should clamp.
	for i := 0; i < 10; i++ {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.cursor != 2 {
		t.Errorf("after many downs: cursor = %d, want 2 (last)", m.cursor)
	}
	for i := 0; i < 10; i++ {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	}
	if m.cursor != 0 {
		t.Errorf("after many ups: cursor = %d, want 0", m.cursor)
	}
}

func TestNetwork_SSHCmd_SkipsNonActionableRows(t *testing.T) {
	cases := []struct {
		name string
		host hostStatus
	}{
		{"local row", hostStatus{Name: "self", Local: true}},
		{"mobile row", hostStatus{Name: "iphone", Mobile: true}},
		{"no dial info", hostStatus{Name: "blank"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newNetwork(mustStyles(t), DefaultKeymap())
			m.SetHosts([]hostStatus{tc.host})
			if cmd := m.SSHCmd(); cmd != nil {
				t.Errorf("SSHCmd should be nil for %s, got non-nil", tc.name)
			}
		})
	}
}

func TestNetwork_SSHCmd_OKForReachablePeer(t *testing.T) {
	m := newNetwork(mustStyles(t), DefaultKeymap())
	m.SetHosts([]hostStatus{
		{Name: "mac-mini", Discovered: true, DialHost: "mac-mini", OK: true},
	})
	if cmd := m.SSHCmd(); cmd == nil {
		t.Fatal("SSHCmd should return a tea.Cmd for an ssh-able peer")
	}
}

func TestNetwork_SSHCmd_FallsBackToDialAddrFor(t *testing.T) {
	m := newNetwork(mustStyles(t), DefaultKeymap())
	// No DialHost, but Address is set: helper should strip port and use it.
	m.SetHosts([]hostStatus{
		{Name: "mac-mini", Discovered: true, Address: "100.75.64.20:7474", OK: true},
	})
	if cmd := m.SSHCmd(); cmd == nil {
		t.Fatal("SSHCmd should fall back to dialAddrFor when DialHost is empty")
	}
}

func TestNetwork_View_EmptyState(t *testing.T) {
	m := newNetwork(mustStyles(t), DefaultKeymap())
	view := m.View(100, 30)
	if !strings.Contains(view, "No devices discovered") {
		t.Errorf("empty-state view should mention 'No devices discovered': %q", view)
	}
}

func TestNetwork_View_ShowsAllRowTypes(t *testing.T) {
	m := newNetwork(mustStyles(t), DefaultKeymap())
	m.SetHosts([]hostStatus{
		{Name: "sputnik", Local: true, OK: true, Version: "v1"},
		{Name: "mac-mini", Discovered: true, DialHost: "mac-mini", OK: true, Version: "v1"},
		{Name: "old-box", Discovered: true, NeedsInstall: true},
		{Name: "iphone", Mobile: true, OS: "iOS"},
	})
	view := m.View(120, 30)
	for _, want := range []string{
		"sputnik",
		"(this device)",
		"mac-mini",
		"ccmuxd unreachable",
		"iphone",
		"mobile",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q\n--- view ---\n%s", want, view)
		}
	}
}

func TestHostStatus_DialHostOrAddr(t *testing.T) {
	cases := []struct {
		name string
		h    hostStatus
		want string
	}{
		{"prefers DialHost", hostStatus{DialHost: "mac", Address: "1.2.3.4:7474"}, "mac"},
		{"falls back to Address", hostStatus{Address: "1.2.3.4:7474"}, "1.2.3.4"},
		{"empty when both unset", hostStatus{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.h.DialHostOrAddr(); got != tc.want {
				t.Errorf("DialHostOrAddr() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestNetwork_NarrowLayout — at phone width the Network screen keeps
// the device rows (T0) but drops the colour legend, the inline action
// hint, and the Selected block's os/address/dial/version detail (T2).
func TestNetwork_NarrowLayout(t *testing.T) {
	m := newNetwork(mustStyles(t), DefaultKeymap())
	m.SetHosts([]hostStatus{
		{Name: "sputnik", Local: true, OK: true, OS: "darwin", Address: "100.1.2.3", Version: "v1.0"},
		{Name: "mir", OK: true, OS: "linux", Address: "100.4.5.6", DialHost: "mir.tail", Version: "v1.0"},
	})
	out := m.View(50, 40)
	assertNoOverflow(t, out, 50)
	assertPresent(t, out, "sputnik", "mir")
	assertAbsent(t, out, "100.1.2.3", "ccmuxd reachable", "enter: ssh")
}
