package setupwizard

import (
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
)

// TestNpmInstallFor_CoversAllShippedAgents — same contract as
// doctor's agentInstallHint, in the wizard. If a future agent is
// added without a wizard install command, the setup flow would print
// an empty hint and confuse the user.
func TestNpmInstallFor_CoversAllShippedAgents(t *testing.T) {
	for _, a := range agent.All() {
		t.Run(string(a.ID()), func(t *testing.T) {
			got := npmInstallFor(a.ID())
			if got == "" {
				t.Fatalf("no npm install hint for %s — every shipped agent needs one", a.ID())
			}
			if !strings.HasPrefix(got, "npm i -g") {
				t.Errorf("hint should start with `npm i -g`: %q", got)
			}
		})
	}
}

// TestNpmInstallFor_NamesActualPackage — the published npm package
// names are load-bearing; if Anthropic / OpenAI / Google rename their
// CLI packages this test will need updating, but at least the failure
// is obvious instead of silently shipping wrong copy.
func TestNpmInstallFor_NamesActualPackage(t *testing.T) {
	cases := map[agent.ID]string{
		agent.IDClaude: "@anthropic-ai/claude-code",
		agent.IDCodex:  "@openai/codex",
		agent.IDGemini: "@google/gemini-cli",
	}
	for id, want := range cases {
		t.Run(string(id), func(t *testing.T) {
			if got := npmInstallFor(id); !strings.Contains(got, want) {
				t.Errorf("missing package %q in hint: %q", want, got)
			}
		})
	}
}
