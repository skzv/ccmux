package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/project"
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

// TestNewProjectForm_SubmitCarriesSSHFields drives the full form flow
// for a remote host with ssh_port 2222 + user + mosh configured and
// asserts the submit message carries all three. Regression: the
// Projects-tab `n` flow dropped User and Mosh entirely (and SSHPort
// had no field to ride in), so the post-create attach dialed port 22
// as the local user over plain ssh.
func TestNewProjectForm_SubmitCarriesSSHFields(t *testing.T) {
	st := styles.Default()
	hosts := []hostStatus{
		{Name: "sputnik", Local: true, OK: true},
		{
			Name: "mac-mini", OK: true,
			Address:  "100.75.64.20:7474",
			DialHost: "mac-mini",
			User:     "sasha",
			SSHPort:  2222,
			Mosh:     true,
		},
	}
	f := newNewProjectForm(st, hosts, "")

	// Type a name, tab to the device row, → onto the remote, submit.
	f, _ = runMsgs(t, f, keyMsg("p"), keyMsg("r"), keyMsg("o"), keyMsg("j"))
	f, _ = runMsgs(t, f, keyMsg("tab"), keyMsg("right"))
	_, msg := runMsgs(t, f, keyMsg("enter"))
	sub, ok := msg.(newProjectSubmitMsg)
	if !ok {
		t.Fatalf("expected newProjectSubmitMsg, got %T", msg)
	}
	if sub.User != "sasha" {
		t.Errorf("User = %q, want sasha", sub.User)
	}
	if sub.SSHPort != 2222 {
		t.Errorf("SSHPort = %d, want 2222", sub.SSHPort)
	}
	if !sub.Mosh {
		t.Error("Mosh = false, want true")
	}

	// The local pick must stay clean of remote addressing.
	f2 := newNewProjectForm(st, hosts, "")
	f2, _ = runMsgs(t, f2, keyMsg("x"))
	_, msg2 := runMsgs(t, f2, keyMsg("enter"))
	sub2, ok := msg2.(newProjectSubmitMsg)
	if !ok {
		t.Fatalf("expected newProjectSubmitMsg, got %T", msg2)
	}
	if sub2.User != "" || sub2.SSHPort != 0 || sub2.Mosh {
		t.Errorf("local submit carries remote SSH fields: %+v", sub2)
	}
}

// TestRemoteStartedFromProjectSubmit_CarriesSSHFields pins the submit →
// attach-trigger mapping used by createProjectCmd's remote branch. The
// old inline construction populated only SessionName and DialHost.
func TestRemoteStartedFromProjectSubmit_CarriesSSHFields(t *testing.T) {
	cases := []struct {
		name   string
		submit newProjectSubmitMsg
		want   remoteSessionStartedMsg
	}{
		{
			name: "full addressing",
			submit: newProjectSubmitMsg{
				Host: "mac-mini", DialHost: "mac-mini.local",
				User: "sasha", SSHPort: 2222, Mosh: true,
			},
			want: remoteSessionStartedMsg{
				SessionName: "c-proj", DialHost: "mac-mini.local",
				User: "sasha", SSHPort: 2222, Mosh: true,
			},
		},
		{
			name:   "dialhost falls back to display name",
			submit: newProjectSubmitMsg{Host: "mac-mini"},
			want:   remoteSessionStartedMsg{SessionName: "c-proj", DialHost: "mac-mini"},
		},
	}
	for _, tc := range cases {
		if got := remoteStartedFromProjectSubmit(tc.submit, "c-proj"); got != tc.want {
			t.Errorf("%s: remoteStartedFromProjectSubmit = %+v, want %+v", tc.name, got, tc.want)
		}
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

// TestNextAgent — the cycler used by the Projects-screen `a` key. With
// every agent installed, going claude → codex → antigravity → cursor →
// pi → … → claude must roundtrip through all of them. The unknown-current
// case (someone hand-edits the sidecar) lands on the first agent rather
// than crashing.
func TestNextAgent(t *testing.T) {
	var all []agent.ID
	for _, a := range agent.All() {
		all = append(all, a.ID())
	}
	cases := []struct {
		from, to agent.ID
	}{
		{agent.IDClaude, agent.IDCodex},
		{agent.IDCodex, agent.IDAntigravity},
		{agent.IDAntigravity, agent.IDCursor},
		{agent.IDCursor, agent.IDPi},
		{agent.IDPi, agent.IDGrok},
		// The cycle continues through the second-wave agents — the
		// Projects "a" action must reach every supported agent — and
		// wraps from the last one (Kiro) back to Claude.
		{agent.IDGrok, agent.IDOpenCode},
		{agent.IDOpenCode, agent.IDKimi},
		{agent.IDKimi, agent.IDDroid},
		{agent.IDDroid, agent.IDCopilot},
		{agent.IDCopilot, agent.IDQoder},
		{agent.IDQoder, agent.IDKilo},
		{agent.IDKilo, agent.IDHermes},
		{agent.IDHermes, agent.IDAmp},
		{agent.IDAmp, agent.IDKiro},
		{agent.IDKiro, agent.IDMuse},
		{agent.IDMuse, agent.IDGemini},
		{agent.IDGemini, agent.IDClaude},
		// Edge: "" is a project with no sidecar, i.e. Claude; unknown
		// values land on the first agent.
		{"", agent.IDCodex},
		{agent.ID("imaginary"), agent.IDClaude},
	}
	for _, tc := range cases {
		t.Run(string(tc.from), func(t *testing.T) {
			if got, ok := nextAgent(tc.from, all); !ok || got != tc.to {
				t.Errorf("nextAgent(%q) = %q, %v; want %q", tc.from, got, ok, tc.to)
			}
		})
	}
}

// TestNextAgent_InstalledOnly — `a` must only land on agents this
// machine can launch (what the new-session / new-project pickers offer).
// Cycling all registered agents put projects on uninstalled CLIs and the
// next session died with "command not found: grok".
func TestNextAgent_InstalledOnly(t *testing.T) {
	installed := []agent.ID{agent.IDClaude, agent.IDCodex}
	cases := []struct{ from, to agent.ID }{
		{agent.IDClaude, agent.IDCodex},
		{agent.IDCodex, agent.IDClaude},
		// A project on an agent that isn't installed moves to the next
		// installed one after it (Grok wraps around to Claude).
		{agent.IDGrok, agent.IDClaude},
		{agent.IDAntigravity, agent.IDClaude},
	}
	for _, tc := range cases {
		if got, ok := nextAgent(tc.from, installed); !ok || got != tc.to {
			t.Errorf("nextAgent(%q, claude+codex) = %q, %v; want %q", tc.from, got, ok, tc.to)
		}
	}
	if got, ok := nextAgent(agent.IDClaude, []agent.ID{agent.IDClaude}); ok {
		t.Errorf("only Claude installed: nextAgent = %q, true; want no switch", got)
	}
	if got, ok := nextAgent(agent.IDClaude, nil); ok {
		t.Errorf("nothing installed: nextAgent = %q, true; want no switch", got)
	}
}

// TestSwitchAgentCmd_OnlyInstalledAgents drives the real `a` command
// against a PATH holding just claude and codex: from Codex it must go
// back to Claude (not on to Antigravity), and the toast must name the
// agent it switched to.
func TestSwitchAgentCmd_OnlyInstalledAgents(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"claude", "codex"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	dir := t.TempDir()
	if err := project.SetAgent(dir, agent.IDCodex); err != nil {
		t.Fatal(err)
	}
	p := project.Project{Name: "demo", Path: dir, Agent: agent.IDCodex}
	msg := switchAgentCmd(p, agent.Commands{})()
	var switched *projectAgentSwitchedMsg
	var toast string
	collect := func(m tea.Msg) {
		switch v := m.(type) {
		case projectAgentSwitchedMsg:
			switched = &v
		case toastMsg:
			toast = v.Text
		}
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			collect(c())
		}
	} else {
		collect(msg)
	}
	if switched == nil || switched.Agent != agent.IDClaude {
		t.Fatalf("switched = %+v, want Claude (the only other installed agent)", switched)
	}
	if got := project.ReadAgent(dir); got != agent.IDClaude {
		t.Errorf("sidecar = %q, want claude", got)
	}
	if !strings.Contains(toast, "Claude") {
		t.Errorf("toast %q doesn't name the new agent", toast)
	}

	// With only one agent installed there is nothing to switch to: the
	// sidecar stays put and the toast says so.
	if err := os.Remove(filepath.Join(bin, "codex")); err != nil {
		t.Fatal(err)
	}
	p.Agent = agent.IDClaude
	msg = switchAgentCmd(p, agent.Commands{})()
	tm, ok := msg.(toastMsg)
	if !ok || !strings.Contains(tm.Text, "only agent installed") {
		t.Errorf("single-agent switch returned %T %+v, want an 'only agent installed' toast", msg, msg)
	}
	if got := project.ReadAgent(dir); got != agent.IDClaude {
		t.Errorf("sidecar changed to %q with nothing to switch to", got)
	}
}
