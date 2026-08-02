package agent

import (
	"testing"
	"time"
)

// TestEngineClassify_CodexTitleSpinner — proves Phase 3's routing
// is alive on a non-claude agent: a braille spinner in the OSC title
// makes codex report active, even when the body would otherwise idle
// out via the legacy fallback.
func TestEngineClassify_CodexTitleSpinner(t *testing.T) {
	long := time.Now().Add(-1 * time.Hour) // legacy fallback would say needs_input
	got := Codex{}.ClassifyWithTitle("any body", "⠙ working", long, 3*time.Second)
	if got != StateActive {
		t.Errorf("codex with title spinner = %v, want active", got)
	}
}

// TestEngineClassify_CodexLegacyFallback — proves the safety net:
// when the engine has nothing to say (no title, no matching body
// shape), the legacy time-based heuristic still answers. This is
// what keeps the existing TestCodex_Classify_IdleHeuristic green.
func TestEngineClassify_CodexLegacyFallback(t *testing.T) {
	long := time.Now().Add(-1 * time.Hour)
	got := Codex{}.ClassifyWithTitle("recent body that matches nothing", "", long, 3*time.Second)
	if got != StateNeedsInput {
		t.Errorf("legacy fallback should say needs_input on long-quiet pane, got %v", got)
	}
}

// TestEngineClassify_AllNonClaudeAgentsTitleAware — Phase 3 makes
// every non-claude agent title-aware. Pin so a future refactor can't
// quietly regress one of them. (Claude's already covered by the
// Phase 1 test.)
func TestEngineClassify_AllNonClaudeAgentsTitleAware(t *testing.T) {
	for _, a := range []TitleAwareAgent{Codex{}, Cursor{}, Pi{}, Grok{}, Antigravity{}} {
		got := a.ClassifyWithTitle("body", "⠙ working", time.Now(), 3*time.Second)
		if got != StateActive {
			t.Errorf("%T should route the title-spinner through the engine, got %v", a, got)
		}
	}
}

// TestEngineClassify_PiWorkingMarker — pi's specific rule for the
// `Working...` body marker. Pin so the rule file's signal is
// end-to-end testable from the agent boundary.
func TestEngineClassify_PiWorkingMarker(t *testing.T) {
	pane := "output\n\nWorking..."
	got := Pi{}.ClassifyWithTitle(pane, "", time.Now(), 3*time.Second)
	if got != StateActive {
		t.Errorf("pi `Working...` marker should classify active, got %v", got)
	}
}
