package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// These tests guard the Sessions-tab agent picker against the
// "regression to bare shell" bug: prior to the picker, pressing `n`
// on the Sessions tab always landed the user in a $SHELL pane even
// when claude / antigravity were installed. Each test pins one of
// the three things that have to be true for the agent path to work:
// the picker is seeded with a real agent at row 0, the picker's
// selection rides Enter into newBareSessionSubmitMsg.Agent, and the
// resolved launch command for a given agent is that agent's
// LaunchCmd — not a hardcoded "claude" string.

// TestLaunchCmdForBareSession_KnownAgents — every supported agent's
// id resolves to the agent's own LaunchCmd(false). A regression that
// re-hardcoded "claude" here would fail this test.
func TestLaunchCmdForBareSession_KnownAgents(t *testing.T) {
	for _, a := range agent.All() {
		t.Run(string(a.ID()), func(t *testing.T) {
			got := launchCmdForBareSession(a.ID())
			want := a.LaunchCmd(false)
			if got != want {
				t.Errorf("launchCmdForBareSession(%q) = %q, want %q (the agent's own LaunchCmd)",
					a.ID(), got, want)
			}
			// Sanity: the command must start with the agent's binary
			// — otherwise the picker fires the wrong process.
			if !strings.HasPrefix(got, a.Binary()) {
				t.Errorf("launchCmdForBareSession(%q) = %q, expected to start with binary %q",
					a.ID(), got, a.Binary())
			}
		})
	}
}

// TestLaunchCmdForBareSession_EmptyFallsBackToShell — selecting the
// "shell" sentinel row (ID == "") must NOT silently launch an agent.
// The fallback is $SHELL (or /bin/sh) so a user who picked "shell"
// in the form gets exactly that.
func TestLaunchCmdForBareSession_EmptyFallsBackToShell(t *testing.T) {
	t.Setenv("SHELL", "/usr/bin/zsh")
	got := launchCmdForBareSession("")
	if got != "/usr/bin/zsh" {
		t.Errorf("launchCmdForBareSession(\"\") = %q, want /usr/bin/zsh (from $SHELL)", got)
	}
	// Confirm no agent binary leaked in — the regression would have
	// `claude` here.
	for _, a := range agent.All() {
		if strings.Contains(got, a.Binary()) {
			t.Errorf("shell fallback contains agent binary %q: %q", a.Binary(), got)
		}
	}
}

// TestLaunchCmdForBareSession_UnknownFallsBackToShell — a hand-edited
// config value or stale wire input that ParseID rejects must NOT
// panic and must NOT default to claude. Fallback is $SHELL.
func TestLaunchCmdForBareSession_UnknownFallsBackToShell(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh")
	got := launchCmdForBareSession(agent.ID("gpt-5-pro"))
	if got != "/bin/zsh" {
		t.Errorf("launchCmdForBareSession(unknown) = %q, want /bin/zsh (shell fallback)", got)
	}
}

// TestNewSessionForm_DefaultRowIsAgent — the picker's default row
// must land on an agent, not on the "shell" sentinel. That's the
// whole point of the picker: pressing `n` → Enter should put the
// user in their default agent. A default-row drift would re-trigger
// the original "shell only" regression silently.
func TestNewSessionForm_DefaultRowIsAgent(t *testing.T) {
	st := styles.Default()
	form := newNewSessionForm(st, nil, "", "")
	cur := form.currentAgent()
	if cur.ID == "" {
		t.Errorf("default agent row = shell sentinel (ID empty), want an agent — picker would land users in $SHELL by default")
	}
}

func TestNewSessionForm_UsesConfiguredAgentCommand(t *testing.T) {
	dir := t.TempDir()
	codexPath := filepath.Join(dir, "codex")
	if err := os.WriteFile(codexPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())

	form := newNewSessionForm(styles.Default(), nil, "", "codex", agent.Commands{Codex: codexPath})
	cur := form.currentAgent()
	if cur.ID != agent.IDCodex {
		t.Fatalf("currentAgent = %q, want codex; choices=%v", cur.ID, form.agents)
	}
}

// TestNewSessionForm_SubmitCarriesAgent — picking an agent in the
// form and pressing Enter must produce a newBareSessionSubmitMsg
// whose Agent field matches the picker's row. Without this, the
// daemon would launch sessions.default_agent on every spawn,
// silently overriding the user's per-session choice.
func TestNewSessionForm_SubmitCarriesAgent(t *testing.T) {
	st := styles.Default()
	form := newNewSessionForm(st, nil, "", "")
	// Force a known agent slate so the test doesn't depend on what's
	// actually installed on the test host.
	form.agents = []sessionAgentChoice{
		{ID: agent.IDClaude, Label: "Claude Code"},
		{ID: agent.IDCodex, Label: "Codex"},
		{ID: agent.IDAntigravity, Label: "Antigravity CLI"},
		{ID: agent.IDCursor, Label: "Cursor"},
		{ID: "", Label: "shell (no agent)"},
	}
	form.agentIdx = 0
	form.focus = 3 // agent row

	// → twice → antigravity (index 2).
	form, _ = form.Update(keyMsg("right"))
	form, _ = form.Update(keyMsg("right"))
	if form.agentIdx != 2 {
		t.Fatalf("agentIdx after 2 rights = %d, want 2 (antigravity)", form.agentIdx)
	}

	_, cmd := form.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("enter produced no cmd")
	}
	msg := cmd()
	sub, ok := msg.(newBareSessionSubmitMsg)
	if !ok {
		t.Fatalf("msg = %T, want newBareSessionSubmitMsg", msg)
	}
	if sub.Agent != agent.IDAntigravity {
		t.Errorf("submit.Agent = %q, want antigravity", sub.Agent)
	}
}

// TestNewSessionForm_ShellRowSubmitsEmptyAgent — picking the "shell"
// sentinel row must produce Agent == "" so the daemon falls through
// to its $SHELL path. A submit that re-emitted "shell" as the ID
// would crash agent.ParseID downstream.
func TestNewSessionForm_ShellRowSubmitsEmptyAgent(t *testing.T) {
	st := styles.Default()
	form := newNewSessionForm(st, nil, "", "")
	form.agents = []sessionAgentChoice{
		{ID: agent.IDClaude, Label: "Claude Code"},
		{ID: "", Label: "shell (no agent)"},
	}
	form.agentIdx = 1 // shell sentinel

	_, cmd := form.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("enter produced no cmd")
	}
	sub, ok := cmd().(newBareSessionSubmitMsg)
	if !ok {
		t.Fatalf("msg type = %T, want newBareSessionSubmitMsg", cmd())
	}
	if sub.Agent != "" {
		t.Errorf("submit.Agent = %q, want empty (shell sentinel)", sub.Agent)
	}
}

// TestIndexOfDefaultAgent — sessions.default_agent must steer the
// picker's initial row. Empty / unknown values fall back to row 0
// (the first installed agent); the literal "shell" lands on the
// sentinel.
func TestIndexOfDefaultAgent(t *testing.T) {
	agents := []sessionAgentChoice{
		{ID: agent.IDClaude, Label: "Claude Code"},
		{ID: agent.IDCodex, Label: "Codex"},
		{ID: agent.IDAntigravity, Label: "Antigravity CLI"},
		{ID: agent.IDCursor, Label: "Cursor"},
		{ID: "", Label: "shell (no agent)"},
	}
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},                // empty → first agent
		{"claude", 0},          //
		{"codex", 1},           //
		{"antigravity", 2},     //
		{"gemini", 2},          // back-compat alias still routes to antigravity
		{"cursor", 3},          //
		{"  Codex  ", 1},       // case-insensitive + trimmed via ParseID
		{"shell", 4},           // explicit no-agent
		{"SHELL", 4},           //
		{"imaginary-llm-9", 0}, // unknown → first agent
	}
	for _, tc := range cases {
		if got := indexOfDefaultAgent(agents, tc.in); got != tc.want {
			t.Errorf("indexOfDefaultAgent(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
