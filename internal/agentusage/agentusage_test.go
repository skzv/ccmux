package agentusage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// write makes a .jsonl file under dir with the given lines.
func write(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestWalk_AnthropicShape — input_tokens/output_tokens under a usage
// block, the Anthropic-style transcript shape.
func TestWalk_AnthropicShape(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "sess", "a.jsonl"),
		`{"role":"user","type":"message"}`,
		`{"role":"assistant","usage":{"input_tokens":1000,"output_tokens":250}}`,
		`{"role":"assistant","usage":{"input_tokens":500,"output_tokens":120}}`,
	)
	s, err := Walk(dir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !s.HasData {
		t.Fatal("HasData=false on a transcript with usage")
	}
	if s.InputTokens != 1500 || s.OutputTokens != 370 {
		t.Errorf("tokens = %d in / %d out, want 1500/370", s.InputTokens, s.OutputTokens)
	}
	if s.Prompts != 1 {
		t.Errorf("prompts = %d, want 1 (one user turn)", s.Prompts)
	}
}

// TestWalk_OpenAIShape — prompt_tokens/completion_tokens, the OpenAI
// shape. Both shapes must aggregate into the same Input/Output fields.
func TestWalk_OpenAIShape(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "x.jsonl"),
		`{"type":"user_message"}`,
		`{"usage":{"prompt_tokens":800,"completion_tokens":200}}`,
	)
	s, err := Walk(dir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if s.InputTokens != 800 || s.OutputTokens != 200 {
		t.Errorf("tokens = %d/%d, want 800/200", s.InputTokens, s.OutputTokens)
	}
	if s.Prompts != 1 {
		t.Errorf("prompts = %d, want 1", s.Prompts)
	}
}

// TestWalk_TopLevelTokens — some agents put token fields at the top
// level rather than under usage. Those count too.
func TestWalk_TopLevelTokens(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "y.jsonl"),
		`{"input_tokens":42,"output_tokens":7}`,
	)
	s, _ := Walk(dir, time.Hour)
	if s.InputTokens != 42 || s.OutputTokens != 7 {
		t.Errorf("top-level tokens = %d/%d, want 42/7", s.InputTokens, s.OutputTokens)
	}
}

// TestWalk_PerMessageTimeWindow — messages older than the window are
// excluded even when the file is recent.
func TestWalk_PerMessageTimeWindow(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	inWindow := now.Add(-10 * time.Minute).Format(time.RFC3339)
	tooOld := now.Add(-10 * time.Hour).Format(time.RFC3339)
	write(t, filepath.Join(dir, "z.jsonl"),
		`{"timestamp":"`+tooOld+`","usage":{"input_tokens":9999,"output_tokens":9999}}`,
		`{"timestamp":"`+inWindow+`","usage":{"input_tokens":100,"output_tokens":50}}`,
	)
	s, _ := Walk(dir, time.Hour)
	if s.InputTokens != 100 || s.OutputTokens != 50 {
		t.Errorf("tokens = %d/%d, want only the in-window 100/50", s.InputTokens, s.OutputTokens)
	}
}

// TestWalk_ToolTurnsNotCountedAsPrompts — tool-result follow-ups
// (role=tool) must not inflate the user-prompt count.
func TestWalk_ToolTurnsNotCountedAsPrompts(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "t.jsonl"),
		`{"role":"user"}`,
		`{"role":"tool"}`,
		`{"role":"assistant","usage":{"input_tokens":10,"output_tokens":5}}`,
	)
	s, _ := Walk(dir, time.Hour)
	if s.Prompts != 1 {
		t.Errorf("prompts = %d, want 1 (tool turn must not count)", s.Prompts)
	}
}

// TestWalk_NoData — an empty / unrecognized tree yields HasData=false
// and no error (the graceful placeholder state).
func TestWalk_NoData(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "junk.jsonl"),
		`not json at all`,
		`{"some":"object","with":"no tokens"}`,
	)
	s, err := Walk(dir, time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.HasData {
		t.Error("HasData=true on a transcript with no recognizable usage")
	}
}

// TestWalk_MissingRootIsNotAnError — a never-used agent (no transcript
// dir) returns an empty summary, not an error.
func TestWalk_MissingRootIsNotAnError(t *testing.T) {
	s, err := Walk(filepath.Join(t.TempDir(), "does-not-exist"), time.Hour)
	if err != nil {
		t.Fatalf("missing root should not error: %v", err)
	}
	if s.HasData {
		t.Error("HasData=true for a missing transcript root")
	}
}

// TestWalk_MalformedLinesSkipped — partial / non-JSON lines interleaved
// with good ones don't break the walk.
func TestWalk_MalformedLinesSkipped(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "m.jsonl"),
		`{"usage":{"input_tokens":10,"output_tokens":2}}`,
		`{partial`,
		``,
		`plain text`,
		`{"usage":{"input_tokens":5,"output_tokens":1}}`,
	)
	s, _ := Walk(dir, time.Hour)
	if s.InputTokens != 15 || s.OutputTokens != 3 {
		t.Errorf("tokens = %d/%d, want 15/3 (good lines summed past the junk)", s.InputTokens, s.OutputTokens)
	}
}

// TestWalk_RecursiveSubdirs — agents nest sessions under per-cwd
// subdirectories; the walk must descend into them.
func TestWalk_RecursiveSubdirs(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "proj-a", "s1.jsonl"),
		`{"usage":{"input_tokens":100,"output_tokens":10}}`)
	write(t, filepath.Join(dir, "proj-b", "deep", "s2.jsonl"),
		`{"usage":{"input_tokens":200,"output_tokens":20}}`)
	s, _ := Walk(dir, time.Hour)
	if s.InputTokens != 300 || s.OutputTokens != 30 {
		t.Errorf("tokens = %d/%d, want 300/30 across nested dirs", s.InputTokens, s.OutputTokens)
	}
}
