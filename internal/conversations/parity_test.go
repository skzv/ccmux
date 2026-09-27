package conversations

import (
	"path/filepath"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
)

// TestCountMessages_MatchesRecentMessages — regression: the Codex,
// Cursor, Pi and Gemini counters skipped cleanPromptText (and Pi the
// empty-body check), so injected <environment_context> / AGENTS.md
// turns were counted but never listed — 83 of 104 real Codex
// conversations showed a message count the transcript modal couldn't
// account for. Every agent's counter must equal what its reader lists.
func TestCountMessages_MatchesRecentMessages(t *testing.T) {
	dir := t.TempDir()
	const envCtx = `<environment_context>\n  <cwd>/repo</cwd>\n  <shell>zsh</shell>\n</environment_context>`
	const agentsMD = `# AGENTS.md instructions for /repo\n\n<INSTRUCTIONS>\n- Prefer tests.\n</INSTRUCTIONS>`
	cases := []struct {
		name  string
		agent agent.ID
		file  string
		body  string
		want  int
	}{
		{
			name: "codex", agent: agent.IDCodex, file: "rollout.jsonl", want: 2,
			body: `{"timestamp":"2026-05-01T10:00:00Z","type":"session_meta","payload":{"id":"x","originator":"codex-tui","cwd":"/repo"}}
{"timestamp":"2026-05-01T10:00:01Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"sandbox rules"}]}}
{"timestamp":"2026-05-01T10:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` + agentsMD + `"}]}}
{"timestamp":"2026-05-01T10:00:03Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` + envCtx + `"}]}}
{"timestamp":"2026-05-01T10:00:04Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fix the flaky test"}]}}
{"timestamp":"2026-05-01T10:00:05Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Done."}]}}
{"timestamp":"2026-05-01T10:00:06Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"   "}]}}
`,
		},
		{
			name: "cursor", agent: agent.IDCursor, file: "cur.jsonl", want: 2,
			body: `{"role":"user","message":{"content":[{"type":"text","text":"` + envCtx + `"}]},"timestamp":"2026-05-01T10:00:00Z"}
{"role":"user","message":{"content":[{"type":"text","text":"<user_instructions>be brief</user_instructions>"}]},"timestamp":"2026-05-01T10:00:01Z"}
{"role":"user","message":{"content":[{"type":"text","text":"add cursor support"}]},"timestamp":"2026-05-01T10:00:02Z"}
{"role":"assistant","message":{"content":[{"type":"text","text":"working"}]},"timestamp":"2026-05-01T10:00:03Z"}
`,
		},
		{
			name: "pi", agent: agent.IDPi, file: "pi.jsonl", want: 2,
			body: `{"type":"session","version":3,"id":"p1","cwd":"/repo"}
{"type":"message","message":{"role":"user","content":"","timestamp":1748080801000}}
{"type":"message","message":{"role":"user","content":"` + envCtx + `","timestamp":1748080802000}}
{"type":"message","message":{"role":"assistant","content":[{"type":"toolCall","name":"bash"}],"timestamp":1748080803000}}
{"type":"message","message":{"role":"user","content":"build pi support","timestamp":1748080804000}}
{"type":"message","message":{"role":"assistant","content":"on it","timestamp":1748080805000}}
{"type":"message","message":{"role":"toolResult","content":"ok","timestamp":1748080806000}}
`,
		},
		{
			name: "gemini", agent: agent.IDGemini, file: "session.json", want: 2,
			body: `{"sessionId":"g1","projectHash":"h","messages":[
{"type":"user","content":"` + envCtx + `"},
{"type":"user","content":"explain the parser"},
{"type":"gemini","content":"It tokenizes first."},
{"type":"gemini","content":"  "}
]}`,
		},
		{
			name: "claude", agent: agent.IDClaude, file: "claude.jsonl", want: 2,
			body: `{"type":"user","message":{"role":"user","content":"` + envCtx + `"},"timestamp":"2026-05-01T10:00:00Z"}
{"type":"user","message":{"role":"user","content":"real prompt"},"timestamp":"2026-05-01T10:00:01Z"}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"x"}]},"timestamp":"2026-05-01T10:00:02Z"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"reply"}]},"timestamp":"2026-05-01T10:00:03Z"}
`,
		},
	}
	// Claude Code writes a finished background task and a user
	// interrupt as type:"user" records. Neither is a turn.
	cases = append(cases, struct {
		name  string
		agent agent.ID
		file  string
		body  string
		want  int
	}{
		name: "claude-harness-records", agent: agent.IDClaude, file: "claude.jsonl", want: 3,
		body: `{"type":"user","message":{"role":"user","content":"<task-notification>\n<task-id>b1</task-id>\n<status>completed</status>\n<summary>Background command finished</summary>\n</task-notification>"},"origin":{"kind":"task-notification"},"timestamp":"2026-05-01T10:00:00Z"}
{"type":"user","message":{"role":"user","content":"run the tests"},"timestamp":"2026-05-01T10:00:01Z"}
{"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]},"timestamp":"2026-05-01T10:00:02Z"}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"x"},{"type":"text","text":"[Request interrupted by user for tool use]"}]},"timestamp":"2026-05-01T10:00:03Z"}
{"type":"user","message":{"role":"user","content":"why did [Request interrupted by user] show up?"},"timestamp":"2026-05-01T10:00:04Z"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"reply"}]},"timestamp":"2026-05-01T10:00:05Z"}
`,
	})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name, tc.file)
			writeFile(t, path, tc.body)
			c := Conversation{Agent: tc.agent, Path: path}
			n, err := CountMessages(c)
			if err != nil {
				t.Fatal(err)
			}
			msgs, err := RecentMessages(c, 1000)
			if err != nil {
				t.Fatal(err)
			}
			if n != len(msgs) {
				t.Errorf("CountMessages = %d but RecentMessages lists %d: %+v", n, len(msgs), msgs)
			}
			if len(msgs) != tc.want {
				t.Errorf("RecentMessages lists %d, want %d: %+v", len(msgs), tc.want, msgs)
			}
		})
	}
}

// TestListClaude_PreviewSkipsHarnessRecords — regression: a
// conversation that opened with a background-task notification (or an
// interrupt marker) showed that record as its preview, and the reader
// listed every one as a user turn. The preview is the first real prompt.
func TestListClaude_PreviewSkipsHarnessRecords(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude/projects/-repo/0f5c2a8e-1b2c-4d3e-8f90-a1b2c3d4e5f6.jsonl")
	writeFile(t, path,
		`{"type":"user","message":{"role":"user","content":"<task-notification>\n<task-id>b1</task-id>\n<summary>Background command finished</summary>\n</task-notification>"},"timestamp":"2026-05-01T10:00:00Z"}`+"\n"+
			`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]},"timestamp":"2026-05-01T10:00:01Z"}`+"\n"+
			`{"type":"user","message":{"role":"user","content":"ship the parser"},"timestamp":"2026-05-01T10:00:02Z"}`+"\n")
	got, err := ListClaude(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Preview != "ship the parser" {
		t.Fatalf("preview = %+v, want the first real prompt", got)
	}
	msgs, err := RecentMessages(got[0], 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Content != "ship the parser" {
		t.Errorf("RecentMessages = %+v, want only the real prompt", msgs)
	}
}
