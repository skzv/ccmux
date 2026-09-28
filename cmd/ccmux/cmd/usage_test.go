package cmd

import (
	"bytes"
	"encoding/json"
	"github.com/skzv/ccmux/internal/daemon"
	"strings"
	"testing"
	"time"
)

func TestUsageMuse(t *testing.T) {
	available := false
	data := daemon.AgentUsage{Others: []daemon.OtherUsage{{Agent: "muse", Usage: daemon.UsageSummary{HasData: true, Prompts: 2, InputTokens: 100, OutputTokens: 20, CachedInputTokens: 10, ReasoningTokens: 5, CostAvailable: &available}}}}
	var b bytes.Buffer
	if err := writeUsage(&b, data, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "muse") || !strings.Contains(b.String(), "unavailable") || strings.Contains(b.String(), "$0.00") {
		t.Fatal(b.String())
	}
	b.Reset()
	if err := writeUsage(&b, data, true); err != nil {
		t.Fatal(err)
	}
	var decoded daemon.AgentUsage
	if err := json.Unmarshal(b.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	s := decoded.Others[0].Usage
	if s.CostAvailable == nil || *s.CostAvailable || s.CachedInputTokens != 10 || s.ReasoningTokens != 5 {
		t.Fatal(s)
	}
}

// testBlock is an active Claude session block, 13:00 to 18:00 local.
func testBlock() *daemon.ClaudeBlock {
	start := time.Date(2026, 9, 28, 13, 0, 0, 0, time.Local)
	return &daemon.ClaudeBlock{
		Active: true, Start: start, ResetAt: start.Add(5 * time.Hour), BlockSeconds: 18000,
		Prompts: 6, Messages: 14, InputTokens: 60, OutputTokens: 12,
		CacheCreationTokens: 600, CacheReadTokens: 6000, EstimatedCost: 0.0318,
	}
}

// TestWriteClaudeBlock — `ccmux usage` prints Claude's current 5-hour
// session block, the one the dashboard's quota bar shows, below the
// rolling-window table: when it started and resets, and what it holds.
// With no block running it says so; a daemon too old to report the block
// gets no section at all.
func TestWriteClaudeBlock(t *testing.T) {
	b := testBlock()
	now := b.Start.Add(time.Hour + 47*time.Minute) // 3h13m to go
	var out bytes.Buffer
	if err := writeClaudeBlock(&out, b, now); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"CLAUDE 5-HOUR BLOCK  started 13:00, resets 18:00 (in 3h13m)",
		"PROMPTS  MESSAGES  INPUT  OUTPUT  CACHE WRITE  CACHE READ  EST. COST",
		"6        14        60     12      600          6000        $0.03",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}

	out.Reset()
	if err := writeClaudeBlock(&out, &daemon.ClaudeBlock{BlockSeconds: 18000}, now); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "none running") || strings.Contains(out.String(), "PROMPTS") {
		t.Errorf("no block running:\n%s", out.String())
	}

	out.Reset()
	if err := writeClaudeBlock(&out, nil, now); err != nil || out.Len() != 0 {
		t.Errorf("an older daemon (no block): %q, %v; want nothing", out.String(), err)
	}
}

func TestUntilReset(t *testing.T) {
	for d, want := range map[time.Duration]string{
		-time.Second:                    "resetting now",
		0:                               "resetting now",
		20 * time.Second:                "in <1m",
		42*time.Minute + 10*time.Second: "in 42m",
		3*time.Hour + 5*time.Minute:     "in 3h05m",
		4*time.Hour + 59*time.Minute + 40*time.Second: "in 5h00m",
	} {
		if got := untilReset(d); got != want {
			t.Errorf("untilReset(%v) = %q, want %q", d, got, want)
		}
	}
}
