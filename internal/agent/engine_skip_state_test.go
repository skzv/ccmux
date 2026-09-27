package agent

import (
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agentdetect"
)

// withExtraRules appends rules to an agent's shipped rule list for the
// duration of the test.
func withExtraRules(t *testing.T, id ID, extra ...agentdetect.Rule) {
	t.Helper()
	orig := rulesFor
	rulesFor = func(got ID) []agentdetect.Rule {
		rules := orig(got)
		if got == id {
			rules = append(append([]agentdetect.Rule{}, rules...), extra...)
		}
		return rules
	}
	t.Cleanup(func() { rulesFor = orig })
}

// TestClassifyStateFrom_SkipStateUpdateKeepsPreviousState — the rule
// schema documents skip_state_update as "park the classifier on the
// previous state" (for overlays like a transcript viewer, so paging
// history doesn't churn the state), but the engine had no previous
// state: a winning skip rule fell through to the fallback classifier,
// which read the quiet overlay as idle / needs_input — a spurious state
// change and, for needs_input, a bell. The daemon now passes the
// previous state and it is kept.
func TestClassifyStateFrom_SkipStateUpdateKeepsPreviousState(t *testing.T) {
	withExtraRules(t, IDClaude, agentdetect.Rule{
		ID:              "transcript_viewer",
		Priority:        2000,
		Region:          "bottom_non_empty_lines(2)",
		State:           "idle",
		Contains:        []string{"Showing detailed transcript"},
		SkipStateUpdate: true,
	})
	const idle = 3 * time.Second
	quiet := time.Now().Add(-time.Hour)
	overlay := "⏺ Read(cmd/ccmuxd/poll.go)\n  ⎿  Read 212 lines\n\n  Showing detailed transcript · ctrl+o to toggle"

	for _, prev := range []State{StateActive, StateNeedsInput, StateIdle, StateError} {
		if got := ClassifyStateFrom(Claude{}, prev, overlay, "", quiet, idle); got != prev {
			t.Errorf("prev %v: overlay classified %v, want the previous state kept", prev, got)
		}
	}

	// No previous state yet (first poll tick): nothing to keep, so the
	// session is classified the ordinary way.
	if got, want := ClassifyStateFrom(Claude{}, StateUnknown, overlay, "", quiet, idle),
		ClassifyState(Claude{}, overlay, "", quiet, idle); got != want {
		t.Errorf("prev unknown: got %v, want ordinary classification %v", got, want)
	}

	// Once the overlay closes, the real screen is classified again.
	shell := "Node.js v22.22.3\nuser@host ~ % "
	if got := ClassifyStateFrom(Claude{}, StateActive, shell, "", quiet, idle); got != StateError {
		t.Errorf("overlay closed onto a shell prompt: got %v, want error", got)
	}
}

// TestClassifyStateFrom_MatchesClassifyStateWithoutSkipRules — no
// shipped rule sets skip_state_update, so for every agent the new entry
// point must classify exactly like ClassifyState whatever the previous
// state was.
func TestClassifyStateFrom_MatchesClassifyStateWithoutSkipRules(t *testing.T) {
	const idle = 3 * time.Second
	panes := []string{
		readPaneFixture(t, "claude_v2_idle.txt"),
		readPaneFixture(t, "claude_crashed_starship.txt"),
		readPaneFixture(t, "opencode_networking_working.txt"),
		"plain output",
	}
	for _, a := range All() {
		if hasSkipRule(a.ID()) {
			t.Errorf("%s: a shipped rule sets skip_state_update — pin it with a real capture in TestClassifyStateFrom_SkipStateUpdateKeepsPreviousState", a.ID())
		}
		for _, pane := range panes {
			for _, lastChange := range []time.Time{time.Now(), time.Now().Add(-time.Hour)} {
				want := ClassifyState(a, pane, "", lastChange, idle)
				for _, prev := range []State{StateUnknown, StateActive, StateNeedsInput} {
					if got := ClassifyStateFrom(a, prev, pane, "", lastChange, idle); got != want {
						t.Errorf("%s prev=%v: ClassifyStateFrom = %v, ClassifyState = %v", a.ID(), prev, got, want)
					}
				}
			}
		}
	}
}
