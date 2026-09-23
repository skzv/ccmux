package claudeusage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// These regression tests only use API that predates the session-block
// fix (Walk, ResetAt, UserPrompts, scanFile), so they compile — and
// fail — against the rolling-window implementation.

// exchange is one prompt + its assistant response in a test transcript.
type exchange struct {
	at time.Time // prompt time; the response lands 30s later
}

// writeTranscript points HOME at a temp dir holding one Claude
// transcript with a prompt/response pair per exchange.
func writeTranscript(t *testing.T, exchanges []exchange) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude", "projects", "-Users-me-Projects-demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "s.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for i, ex := range exchanges {
		writeJSONL(t, f, map[string]any{
			"type": "user", "timestamp": ex.at.UTC().Format(time.RFC3339),
			"message": map[string]any{"role": "user", "content": "prompt"},
		})
		writeJSONL(t, f, map[string]any{
			"type": "assistant", "timestamp": ex.at.Add(30 * time.Second).UTC().Format(time.RFC3339),
			"requestId": "req_" + intStr(i),
			"message": map[string]any{
				"id": "msg_" + intStr(i), "role": "assistant", "model": "claude-sonnet-4-6",
				"usage": map[string]any{"input_tokens": 10, "output_tokens": 1},
			},
		})
	}
}

func intStr(i int) string {
	const digits = "0123456789"
	if i < 10 {
		return digits[i : i+1]
	}
	return intStr(i/10) + digits[i%10:i%10+1]
}

// continuousUse is a prompt every 10 minutes for 6 hours, starting on
// the hour H-6h and ending at H-10m (H = the current hour).
func continuousUse(h time.Time) []exchange {
	var exs []exchange
	for i := 0; i < 36; i++ {
		exs = append(exs, exchange{at: h.Add(-6*time.Hour + time.Duration(i)*10*time.Minute)})
	}
	return exs
}

// TestWalk_ContinuousUseCountsOnlyActiveBlock — regression for the
// rolling-window model. A prompt every 10 minutes for 6 hours used to
// report "resets in ≤10m" (oldest message in [now-5h, now] + 5h) and
// ~30 prompts. Anthropic's limit is a 5-hour session block: the first
// block opened at H-6h and ran out at H-1h; the prompt at H-1h
// (answered at H-1h+30s, more than 5h after H-6h) opened the active
// block, floored to H-1h. So the quota resets at H+4h — 3-4h from now —
// and only the 6 prompts since H-1h count against it.
func TestWalk_ContinuousUseCountsOnlyActiveBlock(t *testing.T) {
	h := time.Now().Truncate(time.Hour)
	writeTranscript(t, continuousUse(h))

	agg, err := Walk(5 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if want := h.Add(4 * time.Hour); !agg.ResetAt(5 * time.Hour).Equal(want) {
		t.Errorf("ResetAt = %v, want %v (active block H-1h..H+4h)", agg.ResetAt(5*time.Hour), want)
	}
	if agg.UserPrompts != 6 {
		t.Errorf("UserPrompts = %d, want 6 (only the active block's prompts)", agg.UserPrompts)
	}
	if agg.Messages != 6 {
		t.Errorf("Messages = %d, want 6", agg.Messages)
	}
	if agg.Total.Input != 60 {
		t.Errorf("Total.Input = %d, want 60 (tokens cover the same block)", agg.Total.Input)
	}
}

// TestWalk_IdleGapStartsNewBlock — after more than 5h of silence the
// next message opens a new block floored to its hour; the old block's
// messages don't count and don't move the reset time.
func TestWalk_IdleGapStartsNewBlock(t *testing.T) {
	h := time.Now().Truncate(time.Hour)
	writeTranscript(t, []exchange{
		{at: h.Add(-9*time.Hour + 10*time.Minute)}, // old block, H-9h..H-4h
		{at: h.Add(-9*time.Hour + 20*time.Minute)},
		{at: h.Add(-2*time.Hour + 15*time.Minute)}, // >5h later: new block at H-2h
		{at: h.Add(-2*time.Hour + 25*time.Minute)},
		{at: h.Add(-10 * time.Minute)},
	})

	agg, err := Walk(5 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// The rolling model said first-in-window + 5h = H+3h15m.
	if want := h.Add(3 * time.Hour); !agg.ResetAt(5 * time.Hour).Equal(want) {
		t.Errorf("ResetAt = %v, want %v (block floored to H-2h)", agg.ResetAt(5*time.Hour), want)
	}
	if agg.UserPrompts != 3 {
		t.Errorf("UserPrompts = %d, want 3", agg.UserPrompts)
	}
}

// TestWalk_ExpiredBlockIsEmpty — a block that started at H-5h has run
// out by now (≥ H) even though its last message is recent. The quota
// is fully reset: nothing counts and there is no reset time until the
// next message opens a new block. The rolling model kept reporting the
// old block's prompts with a reset "in 10m".
func TestWalk_ExpiredBlockIsEmpty(t *testing.T) {
	h := time.Now().Truncate(time.Hour)
	var exs []exchange
	for i := 1; i <= 29; i++ { // H-4h50m … H-10m, all in block H-5h..H
		exs = append(exs, exchange{at: h.Add(-5*time.Hour + time.Duration(i)*10*time.Minute)})
	}
	writeTranscript(t, exs)

	agg, err := Walk(5 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got := agg.ResetAt(5 * time.Hour); !got.IsZero() {
		t.Errorf("ResetAt = %v, want zero (block ended at H)", got)
	}
	if agg.UserPrompts != 0 || agg.Messages != 0 || agg.Total.Total() != 0 {
		t.Errorf("expired block should count nothing: prompts=%d msgs=%d tokens=%d",
			agg.UserPrompts, agg.Messages, agg.Total.Total())
	}
}

// TestScanFile_ExcludesSyntheticUserRecords — regression for the
// UserPrompts over-count: ~44% of "prompts" on real data were records
// Claude Code writes itself. Only the human's prompts count; a slash
// command counts only when the model answers it.
func TestScanFile_ExcludesSyntheticUserRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ts := now.Add(-time.Hour).UTC().Format(time.RFC3339)
	user := func(extra map[string]any, content any) {
		rec := map[string]any{"type": "user", "timestamp": ts, "message": map[string]any{"role": "user", "content": content}}
		for k, v := range extra {
			rec[k] = v
		}
		writeJSONL(t, f, rec)
	}
	n := 0
	assistant := func() {
		n++
		writeJSONL(t, f, map[string]any{
			"type": "assistant", "timestamp": ts, "requestId": "r" + intStr(n),
			"message": map[string]any{"id": "m" + intStr(n), "role": "assistant", "usage": map[string]any{"input_tokens": 1}},
		})
	}
	text := func(s string) []map[string]any { return []map[string]any{{"type": "text", "text": s}} }

	// /model: caveat (isMeta) + command + stdout, no model turn.
	user(map[string]any{"isMeta": true}, "<local-command-caveat>Caveat: The messages below were generated by the user while running local commands.</local-command-caveat>")
	user(nil, "<command-name>/model</command-name>\n            <command-message>model</command-message>\n            <command-args>fable</command-args>")
	user(nil, "<local-command-stdout>Set model to \x1b[1mFable\x1b[22m</local-command-stdout>")
	// A real prompt with a pasted image: the image note is isMeta.
	user(nil, text("what is in this screenshot?")) // counted (1)
	user(map[string]any{"isMeta": true}, text("[Image: source: /tmp/shot.png]"))
	assistant()
	user(nil, []map[string]any{{"type": "tool_result", "content": "ok"}})
	assistant()
	user(nil, text("[Request interrupted by user]"))
	// Background task finished: the model answers, but nobody typed it.
	user(nil, "<task-notification>\n<task-id>abc</task-id>\n</task-notification>")
	assistant()
	// Compaction summary.
	user(map[string]any{"isCompactSummary": true, "isVisibleInTranscriptOnly": true}, "This session is being continued from a previous conversation…")
	// Inline sidechain (older transcripts).
	user(map[string]any{"isSidechain": true}, "subagent instructions")
	assistant()
	// /plan with a prompt: the model answers, so it counts (2).
	user(nil, "<command-name>/plan</command-name>\n            <command-message>plan</command-message>\n            <command-args>build the scroller</command-args>")
	user(nil, "<local-command-stdout>Enabled plan mode</local-command-stdout>")
	assistant()
	// Bash-mode echo.
	user(nil, "<bash-input>ls</bash-input>")
	user(nil, "<bash-stdout>a b</bash-stdout><bash-stderr></bash-stderr>")
	// Plain typed prompt (3).
	user(nil, "and now fix the tests")
	assistant()
	f.Close()

	r := scanFile(path, now.Add(-12*time.Hour), now)
	if r.userPrompts != 3 {
		t.Errorf("userPrompts = %d, want 3 (typed prompt, image prompt, /plan with args)", r.userPrompts)
	}
	if r.assistantCount != 6 {
		t.Errorf("assistantCount = %d, want 6 (every response still costs tokens)", r.assistantCount)
	}
}

// TestScanFile_SubagentTranscriptHasNoPrompts — files under a
// subagents/ directory hold the parent agent's instructions to a
// subagent; their tokens count, their "user" turns don't.
func TestScanFile_SubagentTranscriptHasNoPrompts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sess", "subagents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "agent-a1.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ts := now.Add(-time.Hour).UTC().Format(time.RFC3339)
	writeJSONL(t, f, map[string]any{"type": "user", "timestamp": ts, "message": map[string]any{"role": "user", "content": "search the repo for X"}})
	writeJSONL(t, f, map[string]any{"type": "assistant", "timestamp": ts, "message": map[string]any{"role": "assistant", "usage": map[string]any{"input_tokens": 7}}})
	f.Close()

	r := scanFile(path, now.Add(-12*time.Hour), now)
	if r.userPrompts != 0 {
		t.Errorf("userPrompts = %d, want 0 for a subagent transcript", r.userPrompts)
	}
	if r.total.Input != 7 {
		t.Errorf("total.Input = %d, want 7 (subagent tokens still count)", r.total.Input)
	}
}
