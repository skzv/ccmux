package conversations

import (
	"path/filepath"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
)

// TestIsHeadless_AllClaudeSDKEntrypoints — regression: only "sdk-cli"
// was treated as headless, so Agent SDK runs from TypeScript ("sdk-ts")
// and Python ("sdk-py") flooded the conversations list.
func TestIsHeadless_AllClaudeSDKEntrypoints(t *testing.T) {
	for _, ep := range []string{"sdk-cli", "sdk-ts", "sdk-py"} {
		if !(Conversation{Agent: agent.IDClaude, Entrypoint: ep}).IsHeadless() {
			t.Errorf("Claude entrypoint %q should be headless", ep)
		}
	}
	for _, ep := range []string{"cli", "claude-vscode", ""} {
		if (Conversation{Agent: agent.IDClaude, Entrypoint: ep}).IsHeadless() {
			t.Errorf("Claude entrypoint %q should be interactive", ep)
		}
	}
}

// TestListCodex_SubagentRolloutsAreHeadless — regression: Codex writes
// guardian reviews and thread_spawn children as ordinary rollouts whose
// session_meta.payload.source is {"subagent": …} next to a normal
// originator. They made up ~40% of real Codex rows and were not hidden
// by ExcludeHeadless.
func TestListCodex_SubagentRolloutsAreHeadless(t *testing.T) {
	home := t.TempDir()
	day := filepath.Join(home, ".codex/sessions/2026/05/23")
	rollout := func(n, payload string) {
		writeFile(t,
			filepath.Join(day, "rollout-2026-05-23T10-00-00-00000000-0000-0000-0000-00000000000"+n+".jsonl"),
			`{"timestamp":"2026-05-23T10:00:0`+n+`Z","type":"session_meta","payload":`+payload+`}`+"\n",
		)
	}
	rollout("1", `{"id":"u1","originator":"codex-tui","source":"cli","cwd":"/p"}`)
	rollout("2", `{"id":"u2","originator":"codex_work_desktop","source":{"subagent":{"other":"guardian"}},"cwd":"/p"}`)
	rollout("3", `{"id":"u3","originator":"codex-tui","source":{"subagent":{"thread_spawn":{"parent_thread_id":"x","depth":1}}},"cwd":"/p"}`)
	rollout("4", `{"id":"u4","originator":"Codex Desktop","source":"vscode","cwd":"/p"}`)

	all, err := All(Options{HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	headless := map[string]bool{}
	for _, c := range all {
		headless[c.ID[len(c.ID)-1:]] = c.IsHeadless()
	}
	want := map[string]bool{"1": false, "2": true, "3": true, "4": false}
	for id, w := range want {
		if got, ok := headless[id]; !ok || got != w {
			t.Errorf("rollout %s: IsHeadless = %v (present %v), want %v", id, got, ok, w)
		}
	}

	filtered, err := All(Options{HomeDir: home, ExcludeHeadless: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 2 {
		t.Errorf("ExcludeHeadless kept %d rows, want 2 (the cli + vscode sessions)", len(filtered))
	}
}
