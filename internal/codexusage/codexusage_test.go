package codexusage

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fixtureRollout writes a minimal rollout-*.jsonl into the
// sessions tree rooted at `root`. `events` are emitted verbatim in
// order; the caller supplies them as already-formed JSON lines so
// each test case can vary which records appear.
//
// The directory layout matches Codex's real one
// (~/.codex/sessions/YYYY/MM/DD/rollout-<id>.jsonl) so any
// path-based filtering the walker grows later still gets exercised.
func fixtureRollout(t *testing.T, root string, id string, lines ...string) string {
	t.Helper()
	dir := filepath.Join(root, "2026", "05", "12")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-"+id+".jsonl")
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// Mtime must be inside the window the test uses, or the
	// mtime-pre-filter will skip the file. Set it to the current
	// real wall clock — the test's `now` parameter to walkRoot is
	// what governs in-window matching against per-line timestamps.
	now := time.Now()
	_ = os.Chtimes(path, now, now)
	return path
}

func tokenCountLine(ts string, input, cached, output int) string {
	return fmt.Sprintf(
		`{"timestamp":"%s","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":%d,"cached_input_tokens":%d,"output_tokens":%d,"reasoning_output_tokens":0,"total_tokens":%d}}}}`,
		ts, input, cached, output, input+output,
	)
}

func turnContextLine(ts, model string) string {
	return fmt.Sprintf(`{"timestamp":"%s","type":"turn_context","payload":{"model":"%s","effort":"medium"}}`, ts, model)
}

func userMessageLine(ts, text string) string {
	return fmt.Sprintf(`{"timestamp":"%s","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":%q}]}}`, ts, text)
}

func sessionMetaLine(ts, id string) string {
	return fmt.Sprintf(`{"timestamp":"%s","type":"session_meta","payload":{"id":"%s","timestamp":"%s","cwd":"/x","originator":"test","model_provider":"openai"}}`, ts, id, ts)
}

// TestWalkRoot_MissingTreeReturnsEmpty — fresh-install case: no
// ~/.codex/sessions at all. Walk must return an empty aggregate
// (not an error) so the dashboard renders the install hint cleanly.
func TestWalkRoot_MissingTreeReturnsEmpty(t *testing.T) {
	agg, err := walkRoot(filepath.Join(t.TempDir(), "no-such-dir"), 5*time.Hour, time.Now())
	if err != nil {
		t.Fatalf("missing tree errored: %v", err)
	}
	if agg.Messages != 0 || agg.UserPrompts != 0 || agg.Total.Total() != 0 {
		t.Errorf("expected zero aggregate, got %+v", agg)
	}
}

// TestWalkRoot_HappyPath — typical session: one synthetic env-context
// user message + one synthetic AGENTS.md message + two real user prompts
// + two token_count events.
// Verify totals are summed correctly and the synthetic injection is
// excluded from the prompt count.
func TestWalkRoot_HappyPath(t *testing.T) {
	root := t.TempDir()
	// `now` sits 1m after the latest event so every record falls
	// inside a 5h window.
	now := time.Date(2026, 5, 12, 23, 30, 0, 0, time.UTC)
	ts := func(min int) string {
		return now.Add(time.Duration(-min) * time.Minute).Format(time.RFC3339Nano)
	}
	fixtureRollout(t, root, "happy",
		sessionMetaLine(ts(30), "happy"),
		turnContextLine(ts(30), "gpt-5"),
		userMessageLine(ts(29), "<environment_context>\n<cwd>/x</cwd>\n</environment_context>"),
		userMessageLine(ts(29), "# AGENTS.md instructions for /x\n\n<INSTRUCTIONS>\n- test\n</INSTRUCTIONS>"),
		userMessageLine(ts(28), "hello world"),
		tokenCountLine(ts(27), 1000, 500, 50),
		userMessageLine(ts(20), "follow up please"),
		tokenCountLine(ts(15), 2000, 1800, 100),
	)

	agg, err := walkRoot(root, 5*time.Hour, now)
	if err != nil {
		t.Fatalf("walkRoot: %v", err)
	}
	if got, want := agg.UserPrompts, 2; got != want {
		t.Errorf("UserPrompts = %d, want %d (env-context injection should be filtered)", got, want)
	}
	if got, want := agg.Messages, 2; got != want {
		t.Errorf("Messages (token_count events) = %d, want %d", got, want)
	}
	if got, want := agg.Total.Input, 3000; got != want {
		t.Errorf("Total.Input = %d, want %d", got, want)
	}
	if got, want := agg.Total.Output, 150; got != want {
		t.Errorf("Total.Output = %d, want %d", got, want)
	}
	if got, want := agg.Total.Cached, 2300; got != want {
		t.Errorf("Total.Cached = %d, want %d", got, want)
	}
	if got, ok := agg.ByModel["gpt-5"]; !ok {
		t.Errorf("ByModel missing gpt-5: %+v", agg.ByModel)
	} else if got.Input != 3000 {
		t.Errorf("ByModel[gpt-5].Input = %d, want 3000", got.Input)
	}
}

// TestWalkRoot_WindowFilteringExcludesOldEvents — events earlier
// than `now - window` must be dropped, but events later than `now`
// (clock skew) must also be ignored to keep totals honest.
func TestWalkRoot_WindowFilteringExcludesOldEvents(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 12, 23, 30, 0, 0, time.UTC)
	old := now.Add(-10 * time.Hour).Format(time.RFC3339Nano)
	fresh := now.Add(-1 * time.Hour).Format(time.RFC3339Nano)
	future := now.Add(1 * time.Hour).Format(time.RFC3339Nano)
	fixtureRollout(t, root, "windowed",
		turnContextLine(old, "gpt-5"),
		tokenCountLine(old, 999_999, 0, 999_999), // dropped: too old
		tokenCountLine(fresh, 100, 0, 50),        // counted
		tokenCountLine(future, 999_999, 0, 0),    // dropped: future skew
	)
	agg, err := walkRoot(root, 5*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := agg.Total.Input, 100; got != want {
		t.Errorf("Total.Input = %d, want %d (only fresh event should count)", got, want)
	}
	if got, want := agg.Messages, 1; got != want {
		t.Errorf("Messages = %d, want %d", got, want)
	}
}

// TestWalkRoot_ModelAttributionFromTurnContext — token_count events
// don't carry a model field; we attribute them to the most recent
// turn_context.model seen. Sequence: turn_context(A) → tokens →
// turn_context(B) → tokens. Result: half to A, half to B.
func TestWalkRoot_ModelAttributionFromTurnContext(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 12, 23, 30, 0, 0, time.UTC)
	ts := func(min int) string {
		return now.Add(time.Duration(-min) * time.Minute).Format(time.RFC3339Nano)
	}
	fixtureRollout(t, root, "models",
		turnContextLine(ts(30), "gpt-5"),
		tokenCountLine(ts(28), 100, 0, 10),
		turnContextLine(ts(20), "gpt-5-mini"),
		tokenCountLine(ts(15), 200, 0, 20),
	)
	agg, err := walkRoot(root, 5*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if a := agg.ByModel["gpt-5"]; a == nil || a.Input != 100 || a.Output != 10 {
		t.Errorf("gpt-5 bucket wrong: %+v", a)
	}
	if b := agg.ByModel["gpt-5-mini"]; b == nil || b.Input != 200 || b.Output != 20 {
		t.Errorf("gpt-5-mini bucket wrong: %+v", b)
	}
}

// TestEstimatedCost_AppliesPerModelRates — cost calculation should
// honor the model-specific rate, including the cached-input discount.
// Two models with identical input differ in cost based on rate.
func TestEstimatedCost_AppliesPerModelRates(t *testing.T) {
	a := &Aggregate{ByModel: map[string]*Tokens{
		"gpt-5":      {Input: 1_000_000, Cached: 0, Output: 1_000_000}, // 1.25 + 10.00 = 11.25
		"gpt-5-mini": {Input: 1_000_000, Cached: 0, Output: 1_000_000}, // 0.25 + 2.00  =  2.25
	}}
	got := a.EstimatedCost()
	want := 11.25 + 2.25
	if abs(got-want) > 0.001 {
		t.Errorf("EstimatedCost = %.4f, want %.4f", got, want)
	}
}

// TestEstimatedCost_CachedDiscount — when half the input is cached,
// the cost should drop by the cached-rate ratio, not double-bill.
func TestEstimatedCost_CachedDiscount(t *testing.T) {
	a := &Aggregate{ByModel: map[string]*Tokens{
		"gpt-5": {Input: 1_000_000, Cached: 500_000, Output: 0},
	}}
	// Uncached half: 500_000 * 1.25 / 1e6 = 0.625
	// Cached half:   500_000 * 0.125 / 1e6 = 0.0625
	want := 0.625 + 0.0625
	if got := a.EstimatedCost(); abs(got-want) > 0.001 {
		t.Errorf("EstimatedCost = %.4f, want %.4f", got, want)
	}
}

// TestScanFile_TolerantToBadJSON — a corrupted line in the middle of
// a JSONL must not poison the whole file. The good records around
// it should still be counted.
func TestScanFile_TolerantToBadJSON(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 12, 23, 30, 0, 0, time.UTC)
	ts := now.Add(-10 * time.Minute).Format(time.RFC3339Nano)
	fixtureRollout(t, root, "corrupt",
		turnContextLine(ts, "gpt-5"),
		tokenCountLine(ts, 100, 0, 10),
		`{not valid json — token_count`,
		tokenCountLine(ts, 200, 0, 20),
	)
	agg, err := walkRoot(root, 5*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := agg.Messages, 2; got != want {
		t.Errorf("Messages = %d, want %d (corrupt line should be silently dropped)", got, want)
	}
	if got, want := agg.Total.Input, 300; got != want {
		t.Errorf("Total.Input = %d, want %d", got, want)
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
