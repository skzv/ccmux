package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMuseUsageCompletionOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	path := filepath.Join(home, "data/muse/sessions/2026/09/08/01a0828e-037e-7c32-bd87-87b6e25b70fd/session.jsonl")
	data, err := os.ReadFile("../muse/testdata/native-session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var r map[string]any
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatal(err)
		}
		if p, ok := r["payload"].(map[string]any); ok {
			if e, ok := p["event"].(map[string]any); ok && e["kind"] == "model_completed" {
				e["usage"] = map[string]int{"input_tokens": 101, "output_tokens": 23, "cached_tokens": 11, "reasoning_tokens": 5}
			}
		}
		encoded, _ := json.Marshal(r)
		lines = append(lines, string(encoded), string(encoded))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0600); err != nil {
		t.Fatal(err)
	}
	s := WalkMuse(home, 0)
	if !s.HasData || s.Prompts != 1 || s.InputTokens != 101 || s.OutputTokens != 23 || s.CachedInputTokens != 11 || s.ReasoningTokens != 5 || s.CostAvailable == nil || *s.CostAvailable {
		t.Fatal(s)
	}
	if s := WalkMuse(home, time.Nanosecond); s.HasData {
		t.Fatal("old fixture counted in current window", s)
	}
}
