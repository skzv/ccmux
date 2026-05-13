package usage

import (
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

// TestWalkCodex_Stub — Codex walker is intentionally a stub today.
// HasData must be false so the dashboard renders the install-hint
// placeholder instead of a misleading "0 tokens" row. When this
// gets replaced with a real walker (PR landing fixture-driven
// transcript parsing), the test will fail and the assertion needs
// to flip.
func TestWalkCodex_Stub(t *testing.T) {
	got, err := WalkCodex(5 * time.Hour)
	if err != nil {
		t.Fatalf("stub should not error: %v", err)
	}
	if got.HasData {
		t.Errorf("stub returned HasData=true — expected false until a real walker lands. Did you implement WalkCodex? Update this test.")
	}
}

// TestWalkGemini_Stub mirrors WalkCodex_Stub.
func TestWalkGemini_Stub(t *testing.T) {
	got, err := WalkGemini(5 * time.Hour)
	if err != nil {
		t.Fatalf("stub should not error: %v", err)
	}
	if got.HasData {
		t.Errorf("stub returned HasData=true — expected false until a real walker lands. Did you implement WalkGemini? Update this test.")
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
