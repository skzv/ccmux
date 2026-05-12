package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/tui/styles"
)

// keyMsg is the test helper for synthesizing a Bubble Tea KeyMsg from a
// string the way the form's Update() reads them via msg.String().
// Builds a Runes-backed KeyMsg for printable chars, a typed KeyType
// for named keys.
func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// runMsgs drives the form through a sequence of messages, returning
// the final state and the last non-nil command output (executed once
// to harvest the resulting tea.Msg, e.g. submit).
func runMsgs(t *testing.T, m newProjectFormModel, msgs ...tea.Msg) (newProjectFormModel, tea.Msg) {
	t.Helper()
	var lastCmd tea.Cmd
	for _, msg := range msgs {
		m, lastCmd = m.Update(msg)
	}
	if lastCmd == nil {
		return m, nil
	}
	return m, lastCmd()
}

// TestNewProjectForm_LocalOnly_Submit covers the simplest case: no
// remote hosts in the App's slice, just the local entry. The submit
// message should carry Host="local" and empty Address/DialHost.
func TestNewProjectForm_LocalOnly_Submit(t *testing.T) {
	st := styles.Default()
	f := newNewProjectForm(st, nil) // no hosts
	if got := len(f.hosts); got != 1 || f.hosts[0].Label != "local" {
		t.Fatalf("hosts = %+v, want [{local true}]", f.hosts)
	}

	_, msg := runMsgs(t, f, keyMsg("a"), keyMsg("l"), keyMsg("p"), keyMsg("h"), keyMsg("a"), keyMsg("enter"))
	sub, ok := msg.(newProjectSubmitMsg)
	if !ok {
		t.Fatalf("expected newProjectSubmitMsg, got %T", msg)
	}
	if sub.Name != "alpha" {
		t.Errorf("Name = %q, want alpha", sub.Name)
	}
	if sub.Host != "local" {
		t.Errorf("Host = %q, want local", sub.Host)
	}
	if sub.Address != "" || sub.DialHost != "" {
		t.Errorf("local submit shouldn't carry address/dialhost: %+v", sub)
	}
}

// TestNewProjectForm_NameRequired — pressing enter on an empty name
// should NOT emit a submit msg; it should set the form's err and stay
// open. (Regression: the older form returned a nil cmd here but didn't
// set err; this test pins the error-feedback path.)
func TestNewProjectForm_NameRequired(t *testing.T) {
	st := styles.Default()
	f := newNewProjectForm(st, nil)
	f, msg := runMsgs(t, f, keyMsg("enter"))
	if msg != nil {
		t.Fatalf("empty enter should not emit a msg, got %T", msg)
	}
	if f.err == "" {
		t.Error("form should record an err on empty enter")
	}
}

// TestNewProjectForm_HostPickerCycle — with two hosts available the
// picker should cycle on right/left, and submit picks the current one.
// We also verify that Tab moves focus from name → desc → host (3
// stops) so the picker is reachable without the mouse.
func TestNewProjectForm_HostPickerCycle(t *testing.T) {
	st := styles.Default()
	hosts := []hostStatus{
		{Name: "sputnik", Local: true, OK: true},
		{Name: "mac-mini", OK: true, Address: "100.75.64.20:7474", DialHost: "mac-mini"},
		{Name: "raspi", OK: true, Address: "100.75.64.21:7474", DialHost: "raspi"},
	}
	f := newNewProjectForm(st, hosts)
	if got := len(f.hosts); got != 3 {
		t.Fatalf("hosts = %d, want 3 (local + 2 remotes): %+v", got, f.hosts)
	}
	if f.hosts[0].Label != "local" || f.hosts[1].Label != "mac-mini" || f.hosts[2].Label != "raspi" {
		t.Errorf("host order = [%s %s %s], want [local mac-mini raspi]",
			f.hosts[0].Label, f.hosts[1].Label, f.hosts[2].Label)
	}

	// Type a name, then tab twice to land on the host picker.
	f, _ = runMsgs(t, f, keyMsg("a"), keyMsg("l"), keyMsg("p"), keyMsg("h"), keyMsg("a"))
	f, _ = runMsgs(t, f, keyMsg("tab"), keyMsg("tab"))
	if f.focus != 2 {
		t.Fatalf("focus = %d after 2 tabs, want 2 (host row)", f.focus)
	}

	// → twice to land on raspi (local → mac-mini → raspi).
	f, _ = runMsgs(t, f, keyMsg("right"), keyMsg("right"))
	if f.hostIdx != 2 {
		t.Errorf("hostIdx = %d after 2 rights, want 2", f.hostIdx)
	}

	// ← once to back up to mac-mini.
	f, _ = runMsgs(t, f, keyMsg("left"))
	if f.hostIdx != 1 {
		t.Errorf("hostIdx = %d after 1 left, want 1", f.hostIdx)
	}

	// Submit and confirm the chosen host's addressing rides along.
	_, msg := runMsgs(t, f, keyMsg("enter"))
	sub, ok := msg.(newProjectSubmitMsg)
	if !ok {
		t.Fatalf("expected newProjectSubmitMsg, got %T", msg)
	}
	if sub.Host != "mac-mini" {
		t.Errorf("Host = %q, want mac-mini", sub.Host)
	}
	if sub.Address != "100.75.64.20:7474" {
		t.Errorf("Address = %q, want 100.75.64.20:7474", sub.Address)
	}
	if sub.DialHost != "mac-mini" {
		t.Errorf("DialHost = %q, want mac-mini", sub.DialHost)
	}
}

// TestNewProjectForm_HostPickerWraps — going left from the first entry
// should land on the last; right from the last lands on first.
// Without the wrap the picker would feel busted on small device lists.
func TestNewProjectForm_HostPickerWraps(t *testing.T) {
	st := styles.Default()
	hosts := []hostStatus{
		{Name: "sputnik", Local: true, OK: true},
		{Name: "mac-mini", OK: true, Address: "x:7474", DialHost: "mac-mini"},
	}
	f := newNewProjectForm(st, hosts)
	f, _ = runMsgs(t, f, keyMsg("tab"), keyMsg("tab")) // → host row

	// Wrap backwards from local → mac-mini (last entry).
	f, _ = runMsgs(t, f, keyMsg("left"))
	if f.hostIdx != 1 {
		t.Errorf("wrap left: hostIdx = %d, want 1", f.hostIdx)
	}
	// Wrap forward back to local.
	f, _ = runMsgs(t, f, keyMsg("right"))
	if f.hostIdx != 0 {
		t.Errorf("wrap right: hostIdx = %d, want 0", f.hostIdx)
	}
}

// TestNewProjectForm_DropsUnreachablePeers — a discovered mobile peer
// (Moshi-only iPhone) and a NeedsInstall Mac without ccmuxd both lack
// a working daemon to scaffold against. The picker must skip them.
func TestNewProjectForm_DropsUnreachablePeers(t *testing.T) {
	st := styles.Default()
	hosts := []hostStatus{
		{Name: "sputnik", Local: true, OK: true},
		{Name: "iphone", Mobile: true, OK: true},
		{Name: "old-laptop", NeedsInstall: true, OK: false},
		{Name: "mac-mini", OK: true, Address: "x:7474", DialHost: "mac-mini"},
	}
	f := newNewProjectForm(st, hosts)
	if got := len(f.hosts); got != 2 {
		t.Fatalf("hosts = %d, want 2 (local + mac-mini): %+v", got, f.hosts)
	}
	if f.hosts[1].Label != "mac-mini" {
		t.Errorf("second host = %q, want mac-mini", f.hosts[1].Label)
	}
}

// TestNewProjectForm_Cancel — esc emits a newProjectCancelMsg.
func TestNewProjectForm_Cancel(t *testing.T) {
	st := styles.Default()
	f := newNewProjectForm(st, nil)
	_, msg := runMsgs(t, f, keyMsg("esc"))
	if _, ok := msg.(newProjectCancelMsg); !ok {
		t.Errorf("esc emitted %T, want newProjectCancelMsg", msg)
	}
}
