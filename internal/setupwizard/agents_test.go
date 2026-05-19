package setupwizard

import (
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
)

// TestInstallHintFor_CoversAllShippedAgents — same contract as
// doctor's agentInstallHint, in the wizard. If a future agent is
// added without a wizard install command, the setup flow would print
// an empty hint and confuse the user.
func TestInstallHintFor_CoversAllShippedAgents(t *testing.T) {
	for _, a := range agent.All() {
		t.Run(string(a.ID()), func(t *testing.T) {
			got := installHintFor(a.ID())
			if got == "" {
				t.Fatalf("no install hint for %s — every shipped agent needs one", a.ID())
			}
		})
	}
}

// TestInstallHintFor_NamesActualPackage — the published install
// commands are load-bearing; if Anthropic / OpenAI / Google rename
// their package or installer this test will need updating, but at
// least the failure is obvious instead of silently shipping wrong
// copy.
func TestInstallHintFor_NamesActualPackage(t *testing.T) {
	cases := map[agent.ID]string{
		agent.IDClaude:      "@anthropic-ai/claude-code",
		agent.IDCodex:       "@openai/codex",
		agent.IDAntigravity: "antigravity.google/cli/install.sh",
	}
	for id, want := range cases {
		t.Run(string(id), func(t *testing.T) {
			if got := installHintFor(id); !strings.Contains(got, want) {
				t.Errorf("missing identifier %q in hint: %q", want, got)
			}
		})
	}
}
