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
// LaunchCmd is now the single source of truth for "what does
// StartSession hand to tmux", and these tests pin it.

// TestLaunchCmd_PerAgent — every supported agent ID maps to the
// agent's own LaunchCmd(false). `false` because new scaffolds don't
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

// TestLaunchCmd_NoContinueFlag — fresh scaffolds must NOT pass
// --continue. agent.Claude.LaunchCmd(false) is exactly "claude"; if a
// regression flipped this to true the agent would look for a
// non-existent transcript on every brand-new project.
func TestLaunchCmd_NoContinueFlag(t *testing.T) {
	for _, a := range agent.All() {
		t.Run(string(a.ID()), func(t *testing.T) {
			got := LaunchCmd(Options{Agent: a.ID()})
			if strings.Contains(got, "--continue") {
				t.Errorf("LaunchCmd(Agent=%q) = %q, must not contain --continue (fresh scaffold)",
					a.ID(), got)
			}
		})
	}
}

// TestLaunchCmd_EmptyAgentDefaultsToClaude — pre-multi-agent callers
// (every existing site before the picker landed) pass Options without
// Agent set. That must still produce claude so back-compat holds.
func TestLaunchCmd_EmptyAgentDefaultsToClaude(t *testing.T) {
	got := LaunchCmd(Options{})
	want := agent.Claude{}.LaunchCmd(false)
	if got != want {
		t.Errorf("LaunchCmd(empty Agent) = %q, want %q (claude back-compat)", got, want)
	}
}

// TestInitialPrompt_DefersToAgentWhenNoConfigOverride — without a
// user override in cfg.Scaffold.InitialPrompt, the initial prompt
// must be the agent's own (Claude asks for /init, Antigravity asks
// for AGENTS.md, …). A regression that re-pinned the Claude default
// for everyone would silently send the wrong bootstrap message to
// non-Claude agents.
func TestInitialPrompt_DefersToAgentWhenNoConfigOverride(t *testing.T) {
	hermeticHome(t) // no real config.toml on disk
	for _, a := range agent.All() {
		t.Run(string(a.ID()), func(t *testing.T) {
			opts := Options{Name: "proj", Description: "describe", Agent: a.ID()}
			got := InitialPrompt(opts)
			want := a.InitialPrompt("proj", "describe")
			if got != want {
				t.Errorf("InitialPrompt(Agent=%q) diverged from agent's own prompt:\ngot:  %q\nwant: %q",
					a.ID(), got, want)
			}
		})
	}
}
