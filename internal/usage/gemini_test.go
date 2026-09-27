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

// TestGeminiUsage_SubagentSessionHasNoPrompts — a subagent session's
// "user" messages are the parent agent's instructions. They were
// counted as prompts; the session's tokens are still real usage.
func TestGeminiUsage_SubagentSessionHasNoPrompts(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".gemini", "tmp", "demo", "chats")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	user := `{"sessionId":"main","projectHash":"hash"}
{"id":"u1","type":"user","content":"hello","timestamp":"2026-01-01T00:00:00Z"}
{"id":"a1","type":"gemini","content":"hi","timestamp":"2026-01-01T00:00:01Z","tokens":{"input":100,"output":10}}
`
	sub := `{"sessionId":"sub","projectHash":"hash","kind":"subagent"}
{"id":"u1","type":"user","content":"investigate the parser","timestamp":"2026-01-01T00:00:02Z"}
{"id":"u2","type":"user","content":"then report back","timestamp":"2026-01-01T00:00:03Z"}
{"id":"a1","type":"gemini","content":"done","timestamp":"2026-01-01T00:00:04Z","tokens":{"input":40,"output":4}}
`
	for name, body := range map[string]string{"session-main.jsonl": user, "session-sub.jsonl": sub} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s := WalkGemini(home, 0)
	if s.Prompts != 1 {
		t.Errorf("Prompts = %d, want 1 (subagent turns are not prompts)", s.Prompts)
	}
	if s.InputTokens != 140 || s.OutputTokens != 14 {
		t.Errorf("tokens = %d/%d, want 140/14 (subagent tokens still count)", s.InputTokens, s.OutputTokens)
	}
}
