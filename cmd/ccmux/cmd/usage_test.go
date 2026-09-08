package cmd

import (
	"bytes"
	"encoding/json"
	"github.com/skzv/ccmux/internal/daemon"
	"strings"
	"testing"
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
