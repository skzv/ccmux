package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/agent"
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
	f := newNewProjectForm(st, nil, "") // no hosts
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
	f := newNewProjectForm(st, nil, "")
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
// We also verify that Tab moves focus from name → host → agent so the
// picker is reachable without the mouse.
func TestNewProjectForm_HostPickerCycle(t *testing.T) {
	st := styles.Default()
	hosts := []hostStatus{
		{Name: "sputnik", Local: true, OK: true},
		{Name: "mac-mini", OK: true, Address: "100.75.64.20:7474", DialHost: "mac-mini"},
		{Name: "raspi", OK: true, Address: "100.75.64.21:7474", DialHost: "raspi"},
	}
	f := newNewProjectForm(st, hosts, "")
	if got := len(f.hosts); got != 3 {
		t.Fatalf("hosts = %d, want 3 (local + 2 remotes): %+v", got, f.hosts)
	}
	if f.hosts[0].Label != "local" || f.hosts[1].Label != "mac-mini" || f.hosts[2].Label != "raspi" {
		t.Errorf("host order = [%s %s %s], want [local mac-mini raspi]",
			f.hosts[0].Label, f.hosts[1].Label, f.hosts[2].Label)
	}

	// Type a name, then tab once to land on the host picker.
	f, _ = runMsgs(t, f, keyMsg("a"), keyMsg("l"), keyMsg("p"), keyMsg("h"), keyMsg("a"))
	f, _ = runMsgs(t, f, keyMsg("tab"))
	if f.focus != 1 {
		t.Fatalf("focus = %d after 1 tab, want 1 (host row)", f.focus)
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
	f := newNewProjectForm(st, hosts, "")
	f, _ = runMsgs(t, f, keyMsg("tab")) // → host row

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
	f := newNewProjectForm(st, hosts, "")
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
	f := newNewProjectForm(st, nil, "")
	_, msg := runMsgs(t, f, keyMsg("esc"))
	if _, ok := msg.(newProjectCancelMsg); !ok {
		t.Errorf("esc emitted %T, want newProjectCancelMsg", msg)
	}
}

// TestNewProjectForm_HasAgentRow — the form's third row is the agent
// picker. focus cycling must hit 3 stops (name → host → agent) and the
// form must initialize with at least one agent so submit is always
// reachable. This pins that contract.
func TestNewProjectForm_HasAgentRow(t *testing.T) {
	st := styles.Default()
	f := newNewProjectForm(st, nil, "")
	if len(f.agents) == 0 {
		t.Fatal("form should seed agents from agent.All() when nothing is installed")
	}
	// Initial focus on the name input.
	if f.focus != 0 {
		t.Fatalf("initial focus = %d, want 0 (name)", f.focus)
	}
	// 2 tabs lands on the agent row.
	f, _ = runMsgs(t, f, keyMsg("tab"), keyMsg("tab"))
	if f.focus != 2 {
		t.Errorf("after 2 tabs focus = %d, want 2 (agent row)", f.focus)
	}
	// 3rd tab wraps back to name.
	f, _ = runMsgs(t, f, keyMsg("tab"))
	if f.focus != 0 {
		t.Errorf("after 3 tabs focus = %d, want 0 (wrap to name)", f.focus)
	}
}

// TestNewProjectForm_AgentPickerCycle — when the agent row has focus,
// ←/→ cycles through the registered agents. We force the form's
// agents slice to the canonical list so this test isn't affected by
// what's actually installed on the dev machine.
func TestNewProjectForm_AgentPickerCycle(t *testing.T) {
	st := styles.Default()
	f := newNewProjectForm(st, nil, "")
	// Pin the agents list deterministically.
	f.agents = []agent.Agent{agent.Claude{}, agent.Codex{}, agent.Antigravity{}, agent.Cursor{}}
	f.agentIdx = 0

	// Tab to agent row.
	f, _ = runMsgs(t, f, keyMsg("tab"), keyMsg("tab"))
	if f.focus != 2 {
		t.Fatalf("focus = %d, want 2 (agent row)", f.focus)
	}

	// → twice → antigravity (index 2).
	f, _ = runMsgs(t, f, keyMsg("right"), keyMsg("right"))
	if f.agentIdx != 2 {
		t.Errorf("after 2 rights agentIdx = %d, want 2 (antigravity)", f.agentIdx)
	}

	// → once more lands on cursor.
	f, _ = runMsgs(t, f, keyMsg("right"))
	if f.agentIdx != 3 {
		t.Errorf("after third right agentIdx = %d, want 3 (cursor)", f.agentIdx)
	}

	// → once more wraps to claude.
	f, _ = runMsgs(t, f, keyMsg("right"))
	if f.agentIdx != 0 {
		t.Errorf("after wrap-right agentIdx = %d, want 0 (claude)", f.agentIdx)
	}

	// ← from claude wraps to cursor.
	f, _ = runMsgs(t, f, keyMsg("left"))
	if f.agentIdx != 3 {
		t.Errorf("wrap-left agentIdx = %d, want 3 (cursor)", f.agentIdx)
	}
}

// TestNewProjectForm_SubmitCarriesAgent — the picker's selection must
// land in newProjectSubmitMsg.Agent so the downstream createProjectCmd
// hands the right id to scaffold.StartSession (local) or to
// daemon.NewProjectRequest (remote). Without this every project would
// quietly land on the first-listed agent regardless of what the user
// picked.
func TestNewProjectForm_SubmitCarriesAgent(t *testing.T) {
	st := styles.Default()
	f := newNewProjectForm(st, nil, "")
	f.agents = []agent.Agent{agent.Claude{}, agent.Codex{}, agent.Antigravity{}, agent.Cursor{}}

	// Type a name, tab to agent row, → twice to land on antigravity.
	f, _ = runMsgs(t, f, keyMsg("p"), keyMsg("r"), keyMsg("o"), keyMsg("j"))
	f, _ = runMsgs(t, f, keyMsg("tab"), keyMsg("tab"))
	f, _ = runMsgs(t, f, keyMsg("right"), keyMsg("right"))

	_, msg := runMsgs(t, f, keyMsg("enter"))
	sub, ok := msg.(newProjectSubmitMsg)
	if !ok {
		t.Fatalf("expected newProjectSubmitMsg, got %T", msg)
	}
	if sub.Agent != agent.IDAntigravity {
		t.Errorf("submit.Agent = %q, want antigravity", sub.Agent)
	}
}

// TestNewProjectForm_PickerRowsDontConsumeTypedChars — typing into the
// agent / host rows must not corrupt the picker state or trigger
// textinput events. Without this guard a stray keystroke could silently
// rewrite the focused textinput off-screen.
func TestNewProjectForm_PickerRowsDontConsumeTypedChars(t *testing.T) {
	st := styles.Default()
	f := newNewProjectForm(st, nil, "")
	f.agents = []agent.Agent{agent.Claude{}, agent.Codex{}}

	// Tab to agent row, then type "x" — should be ignored.
	f, _ = runMsgs(t, f, keyMsg("tab"), keyMsg("tab"))
	beforeIdx := f.agentIdx
	f, _ = runMsgs(t, f, keyMsg("x"))
	if f.agentIdx != beforeIdx {
		t.Errorf("typing on agent row changed agentIdx: %d → %d", beforeIdx, f.agentIdx)
	}
}

// TestNextAgent — the cycler used by the Projects-screen `a` key.
// Going claude → codex → antigravity → cursor → pi → claude must
// roundtrip through every installed agent. The unknown-current case
// (someone hand-edits the sidecar) defaults to the first registered
// agent rather than crashing.
func TestNextAgent(t *testing.T) {
	cases := []struct {
		from, to agent.ID
	}{
		{agent.IDClaude, agent.IDCodex},
		{agent.IDCodex, agent.IDAntigravity},
		{agent.IDAntigravity, agent.IDCursor},
		{agent.IDCursor, agent.IDPi},
		{agent.IDPi, agent.IDClaude},
		// Edge: empty / unknown values land on the first agent.
		{"", agent.IDClaude},
		{agent.ID("imaginary"), agent.IDClaude},
	}
	for _, tc := range cases {
		t.Run(string(tc.from), func(t *testing.T) {
			if got := nextAgent(tc.from); got != tc.to {
				t.Errorf("nextAgent(%q) = %q, want %q", tc.from, got, tc.to)
			}
		})
	}
}
