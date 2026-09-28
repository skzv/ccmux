package main

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/claudeusage"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
)

// writeClaudeTranscripts points HOME at a temp dir holding one Claude
// Code transcript: a prompt at each of at, answered 30 seconds later by
// a Sonnet response with input, output and cache tokens.
func writeClaudeTranscripts(t *testing.T, at []time.Time) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude", "projects", "-Users-me-Projects-demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	line := func(v map[string]any) {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	for i, ts := range at {
		line(map[string]any{
			"type": "user", "timestamp": ts.UTC().Format(time.RFC3339), "cwd": "/Users/me/Projects/demo",
			"message": map[string]any{"role": "user", "content": "prompt " + strconv.Itoa(i)},
		})
		line(map[string]any{
			"type": "assistant", "timestamp": ts.Add(30 * time.Second).UTC().Format(time.RFC3339),
			"requestId": "req_" + strconv.Itoa(i),
			"message": map[string]any{
				"id": "msg_" + strconv.Itoa(i), "role": "assistant", "model": "claude-sonnet-4-6",
				"usage": map[string]any{
					"input_tokens": 10, "output_tokens": 2,
					"cache_creation_input_tokens": 100, "cache_read_input_tokens": 1000,
				},
			},
		})
	}
	if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// getUsage runs GET /v1/usage (with query q) against a daemon with no
// OpenRouter key, returning the raw body and its decoded form.
func getUsage(t *testing.T, q string) (string, daemon.AgentUsage) {
	t.Helper()
	s := &server{cfg: config.Config{}}
	w := httptest.NewRecorder()
	s.handleUsage(w, httptest.NewRequest(http.MethodGet, "/v1/usage"+q, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1/usage%s: %d %s", q, w.Code, w.Body.String())
	}
	var out daemon.AgentUsage
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	return w.Body.String(), out
}

// TestHandleUsage_ClaudeBlockMatchesTUI — /v1/usage reported only a
// rolling window, while the TUI's quota bar reads Claude's 5-hour session
// block: the phone's usage card and the dashboard disagreed about
// prompts, tokens, cost and when the quota resets. claude_block is the
// block the TUI reads (claudeusage.Walk over SessionBlock), from the same
// transcripts; the rolling-window "claude" summary stays as it was.
//
// The transcripts are a prompt every 10 minutes for 6 hours, ending 10
// minutes before the current hour H: the block that ran from H-6h ran
// out at H-1h, and the prompt at H-1h opened the active one, H-1h to
// H+4h, holding 6 of them. The rolling window holds far more.
func TestHandleUsage_ClaudeBlockMatchesTUI(t *testing.T) {
	h := time.Now().Truncate(time.Hour)
	var at []time.Time
	for i := range 36 {
		at = append(at, h.Add(-6*time.Hour+time.Duration(i)*10*time.Minute))
	}
	writeClaudeTranscripts(t, at)

	_, got := getUsage(t, "")
	b := got.ClaudeBlock
	if b == nil {
		t.Fatal("no claude_block in /v1/usage")
	}
	// What the dashboard's usage panel reads from the same transcripts.
	tui, err := claudeusage.Walk(5 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !b.Active || !b.Start.Equal(tui.BlockStart) || !b.ResetAt.Equal(tui.ResetAt(5*time.Hour)) {
		t.Errorf("block active=%v start=%v reset=%v; TUI: start=%v reset=%v", b.Active, b.Start, b.ResetAt, tui.BlockStart, tui.ResetAt(5*time.Hour))
	}
	if b.Prompts != tui.UserPrompts || b.Messages != tui.Messages ||
		b.InputTokens != tui.Total.Input || b.OutputTokens != tui.Total.Output ||
		b.CacheCreationTokens != tui.Total.CacheCreation || b.CacheReadTokens != tui.Total.CacheRead {
		t.Errorf("block counts %+v; TUI: prompts=%d messages=%d tokens=%+v", *b, tui.UserPrompts, tui.Messages, tui.Total)
	}
	if math.Abs(b.EstimatedCost-tui.EstimatedCost()) > 1e-9 || b.EstimatedCost <= 0 {
		t.Errorf("block cost $%f, TUI $%f", b.EstimatedCost, tui.EstimatedCost())
	}
	// And what that is, worked out by hand.
	if !b.Start.Equal(h.Add(-time.Hour)) || !b.ResetAt.Equal(h.Add(4*time.Hour)) || b.BlockSeconds != 5*3600 {
		t.Errorf("block %v to %v (%ds), want %v to %v (18000s)", b.Start, b.ResetAt, b.BlockSeconds, h.Add(-time.Hour), h.Add(4*time.Hour))
	}
	if b.Prompts != 6 || b.Messages != 6 || b.InputTokens != 60 || b.OutputTokens != 12 || b.CacheCreationTokens != 600 || b.CacheReadTokens != 6000 {
		t.Errorf("block counts %+v, want 6 prompts and messages, 60/12/600/6000 tokens", *b)
	}
	// The rolling window is unchanged, and not the block.
	if got.Claude.WindowSeconds != 5*3600 || got.Claude.Prompts <= b.Prompts {
		t.Errorf("claude (rolling): %+v, want a 5h window holding more than the block's %d prompts", got.Claude, b.Prompts)
	}

	// ?window= sizes the rolling summaries only.
	_, day := getUsage(t, "?window=24h")
	if a, z := jsonOf(t, day.ClaudeBlock), jsonOf(t, b); a != z {
		t.Errorf("?window=24h changed the block: %s, want %s", a, z)
	}
}

// TestHandleUsage_NoActiveClaudeBlock — with no block running (the last
// message is more than 5 hours old, or there are no transcripts at all)
// claude_block says so, with no start or reset time and nothing counted.
func TestHandleUsage_NoActiveClaudeBlock(t *testing.T) {
	for name, at := range map[string][]time.Time{
		"ran out":        {time.Now().Add(-7 * time.Hour)},
		"no transcripts": nil,
	} {
		t.Run(name, func(t *testing.T) {
			writeClaudeTranscripts(t, at)
			raw, got := getUsage(t, "")
			b := got.ClaudeBlock
			if b == nil {
				t.Fatal("no claude_block in /v1/usage")
			}
			if b.Active || b.Prompts != 0 || b.InputTokens != 0 || b.EstimatedCost != 0 || !b.Start.IsZero() || !b.ResetAt.IsZero() {
				t.Errorf("block %+v, want an inactive, empty one", *b)
			}
			if strings.Contains(raw, `"reset_at"`) || strings.Contains(raw, `"start"`) {
				t.Errorf("an inactive block carries times: %s", raw)
			}
		})
	}
}
