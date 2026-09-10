package usage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGeminiUsageDeduplicatesNativeMessageUpdates(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".gemini", "tmp", "demo", "chats")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data := `{"sessionId":"id","projectHash":"hash"}
{"id":"u1","type":"user","content":"hello","timestamp":"2026-01-01T00:00:00Z"}
{"id":"a1","type":"gemini","content":"response","timestamp":"2026-01-01T00:00:01Z","tokens":{"input":100,"output":20,"cached":50,"thoughts":7}}
{"id":"a1","type":"gemini","content":"response","timestamp":"2026-01-01T00:00:01Z","tokens":{"input":100,"output":20,"cached":50,"thoughts":7}}
`
	if err := os.WriteFile(filepath.Join(dir, "session-one.jsonl"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	s := WalkGemini(home, 0)
	if !s.HasData || s.Prompts != 1 || s.InputTokens != 100 || s.OutputTokens != 20 || s.CachedInputTokens != 50 || s.ReasoningTokens != 7 {
		t.Fatalf("%+v", s)
	}
	if s.CostAvailable == nil || *s.CostAvailable {
		t.Fatal("must not invent subscription cost")
	}
	if s := WalkGemini(home, time.Second); s.HasData {
		t.Fatal("included expired usage", s)
	}
}
