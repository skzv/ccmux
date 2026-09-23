package agent

import (
	"testing"

	"github.com/skzv/ccmux/internal/agentdetect"
)

// TestEveryAgentHasDetectionRules — CLAUDE.md: adding an agent means
// adding a rule file under internal/agentdetect/rules/. Without one the
// agent silently falls back to the time-only heuristic.
func TestEveryAgentHasDetectionRules(t *testing.T) {
	for _, a := range All() {
		if len(agentdetect.RulesFor(agentdetect.ID(a.ID()))) == 0 {
			t.Errorf("agent %q has no rules in internal/agentdetect/rules/%s.toml", a.ID(), a.ID())
		}
	}
}
