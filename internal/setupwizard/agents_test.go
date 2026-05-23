package setupwizard

import (
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/config"
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

func TestDefaultAgentCommandSelection(t *testing.T) {
	tests := []struct {
		name       string
		current    string
		candidates []string
		want       string
		wantPrompt bool
	}{
		{
			name:       "configured command wins",
			current:    "  /Users/me/.nvm/versions/node/v24/bin/claude  ",
			candidates: []string{"/opt/homebrew/bin/claude"},
			want:       "/Users/me/.nvm/versions/node/v24/bin/claude",
		},
		{
			name: "no candidates skips",
		},
		{
			name:       "single candidate is selected without prompt",
			candidates: []string{"/opt/homebrew/bin/claude"},
			want:       "/opt/homebrew/bin/claude",
		},
		{
			name:       "multiple candidates prompt with path first default",
			candidates: []string{"/Users/me/.nvm/bin/claude", "/opt/homebrew/bin/claude"},
			want:       "/Users/me/.nvm/bin/claude",
			wantPrompt: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotPrompt := defaultAgentCommandSelection(tt.current, tt.candidates)
			if got != tt.want {
				t.Fatalf("selection = %q, want %q", got, tt.want)
			}
			if gotPrompt != tt.wantPrompt {
				t.Fatalf("shouldPrompt = %v, want %v", gotPrompt, tt.wantPrompt)
			}
		})
	}
}

func TestConfiguredAgentCommand(t *testing.T) {
	cfg := config.Config{}
	cfg.Agents.Claude.Command = "  /tmp/claude  "
	cfg.Agents.Codex.Command = "  /tmp/codex  "
	cfg.Agents.Antigravity.Command = "  /tmp/agy  "

	cases := map[agent.ID]string{
		agent.IDClaude:      "/tmp/claude",
		agent.IDCodex:       "/tmp/codex",
		agent.IDAntigravity: "/tmp/agy",
	}
	for id, want := range cases {
		t.Run(string(id), func(t *testing.T) {
			if got := configuredAgentCommand(cfg, id); got != want {
				t.Fatalf("configuredAgentCommand(%s) = %q, want %q", id, got, want)
			}
		})
	}
}
