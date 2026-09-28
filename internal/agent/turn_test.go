package agent

import "testing"

// TestReadTurn pins which agents report what: Claude keeps its input
// apart from its output (and says when it's busy in its status line),
// an agent with a body working rule is busy while that rule matches,
// and an agent with neither gives the daemon nothing beyond its state.
func TestReadTurn(t *testing.T) {
	for _, tc := range []struct {
		name            string
		a               Agent
		pane            string
		busy, separated bool
		hasInput        bool
	}{
		{"claude working", Claude{}, readPaneFixture(t, "claude_v2_working.txt"), true, true, true},
		{"claude waiting", Claude{}, readPaneFixture(t, "claude_v2_idle.txt"), false, true, true},
		{"claude crashed to a shell", Claude{}, readPaneFixture(t, "claude_crashed_shell.txt"), false, true, false},
		{"opencode working footer", OpenCode{}, readPaneFixture(t, "opencode_networking_working.txt"), true, false, false},
		{"opencode waiting", OpenCode{}, readPaneFixture(t, "opencode_networking_idle.txt"), false, false, false},
		{"codex has neither", Codex{}, "Working (5s • esc to interrupt)\n› ", false, false, false},
	} {
		got := ReadTurn(tc.a, tc.pane)
		if got.Busy != tc.busy || got.Separated != tc.separated || got.HasInput != tc.hasInput {
			t.Errorf("%s: %+v, want busy=%v separated=%v hasInput=%v", tc.name, got, tc.busy, tc.separated, tc.hasInput)
		}
	}
}

// TestHasBodyWorkingRule — only rules that read the body count: every
// agent's title spinner rule doesn't make it "busy" by body.
func TestHasBodyWorkingRule(t *testing.T) {
	for id, want := range map[ID]bool{IDOpenCode: true, IDPi: true, IDClaude: false, IDCodex: false} {
		if got := hasBodyWorkingRule(id); got != want {
			t.Errorf("hasBodyWorkingRule(%s) = %v, want %v", id, got, want)
		}
	}
}
