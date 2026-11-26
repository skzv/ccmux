package conversations

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/skzv/ccmux/internal/agent"
)

func createAntigravitySummaryDB(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE conversation_summaries (
		conversation_id text PRIMARY KEY,
		title text NOT NULL DEFAULT "",
		preview text NOT NULL DEFAULT "",
		step_count integer NOT NULL DEFAULT 0,
		last_modified_time datetime NOT NULL,
		workspace_uris text NOT NULL,
		status text NOT NULL DEFAULT "",
		source text NOT NULL DEFAULT "",
		project_id text NOT NULL DEFAULT "",
		agent_name text NOT NULL DEFAULT "",
		parent_conversation_id text NOT NULL DEFAULT "",
		nesting_depth integer NOT NULL DEFAULT 0
	)`)
	if err != nil {
		db.Close()
		t.Fatalf("create table: %v", err)
	}
	return db
}

func TestListAntigravity_SQLiteAndJSONL(t *testing.T) {
	home := t.TempDir()
	base := filepath.Join(home, ".gemini", "antigravity-cli")

	// Main conversation has both .pb, SQLite metadata, and brain JSONL transcript.
	mainID := "11111111-1111-1111-1111-111111111111"
	subID := "22222222-2222-2222-2222-222222222222"
	jsonlOnlyID := "33333333-3333-3333-3333-333333333333"

	writeFile(t, filepath.Join(base, "conversations", mainID+".pb"), "opaque")
	writeFile(t, filepath.Join(base, "brain", mainID, ".system_generated", "logs", "transcript.jsonl"),
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-11-25T10:00:00Z","content":"fix the auth middleware"}`+"\n"+
			`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-11-25T10:00:05Z","tool_calls":[{"name":"read_file"}]}`+"\n"+
			`{"step_index":2,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-11-25T10:00:10Z","content":"Updated the auth check."}`+"\n"+
			`{"step_index":3,"source":"SYSTEM","type":"SYSTEM_MESSAGE","created_at":"2026-11-25T10:00:11Z","content":"internal reminder"}`+"\n",
	)
	writeFile(t, filepath.Join(base, "brain", jsonlOnlyID, ".system_generated", "logs", "transcript.jsonl"),
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-11-25T12:00:00Z","content":"audit performance"}`+"\n"+
			`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-11-25T12:00:04Z","content":"Profile looks clean."}`+"\n",
	)

	db := createAntigravitySummaryDB(t, filepath.Join(base, "conversation_summaries.db"))
	_, err := db.Exec(`INSERT INTO conversation_summaries
		(conversation_id, title, preview, step_count, last_modified_time, workspace_uris, parent_conversation_id, nesting_depth)
		VALUES
		(?, 'Fix Auth Middleware', 'fix the auth middleware', 4, '2026-11-25 10:05:00+00:00', '["file:///Users/demo/Projects/ccmux"]', '', 0),
		(?, 'Subagent Lookup', 'inspect files', 2, '2026-11-25 10:03:00+00:00', '["file:///Users/demo/Projects/ccmux"]', ?, 1)
	`, mainID, subID, mainID)
	if err != nil {
		db.Close()
		t.Fatalf("insert rows: %v", err)
	}
	db.Close()

	got, err := ListAntigravity(home)
	if err != nil {
		t.Fatalf("ListAntigravity: %v", err)
	}
	byID := map[string]Conversation{}
	for _, c := range got {
		byID[c.ID] = c
	}

	mainConv, ok := byID[mainID]
	if !ok {
		t.Fatalf("missing main conversation %s", mainID)
	}
	if mainConv.Project != filepath.Clean("/Users/demo/Projects/ccmux") {
		t.Errorf("Project = %q, want /Users/demo/Projects/ccmux", mainConv.Project)
	}
	if mainConv.Preview != "Fix Auth Middleware" {
		t.Errorf("Preview = %q, want %q", mainConv.Preview, "Fix Auth Middleware")
	}
	if mainConv.IsHeadless() {
		t.Errorf("main conversation should not be headless")
	}

	// CountMessages & RecentMessages read visible user + assistant turns only.
	n, err := CountMessages(mainConv)
	if err != nil {
		t.Fatalf("CountMessages: %v", err)
	}
	if n != 2 {
		t.Errorf("CountMessages = %d, want 2 (1 user + 1 text assistant)", n)
	}
	msgs, err := RecentMessages(mainConv, 10)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("len(msgs) = %d, want 2", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "fix the auth middleware" {
		t.Errorf("msgs[0] = %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "Updated the auth check." {
		t.Errorf("msgs[1] = %+v", msgs[1])
	}

	// Subagent row is marked headless so ExcludeHeadless filters it out.
	subConv, ok := byID[subID]
	if !ok {
		t.Fatalf("missing subagent conversation %s", subID)
	}
	if !subConv.Subagent || !subConv.IsHeadless() {
		t.Errorf("subagent conversation should have Subagent=true and IsHeadless()=true: %+v", subConv)
	}

	// JSONL-only conversation extracts preview and timestamp without DB entry.
	jConv, ok := byID[jsonlOnlyID]
	if !ok {
		t.Fatalf("missing jsonl-only conversation %s", jsonlOnlyID)
	}
	if jConv.Preview != "audit performance" {
		t.Errorf("jsonl-only Preview = %q, want %q", jConv.Preview, "audit performance")
	}
	wantTS := time.Date(2026, 11, 25, 12, 0, 4, 0, time.UTC)
	if !jConv.LastActivity.Equal(wantTS) && jConv.LastActivity.Before(wantTS) {
		t.Errorf("jsonl-only LastActivity = %v, want >= %v", jConv.LastActivity, wantTS)
	}

	// All() with ExcludeHeadless drops the Antigravity subagent.
	filtered, err := All(Options{HomeDir: home, ExcludeHeadless: true})
	if err != nil {
		t.Fatalf("All(ExcludeHeadless): %v", err)
	}
	for _, c := range filtered {
		if c.ID == subID {
			t.Errorf("ExcludeHeadless failed to filter Antigravity subagent %s", subID)
		}
	}
}

func TestReadAntigravityMessages_PrefersFullTranscript(t *testing.T) {
	home := t.TempDir()
	logsDir := filepath.Join(home, ".gemini", "antigravity-cli", "brain", "conv-full", ".system_generated", "logs")
	compact := filepath.Join(logsDir, "transcript.jsonl")
	full := filepath.Join(logsDir, "transcript_full.jsonl")

	writeFile(t, compact, `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-11-25T10:00:00Z","content":"short...","truncated_fields":["content"]}`+"\n")
	writeFile(t, full, `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-11-25T10:00:00Z","content":"complete untruncated user prompt"}`+"\n")

	c := Conversation{ID: "conv-full", Agent: agent.IDAntigravity, Path: compact}
	msgs, err := RecentMessages(c, 5)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Content != "complete untruncated user prompt" {
		t.Fatalf("got %+v, want untruncated content from transcript_full.jsonl", msgs)
	}
}
