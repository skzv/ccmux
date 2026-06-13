package agent

import (
	"strings"
	"testing"
)

// TestLaunchCmd_OpenRouterRouting_InjectsBaseURL — when an agent is in
// the OpenRouter route set, its launch command exports OPENAI_BASE_URL
// (the routing target) ahead of the binary.
func TestLaunchCmd_OpenRouterRouting_InjectsBaseURL(t *testing.T) {
	cmds := Commands{
		OpenRouterAgents: map[ID]bool{IDCodex: true},
	}
	got := LaunchCmd(IDCodex, false, cmds)
	if !strings.Contains(got, "export OPENAI_BASE_URL=https://openrouter.ai/api/v1;") {
		t.Errorf("routed codex missing default OPENAI_BASE_URL export:\n%s", got)
	}
	// The binary still runs after the exports.
	if !strings.Contains(got, "codex") {
		t.Errorf("routed command lost the agent binary:\n%s", got)
	}
}

// TestLaunchCmd_OpenRouterRouting_KeyViaEnvIndirection — the API key
// must NOT be embedded literally; it's read from the shell's
// OPENROUTER_API_KEY so the secret never lands in the command string
// (which tmux/pane captures could surface).
func TestLaunchCmd_OpenRouterRouting_KeyViaEnvIndirection(t *testing.T) {
	cmds := Commands{
		OpenRouterAgents: map[ID]bool{IDOpenCode: true},
	}
	got := LaunchCmd(IDOpenCode, false, cmds)
	if !strings.Contains(got, `export OPENAI_API_KEY="${OPENROUTER_API_KEY:-$OPENAI_API_KEY}"`) {
		t.Errorf("expected env-indirection for the key, got:\n%s", got)
	}
	// Defensive: no obvious literal secret marker should appear.
	if strings.Contains(got, "sk-or-") {
		t.Errorf("a literal key leaked into the command:\n%s", got)
	}
}

// TestLaunchCmd_OpenRouterRouting_CustomBaseURL — a configured base URL
// overrides the default (self-hosted gateway / proxy).
func TestLaunchCmd_OpenRouterRouting_CustomBaseURL(t *testing.T) {
	cmds := Commands{
		OpenRouterAgents:  map[ID]bool{IDKilo: true},
		OpenRouterBaseURL: "https://gw.internal/v1",
	}
	got := LaunchCmd(IDKilo, false, cmds)
	if !strings.Contains(got, "export OPENAI_BASE_URL=https://gw.internal/v1;") {
		t.Errorf("custom base URL not honored:\n%s", got)
	}
}

// TestLaunchCmd_OpenRouterRouting_OnlyRoutedAgents — an agent NOT in the
// route set must get a clean command with no OpenRouter exports.
func TestLaunchCmd_OpenRouterRouting_OnlyRoutedAgents(t *testing.T) {
	cmds := Commands{
		OpenRouterAgents: map[ID]bool{IDCodex: true},
	}
	// Codex is routed; Grok is not.
	grok := LaunchCmd(IDGrok, false, cmds)
	if strings.Contains(grok, "OPENAI_BASE_URL") {
		t.Errorf("un-routed agent got OpenRouter exports:\n%s", grok)
	}
	if grok != "grok" {
		t.Errorf("un-routed grok command = %q, want bare \"grok\"", grok)
	}
}

// TestLaunchCmd_OpenRouterRouting_SurvivesContinueChain — the exports
// must sit ahead of the whole `--continue || … || zsh` fallback chain
// so the routing applies to every link, not just the first invocation.
func TestLaunchCmd_OpenRouterRouting_SurvivesContinueChain(t *testing.T) {
	cmds := Commands{OpenRouterAgents: map[ID]bool{IDCodex: true}}
	got := LaunchCmd(IDCodex, true, cmds)
	exportIdx := strings.Index(got, "export OPENAI_BASE_URL")
	chainIdx := strings.Index(got, "--continue")
	if exportIdx == -1 || chainIdx == -1 {
		t.Fatalf("expected both exports and a continue chain:\n%s", got)
	}
	if exportIdx > chainIdx {
		t.Errorf("exports must precede the continue chain so all links inherit them:\n%s", got)
	}
}

// TestLaunchCmd_NoRouting_IsUnchanged — with no OpenRouter config, the
// command is exactly the agent's own LaunchCmd (the back-compat
// invariant the rest of the suite relies on).
func TestLaunchCmd_NoRouting_IsUnchanged(t *testing.T) {
	for _, id := range []ID{IDClaude, IDCodex, IDOpenCode, IDKilo} {
		got := LaunchCmd(id, false, Commands{})
		want := ByID(id).LaunchCmd(false)
		if got != want {
			t.Errorf("LaunchCmd(%q) with empty Commands = %q, want %q", id, got, want)
		}
	}
}
