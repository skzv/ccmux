package setupwizard

import (
	"testing"

	"github.com/charmbracelet/huh"

	"github.com/skzv/ccmux/internal/agent"
)

// TestDefaultAgentOptions_KeepsCurrentValue — regression: re-running
// the setup wizard interactively reset agents.default. The picker only
// listed detected agents from a fixed set, and huh's Select rewrites a
// bound value that isn't among its options to the first option, so a
// configured grok / muse / opencode (or an agent not on PATH right now)
// silently became claude. The current value must always be an option,
// and binding it must leave it unchanged.
func TestDefaultAgentOptions_KeepsCurrentValue(t *testing.T) {
	choices := []agent.ID{agent.IDClaude, agent.IDCodex}
	for _, current := range []string{"grok", "muse", "opencode", "codex", "claude", "shell"} {
		t.Run(current, func(t *testing.T) {
			opts := defaultAgentOptions(choices, current)
			count := 0
			for _, o := range opts {
				if o.Value == current {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("options %v offer %q %d times, want exactly once", optionValues(opts), current, count)
			}

			v := current
			huh.NewSelect[string]().Options(opts...).Value(&v)
			if v != current {
				t.Errorf("binding %q to the picker rewrote it to %q", current, v)
			}
		})
	}
}

// TestDefaultAgentOptions_Order — detected choices first, the shell
// opt-out last, a not-detected current value in between.
func TestDefaultAgentOptions_Order(t *testing.T) {
	got := optionValues(defaultAgentOptions([]agent.ID{agent.IDClaude, agent.IDCodex}, "grok"))
	want := []string{"claude", "codex", "grok", "shell"}
	if len(got) != len(want) {
		t.Fatalf("options = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("options = %v, want %v", got, want)
		}
	}
}

func optionValues(opts []huh.Option[string]) []string {
	out := make([]string, len(opts))
	for i, o := range opts {
		out[i] = o.Value
	}
	return out
}
