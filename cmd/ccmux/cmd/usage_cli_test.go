//go:build !windows

package cmd

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/daemon"
)

// TestUsageCommand_ClaudeBlock — end to end through the CLI: `ccmux
// usage` asks the local daemon and prints its claude_block under the
// table, and `--json` passes it through.
func TestUsageCommand_ClaudeBlock(t *testing.T) {
	e := newCLIEnv(t)
	e.env["TZ"] = "UTC"
	start := time.Now().UTC().Truncate(time.Hour)
	block := daemon.ClaudeBlock{
		Active: true, Start: start, ResetAt: start.Add(5 * time.Hour), BlockSeconds: 18000,
		Prompts: 7, Messages: 9, InputTokens: 70, OutputTokens: 14,
		CacheCreationTokens: 700, CacheReadTokens: 7000, EstimatedCost: 1.5,
	}
	e.fakeDaemon(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/usage" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(daemon.AgentUsage{
			Claude:      daemon.UsageSummary{HasData: true, WindowSeconds: 18000, Prompts: 30, InputTokens: 300, OutputTokens: 60, EstimatedCost: 4.2},
			ClaudeBlock: &block,
		})
	}))

	res := e.run("", "usage")
	if res.code != 0 {
		t.Fatalf("ccmux usage: exit %d: %s", res.code, res.stderr)
	}
	want := "CLAUDE 5-HOUR BLOCK  started " + start.Format("15:04") + ", resets " + start.Add(5*time.Hour).Format("15:04") + " (in "
	if !strings.Contains(res.stdout, want) || !strings.Contains(res.stdout, "$1.50") {
		t.Errorf("ccmux usage output lacks the block (%q):\n%s", want, res.stdout)
	}
	if !strings.Contains(res.stdout, "claude") || !strings.Contains(res.stdout, "$4.20") {
		t.Errorf("the rolling-window table is gone:\n%s", res.stdout)
	}

	res = e.run("", "usage", "--json")
	var got daemon.AgentUsage
	if err := json.Unmarshal([]byte(res.stdout), &got); err != nil {
		t.Fatalf("ccmux usage --json: %v\n%s", err, res.stdout)
	}
	if b := got.ClaudeBlock; b == nil || !b.Active || !b.ResetAt.Equal(block.ResetAt) || b.Prompts != 7 || b.CacheReadTokens != 7000 {
		t.Errorf("--json claude_block: %+v, want %+v", got.ClaudeBlock, block)
	}
}
