package tui

import (
	"strings"
	"testing"
	"time"

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
		{Name: "sputnik", Source: "local", Local: true, OK: true, Version: "v1"},
		{Name: "mac-mini", Source: "discovered", Discovered: true, DialHost: "mac-mini", OK: true, Version: "v1"},
		{Name: "old-box", Source: "discovered", Discovered: true, NeedsInstall: true},
		{Name: "iphone", Source: "mobile", Mobile: true, OS: "iOS"},
	})
	view := m.View(120, 30)
	for _, want := range []string{
		"sputnik",
		"(this device)",
		"mac-mini",
		"no ccmuxd",
		"iphone",
		"Moshi",
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

// TestNetwork_ChipsRenderForEachState — pins the chip vocabulary
// against each state's hostStatus shape. Each row exercises a
// single chip; the non-local rows test the chip's presence while
// the local row pins the "no [↑ update] on local" invariant.
func TestNetwork_ChipsRenderForEachState(t *testing.T) {
	m := newNetwork(mustStyles(t), DefaultKeymap())
	m.SetVersion("v1.0")
	m.SetHosts([]hostStatus{
		// Local: never renders [↑ update] even if Version differs.
		{Name: "self", Source: "local", Local: true, OK: true, Version: "v0.9"},
		// Configured peer on an outdated build → [↑ update].
		{Name: "mac-mini", Source: "configured", DialHost: "mac-mini", OK: true, Version: "v0.9"},
		// Configured peer that errored → [unreachable].
		{Name: "broken", Source: "configured", DialHost: "broken", OK: false, Err: errBadDial},
		// Discovered TS-SSH peer → [Tailscale SSH ✓].
		{Name: "tss", Source: "discovered", DialHost: "tss", TailscaleSSH: true, OK: true},
		// Discovered peer with verified pubkey auth → [SSH ✓].
		{Name: "keyed", Source: "discovered", DialHost: "keyed", SSHVerified: true, OK: true},
		// Discovered peer that pings but has no daemon → [no ccmuxd].
		{Name: "no-daemon", Source: "discovered", NeedsInstall: true, OK: true},
		// Mobile peer → [Moshi].
		{Name: "iphone", Source: "mobile", Mobile: true},
	})
	view := m.View(160, 40)

	for _, want := range []string{
		"[↑ update]",
		"[unreachable]",
		"[Tailscale SSH ✓]",
		"[SSH ✓]",
		"[no ccmuxd]",
		"[Moshi]",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q chip\n--- view ---\n%s", want, view)
		}
	}

	// Local row sits at the top of the view (Local section first).
	// The update chip must not appear before the next section
	// heading. Use the section heading as an upper bound.
	localBlock := view
	if idx := strings.Index(view, "Configured"); idx > 0 {
		localBlock = view[:idx]
	}
	if strings.Contains(localBlock, "[↑ update]") {
		t.Errorf("[↑ update] chip rendered on Local row\n--- Local block ---\n%s", localBlock)
	}
	if strings.Contains(localBlock, "[SSH ✓]") {
		t.Errorf("[SSH ✓] chip rendered on Local row\n--- Local block ---\n%s", localBlock)
	}
}

// errBadDial is a sentinel test error used as the Err on an
// unreachable hostStatus fixture. Kept here so the table above
// stays compact.
var errBadDial = networkTestError("dial failed")

type networkTestError string

func (e networkTestError) Error() string { return string(e) }

// TestNetwork_SectionOrderingAndEmptyGroups — pins the four
// source-grouped sections rendering in order, plus the
// empty-section-skip behavior.
func TestNetwork_SectionOrderingAndEmptyGroups(t *testing.T) {
	m := newNetwork(mustStyles(t), DefaultKeymap())
	m.SetHosts([]hostStatus{
		{Name: "self", Source: "local", Local: true, OK: true},
		{Name: "mini", Source: "configured", DialHost: "mini", OK: true},
		{Name: "scout", Source: "discovered", DialHost: "scout", OK: true},
		{Name: "phone", Source: "mobile", Mobile: true},
	})
	v := m.View(120, 30)

	// Section headings appear in the pinned order.
	iLocal := strings.Index(v, "Local")
	iConf := strings.Index(v, "Configured")
	iDisc := strings.Index(v, "Discovered")
	iMob := strings.Index(v, "Mobile")
	if iLocal < 0 || iConf < 0 || iDisc < 0 || iMob < 0 {
		t.Fatalf("missing section heading(s): local=%d configured=%d discovered=%d mobile=%d\n%s",
			iLocal, iConf, iDisc, iMob, v)
	}
	if !(iLocal < iConf && iConf < iDisc && iDisc < iMob) {
		t.Errorf("section order wrong: local=%d configured=%d discovered=%d mobile=%d",
			iLocal, iConf, iDisc, iMob)
	}

	// Empty-section skip: drop the Mobile row and re-render. The
	// Mobile heading must not appear.
	m.SetHosts([]hostStatus{
		{Name: "self", Source: "local", Local: true, OK: true},
		{Name: "mini", Source: "configured", DialHost: "mini", OK: true},
	})
	v2 := m.View(120, 30)
	if strings.Contains(v2, "Mobile") {
		t.Errorf("Mobile section heading rendered for empty group\n%s", v2)
	}
	if strings.Contains(v2, "Discovered") {
		t.Errorf("Discovered section heading rendered for empty group\n%s", v2)
	}
}

// TestNetwork_DropsInlineLegend — the legend row used to live under
// the header on wide. After the chip vocabulary lands it's
// redundant. Pins that it doesn't render even on a wide layout
// with at least one device row.
func TestNetwork_DropsInlineLegend(t *testing.T) {
	m := newNetwork(mustStyles(t), DefaultKeymap())
	m.SetHosts([]hostStatus{
		{Name: "self", Source: "local", Local: true, OK: true},
	})
	v := m.View(140, 30)
	// The legacy legend's distinguishing word.
	if strings.Contains(v, "ccmuxd reachable") {
		t.Errorf("legend row still rendered\n%s", v)
	}
}

// TestNetwork_HelpBarAdvertisesSetupSSH — the HelpBar must include
// the `s setup ssh` and `i details` hints alongside `enter ssh`.
func TestNetwork_HelpBarAdvertisesSetupSSH(t *testing.T) {
	m := newNetwork(mustStyles(t), DefaultKeymap())
	props := m.HelpBarProps(120)
	var sawSetup, sawDetails, sawSSH bool
	for _, h := range props.Hints {
		switch {
		case h.Key == "s" && strings.Contains(strings.ToLower(h.Label), "setup"):
			sawSetup = true
		case h.Key == "i" && strings.Contains(strings.ToLower(h.Label), "detail"):
			sawDetails = true
		case h.Key == "enter" && strings.Contains(strings.ToLower(h.Label), "ssh"):
			sawSSH = true
		}
	}
	if !sawSetup {
		t.Errorf("HelpBar missing `s setup ssh` hint: %+v", props.Hints)
	}
	if !sawDetails {
		t.Errorf("HelpBar missing `i details` hint: %+v", props.Hints)
	}
	if !sawSSH {
		t.Errorf("HelpBar missing `enter ssh` hint: %+v", props.Hints)
	}
}

// TestNetwork_DetailOverlayRendersFullHostInfo — pressing `i`
// opens the host-detail overlay; pin that its body carries the
// tailnet address, ccmuxd version, source, and last-probe time.
func TestNetwork_DetailOverlayRendersFullHostInfo(t *testing.T) {
	m := newNetwork(mustStyles(t), DefaultKeymap())
	probeAt := time.Date(2026, 5, 27, 14, 32, 17, 0, time.Local)
	m.SetHosts([]hostStatus{
		{
			Name: "mac-mini", Source: "configured",
			Address: "100.64.1.5:7474", DialHost: "mac-mini",
			Version: "v1.0", Sessions: 3, OK: true,
			LastProbe: probeAt,
		},
	})
	m.OpenDetail()
	if !m.DetailOpen() {
		t.Fatal("DetailOpen() = false after OpenDetail()")
	}
	out := m.renderDetailOverlay(120, 40)
	for _, want := range []string{
		"mac-mini",
		"100.64.1.5:7474",
		"v1.0",
		"configured",
		"14:32:17",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("detail overlay missing %q\n--- overlay ---\n%s", want, out)
		}
	}
	m.CloseDetail()
	if m.DetailOpen() {
		t.Fatal("DetailOpen() = true after CloseDetail()")
	}
}

// TestNetwork_NarrowLayout — at phone width the Network screen keeps
// the device rows (T0) but drops the colour legend, the inline action
// hint, and the Selected block's os/address/dial/version detail (T2).
func TestNetwork_NarrowLayout(t *testing.T) {
	m := newNetwork(mustStyles(t), DefaultKeymap())
	m.SetHosts([]hostStatus{
		{Name: "sputnik", Source: "local", Local: true, OK: true, OS: "darwin", Address: "100.1.2.3", Version: "v1.0"},
		{Name: "mir", Source: "configured", OK: true, OS: "linux", Address: "100.4.5.6", DialHost: "mir.tail", Version: "v1.0"},
	})
	out := m.View(50, 40)
	assertNoOverflow(t, out, 50)
	assertPresent(t, out, "sputnik", "mir")
	assertAbsent(t, out, "100.1.2.3", "ccmuxd reachable", "enter: ssh")
}
