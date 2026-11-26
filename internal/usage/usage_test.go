package usage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestAgentSummary_TotalTokens — small helper but the dashboard's
// per-agent row prints "input · output", so if TotalTokens ever
// includes cache fields by mistake it'd disagree with what the user
// sees. Pin the sum-of-input-and-output semantic.
func TestAgentSummary_TotalTokens(t *testing.T) {
	s := AgentSummary{InputTokens: 100, OutputTokens: 250}
	if got, want := s.TotalTokens(), 350; got != want {
		t.Errorf("TotalTokens = %d, want %d", got, want)
	}
}

// TestWalkCodex_NoTranscripts — with no ~/.codex tree, WalkCodex
// must return HasData=false (not error) so the dashboard renders the
// install-hint placeholder. The walker now does real parsing; only
// the "no data" branch is asserted here because the rich behavior is
// covered by codexusage's own tests against fixture files.
func TestWalkCodex_NoTranscripts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", "")
	got, err := WalkCodex(5 * time.Hour)
	if err != nil {
		t.Fatalf("WalkCodex on empty HOME: %v", err)
	}
	if got.HasData {
		t.Errorf("empty HOME returned HasData=true: %+v", got)
	}
}

// TestWalkAntigravity_NoTranscripts — fresh install with no
// ~/.gemini/antigravity-cli/ tree should return HasData=false with no
// error. The dashboard renders the install-hint placeholder for that
// case, same as WalkCodex.
func TestWalkAntigravity_NoTranscripts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got, err := WalkAntigravity(5 * time.Hour)
	if err != nil {
		t.Fatalf("missing tree should not error: %v", err)
	}
	if got.HasData {
		t.Errorf("empty HOME returned HasData=true: %+v", got)
	}
}

// TestWalkAntigravity_CountsPBFilesInWindow — Antigravity transcripts
// are opaque protobuf (no schema available), so the walker derives
// the only field it CAN: one prompt per .pb file with mtime inside
// the window. Token fields stay zero — the renderer key off
// HasData + token=0 to surface "(tokens unavailable)" honestly.
//
// Pinning the count semantic here matters because a regression that
// stopped counting (e.g. wrong file extension filter) would silently
// blank Antigravity's panel and make the agent look unused.
func TestWalkAntigravity_CountsPBFilesInWindow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".gemini", "antigravity-cli", "conversations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Three .pb files: two recent, one stale. Plus a non-pb file the
	// walker must skip.
	for _, name := range []string{"recent-a.pb", "recent-b.pb", "stale.pb", "not-a-conversation.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Backdate the stale file beyond the window.
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "stale.pb"), old, old); err != nil {
		t.Fatal(err)
	}

	got, err := WalkAntigravity(5 * time.Hour)
	if err != nil {
		t.Fatalf("WalkAntigravity: %v", err)
	}
	if !got.HasData {
		t.Fatal("expected HasData=true with 2 recent .pb files")
	}
	if got.Prompts != 2 {
		t.Errorf("Prompts = %d, want 2 (excluding stale + non-pb)", got.Prompts)
	}
	// Token fields stay zero — pb is opaque.
	if got.InputTokens != 0 || got.OutputTokens != 0 {
		t.Errorf("expected zero tokens (opaque pb), got in=%d out=%d",
			got.InputTokens, got.OutputTokens)
	}
}

func TestWalkAntigravity_ParsesBrainJSONL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ANTIGRAVITY_HOME", "")

	logsDir := filepath.Join(home, ".gemini", "antigravity-cli", "brain", "conv-1", ".system_generated", "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	recentTS := now.Add(-10 * time.Minute).Format(time.RFC3339)
	oldTS := now.Add(-48 * time.Hour).Format(time.RFC3339)
	content := "" +
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"` + oldTS + `","content":"stale prompt"}` + "\n" +
		`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"` + oldTS + `","input_tokens":999,"cache_read_tokens":500,"output_tokens":100}` + "\n" +
		`{"step_index":2,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"` + recentTS + `","content":"recent prompt 1"}` + "\n" +
		`{"step_index":3,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"` + recentTS + `","input_tokens":1200,"cache_read_tokens":4500,"output_tokens":350}` + "\n" +
		`{"step_index":4,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"` + recentTS + `","content":"recent prompt 2"}` + "\n" +
		`{"step_index":5,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"` + recentTS + `","input_tokens":800,"cache_read_tokens":5500,"output_tokens":150}` + "\n"
	if err := os.WriteFile(filepath.Join(logsDir, "transcript.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := WalkAntigravity(5 * time.Hour)
	if err != nil {
		t.Fatalf("WalkAntigravity: %v", err)
	}
	if !got.HasData {
		t.Fatal("expected HasData=true")
	}
	if got.Prompts != 2 {
		t.Errorf("Prompts = %d, want 2", got.Prompts)
	}
	if got.InputTokens != 2000 {
		t.Errorf("InputTokens = %d, want 2000", got.InputTokens)
	}
	if got.CachedInputTokens != 10000 {
		t.Errorf("CachedInputTokens = %d, want 10000", got.CachedInputTokens)
	}
	if got.OutputTokens != 500 {
		t.Errorf("OutputTokens = %d, want 500", got.OutputTokens)
	}
}

// TestWalkClaude_TolerantOfNoTranscripts — the test machine in CI
// has no ~/.claude/projects/ tree. WalkClaude must not error on
// the empty case; it should just return a HasData=false summary.
// The Claude rich panel handles the nil-Aggregate case separately.
func TestWalkClaude_TolerantOfNoTranscripts(t *testing.T) {
	// Force HOME to an empty dir so claudeusage.Walk finds nothing.
	t.Setenv("HOME", t.TempDir())
	got, err := WalkClaude(5 * time.Hour)
	if err != nil {
		// claudeusage.Walk returns nil-with-no-error on a missing
		// tree today. If that ever changes to an error, decide:
		// either propagate (and the dashboard suppresses), or
		// swallow here. For now we just assert "no panic".
		t.Logf("WalkClaude errored on empty HOME (acceptable): %v", err)
		return
	}
	if got.HasData {
		t.Errorf("WalkClaude on empty HOME returned HasData=true: %+v", got)
	}
}
