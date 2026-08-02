package agent

import (
	"testing"
	"time"
)

// fakeBareAgent satisfies Agent but NOT TitleAwareAgent — used to
// prove ClassifyState falls back to legacy Classify when the agent
// doesn't opt in.
type fakeBareAgent struct{ ret State }

func (f fakeBareAgent) ID() ID                                          { return "fake" }
func (f fakeBareAgent) DisplayName() string                             { return "Fake" }
func (f fakeBareAgent) Binary() string                                  { return "fake" }
func (f fakeBareAgent) LaunchCmd(bool) string                           { return "fake" }
func (f fakeBareAgent) ConfigRoot(home string) string                   { return home }
func (f fakeBareAgent) TranscriptsRoot(home string) string              { return home }
func (f fakeBareAgent) InitialPrompt(string, string) string             { return "" }
func (f fakeBareAgent) Classify(string, time.Time, time.Duration) State { return f.ret }

// fakeTitleAgent satisfies TitleAwareAgent — proves the dispatcher
// routes through ClassifyWithTitle when it's available.
type fakeTitleAgent struct {
	bodyRet  State
	titleRet State
}

func (f fakeTitleAgent) ID() ID                                          { return "title" }
func (f fakeTitleAgent) DisplayName() string                             { return "Title" }
func (f fakeTitleAgent) Binary() string                                  { return "title" }
func (f fakeTitleAgent) LaunchCmd(bool) string                           { return "title" }
func (f fakeTitleAgent) ConfigRoot(home string) string                   { return home }
func (f fakeTitleAgent) TranscriptsRoot(home string) string              { return home }
func (f fakeTitleAgent) InitialPrompt(string, string) string             { return "" }
func (f fakeTitleAgent) Classify(string, time.Time, time.Duration) State { return f.bodyRet }
func (f fakeTitleAgent) ClassifyWithTitle(_, _ string, _ time.Time, _ time.Duration) State {
	return f.titleRet
}

// TestClassifyState_RoutesToTitleAwareWhenAvailable — the dispatcher
// must prefer ClassifyWithTitle when the agent implements it, so any
// title-derived state wins over body-only.
func TestClassifyState_RoutesToTitleAwareWhenAvailable(t *testing.T) {
	a := fakeTitleAgent{bodyRet: StateIdle, titleRet: StateActive}
	got := ClassifyState(a, "ignored", "⠙", time.Now(), time.Second)
	if got != StateActive {
		t.Errorf("got %v, want active (title path should be used)", got)
	}
}

// TestClassifyState_FallsBackForBareAgent — a vanilla Agent that
// doesn't implement TitleAwareAgent must still work exactly as before,
// so this phase doesn't ripple changes through every agent's tests.
func TestClassifyState_FallsBackForBareAgent(t *testing.T) {
	a := fakeBareAgent{ret: StateNeedsInput}
	got := ClassifyState(a, "body", "anything", time.Now(), time.Second)
	if got != StateNeedsInput {
		t.Errorf("got %v, want needs_input (legacy path should be used)", got)
	}
}

// TestRealClaudeAgent_IsTitleAware — Phase 1 specifically wires the
// Claude agent into the title-aware path. Pin this so a future
// refactor doesn't quietly drop the interface satisfaction.
func TestRealClaudeAgent_IsTitleAware(t *testing.T) {
	var _ TitleAwareAgent = Claude{}
}
