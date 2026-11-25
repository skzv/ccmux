package claudeusage

import "testing"

func TestFamilyVersion_DotAndTwoDigitMinor(t *testing.T) {
	if got := familyVersion("claude-opus-4.5", "opus"); got != 4.5 {
		t.Errorf("familyVersion(claude-opus-4.5) = %v, want 4.5", got)
	}
	if got := familyVersion("anthropic/claude-3.5-haiku", "haiku"); got != 3.5 {
		t.Errorf("familyVersion(claude-3.5-haiku) = %v, want 3.5", got)
	}
	if got := familyVersion("claude-opus-4-10", "opus"); got >= 5.0 || got < 4.5 {
		t.Errorf("familyVersion(claude-opus-4-10) = %v, want in [4.5, 5.0)", got)
	}
	if p := priceFor("claude-opus-4.5"); p.Input != 5 || p.Output != 25 {
		t.Errorf("priceFor(claude-opus-4.5) = %+v, want 5/25", p)
	}
}
