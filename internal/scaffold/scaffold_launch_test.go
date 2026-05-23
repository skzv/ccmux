package scaffold

import (
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
)

// Regression guard for the scaffold-side half of the multi-agent
// bug: StartSession used to call `tmux.New(..., "claude")` regardless
// of opts.Agent, so a user who picked Codex / Antigravity in the
// new-project picker got claude in tmux while their .ccmux/agent
// sidecar correctly said the other thing.
//
// LaunchCmd is the single source of truth for "what does StartSession
// hand to tmux", and these tests pin it.

// TestLaunchCmd_PerAgent — every supported agent ID maps to the
// agent's own LaunchCmd(false). `false` because new projects don't
// have a prior conversation to resume.
func TestLaunchCmd_PerAgent(t *testing.T) {
	for _, a := range agent.All() {
		t.Run(string(a.ID()), func(t *testing.T) {
			got := LaunchCmd(Options{Agent: a.ID()})
			want := a.LaunchCmd(false)
			if got != want {
				t.Errorf("LaunchCmd(Agent=%q) = %q, want %q", a.ID(), got, want)
			}
			if !strings.HasPrefix(got, a.Binary()) {
				t.Errorf("LaunchCmd(Agent=%q) = %q, expected to start with binary %q",
					a.ID(), got, a.Binary())
			}
		})
	}
}

// TestLaunchCmd_NoContinueFlag — new projects must NOT pass
// --continue. agent.Claude.LaunchCmd(false) is exactly "claude"; if a
// regression flipped this to true the agent would look for a
// non-existent transcript on every brand-new project.
func TestLaunchCmd_NoContinueFlag(t *testing.T) {
	for _, a := range agent.All() {
		t.Run(string(a.ID()), func(t *testing.T) {
			got := LaunchCmd(Options{Agent: a.ID()})
			if strings.Contains(got, "--continue") {
				t.Errorf("LaunchCmd(Agent=%q) = %q, must not contain --continue (new project)",
					a.ID(), got)
			}
		})
	}
}

// TestLaunchCmd_EmptyAgentDefaultsToClaude — callers that pass Options
// without Agent set must still produce claude so back-compat holds.
func TestLaunchCmd_EmptyAgentDefaultsToClaude(t *testing.T) {
	got := LaunchCmd(Options{})
	want := agent.Claude{}.LaunchCmd(false)
	if got != want {
		t.Errorf("LaunchCmd(empty Agent) = %q, want %q (claude back-compat)", got, want)
	}
}

func TestLaunchCmd_ConfiguredCommands(t *testing.T) {
	commands := agent.Commands{
		Claude:      "/tmp/claude",
		Codex:       "/tmp/codex",
		Antigravity: "/tmp/agy",
	}
	tests := []struct {
		name string
		id   agent.ID
		want string
	}{
		{name: "claude", id: agent.IDClaude, want: "/tmp/claude"},
		{name: "codex", id: agent.IDCodex, want: "/tmp/codex"},
		{name: "antigravity", id: agent.IDAntigravity, want: "/tmp/agy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LaunchCmd(Options{
				Agent:    tt.id,
				Commands: commands,
			})
			if got != tt.want {
				t.Errorf("configured LaunchCmd = %q, want %q", got, tt.want)
			}
		})
	}
}
