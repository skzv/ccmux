package cmd

import (
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
)

// TestAgentInstallHint_CoversAllShippedAgents — every agent in
// agent.All() must produce a non-empty install hint. Doctor surfaces
// the hint when the binary is missing; if a future agent gets added
// without a hint, this test flags it before users see a blank "install:
// " line.
func TestAgentInstallHint_CoversAllShippedAgents(t *testing.T) {
	for _, a := range agent.All() {
		t.Run(string(a.ID()), func(t *testing.T) {
			hint := agentInstallHint(a.ID())
			if hint == "" {
				t.Fatalf("no install hint for %s — every shipped agent needs one", a.ID())
			}
		})
	}
}

// TestAgentInstallHint_HasActionableCommand — each hint should include
// either an `npm i -g` snippet or a documentation URL. A hint of just
// "go check the docs" would be unhelpful when the user is stuck at
// `ccmux doctor` output.
func TestAgentInstallHint_HasActionableCommand(t *testing.T) {
	for _, a := range agent.All() {
		t.Run(string(a.ID()), func(t *testing.T) {
			hint := agentInstallHint(a.ID())
			if !strings.Contains(hint, "npm i -g") && !strings.Contains(hint, "http") {
				t.Errorf("%s install hint lacks a runnable command or URL: %q",
					a.ID(), hint)
			}
		})
	}
}

// TestAgentInstallHint_UnknownReturnsEmpty — the function falls back
// to "" for ids not in the switch. This is just a defensive code-path
// pin so a future ParseID-bypassing caller (shouldn't exist) doesn't
// crash on a typo'd id.
func TestAgentInstallHint_UnknownReturnsEmpty(t *testing.T) {
	if got := agentInstallHint(agent.ID("imaginary")); got != "" {
		t.Errorf("agentInstallHint(unknown) = %q, want empty", got)
	}
}
