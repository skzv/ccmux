package claudeusage

import (
	"testing"
	"time"
)

// TestWalk_ReportsBlockStart — the active block's start is exposed on
// the aggregate (continuous-use scenario from block_test.go).
func TestWalk_ReportsBlockStart(t *testing.T) {
	h := time.Now().Truncate(time.Hour)
	writeTranscript(t, continuousUse(h))
	agg, err := Walk(SessionBlock)
	if err != nil {
		t.Fatal(err)
	}
	if !agg.BlockStart.Equal(h.Add(-time.Hour)) || !agg.WindowStart.Equal(agg.BlockStart) {
		t.Errorf("BlockStart = %v WindowStart = %v, want both %v", agg.BlockStart, agg.WindowStart, h.Add(-time.Hour))
	}
}

// TestWalkRolling_KeepsWindowSemantics — the cross-agent summary
// (internal/usage) still reads a plain rolling window.
func TestWalkRolling_KeepsWindowSemantics(t *testing.T) {
	h := time.Now().Truncate(time.Hour)
	writeTranscript(t, []exchange{
		{at: h.Add(-9 * time.Hour)},
		{at: h.Add(-2 * time.Hour)},
		{at: h.Add(-10 * time.Minute)},
	})
	agg, err := WalkRolling(24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if agg.UserPrompts != 3 || agg.Messages != 3 {
		t.Errorf("rolling 24h: prompts=%d msgs=%d, want 3/3", agg.UserPrompts, agg.Messages)
	}
	if !agg.BlockStart.IsZero() {
		t.Errorf("rolling aggregate has BlockStart %v", agg.BlockStart)
	}
}

// TestActiveBlock pins the ccusage block algorithm on fixed times.
func TestActiveBlock(t *testing.T) {
	base := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	at := func(h, m int) time.Time { return base.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute) }
	cases := []struct {
		name      string
		responses []time.Time
		now       time.Time
		start     time.Time
		first     time.Time
		ok        bool
	}{
		{"none", nil, at(1, 0), time.Time{}, time.Time{}, false},
		{"single floors to hour", []time.Time{at(0, 37)}, at(1, 0), at(0, 0), at(0, 37), true},
		{"exactly 5h after start stays in block", []time.Time{at(0, 0), at(5, 0)}, at(4, 59), at(0, 0), at(0, 0), true},
		{"just past 5h opens next", []time.Time{at(0, 0), at(5, 1)}, at(5, 30), at(5, 0), at(5, 1), true},
		{"block ran out", []time.Time{at(0, 10), at(4, 50)}, at(5, 0), time.Time{}, time.Time{}, false},
		{"idle 5h", []time.Time{at(2, 0)}, at(7, 0), time.Time{}, time.Time{}, false},
		{"unsorted input", []time.Time{at(5, 20), at(0, 5)}, at(6, 0), at(5, 0), at(5, 20), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, first, ok := activeBlock(tc.responses, 5*time.Hour, tc.now)
			if ok != tc.ok || !start.Equal(tc.start) || !first.Equal(tc.first) {
				t.Errorf("activeBlock = (%v, %v, %v), want (%v, %v, %v)", start, first, ok, tc.start, tc.first, tc.ok)
			}
		})
	}
}

func TestPromptKindOf(t *testing.T) {
	cases := []struct {
		raw  string
		want promptKind
	}{
		{`"fix the bug"`, humanPrompt},
		{`[{"type":"text","text":"hello"}]`, humanPrompt},
		{`[{"type":"image"},{"type":"text","text":"what is this"}]`, humanPrompt},
		{`[{"type":"tool_result","content":"x"}]`, turnBoundary},
		{`"<task-notification><task-id>1</task-id></task-notification>"`, turnBoundary},
		{`"<command-name>/model</command-name>"`, commandPrompt},
		{`"<command-message>review is running…</command-message>\n<command-name>/review</command-name>"`, commandPrompt},
		{`"<local-command-stdout>done</local-command-stdout>"`, notPrompt},
		{`"<local-command-caveat>Caveat</local-command-caveat>"`, notPrompt},
		{`"<bash-input>ls</bash-input>"`, notPrompt},
		{`[{"type":"text","text":"[Request interrupted by user for tool use]"}]`, notPrompt},
		{`"  <command-args>x</command-args>"`, notPrompt},
	}
	for _, tc := range cases {
		if got := promptKindOf([]byte(tc.raw)); got != tc.want {
			t.Errorf("promptKindOf(%s) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}
