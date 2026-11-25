package claude

import (
	"strings"
	"testing"
)

func TestReadTurn_BackgroundTaskPromptBelowClosingRule(t *testing.T) {
	// A complete v2 input box whose closing rule is immediately followed
	// by a selected background-task row (`❯ ○ ...`) must still parse the
	// real input box above rather than treating the closing rule as an
	// unclosed box.
	pane := "" +
		"✳ Working on tests… (esc to interrupt)\n" +
		"────────────────────────────────────────\n" +
		"❯ draft follow-up\n" +
		"────────────────────────────────────────\n" +
		"❯ ○ 1 background task\n"
	turn := ReadTurn(pane)
	if !turn.HasInput {
		t.Fatal("expected HasInput=true")
	}
	if !turn.Busy {
		t.Errorf("expected Busy=true when status line is above complete input box, got %+v", turn)
	}
	if strings.Contains(turn.Output, "draft follow-up") {
		t.Errorf("Output should not include typed text inside input box: %q", turn.Output)
	}
}
