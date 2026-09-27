package agentusage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
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

// TestWalk_UndatedRecordsFollowTheirNeighbours — an undated record is
// as old as the dated record before it. A long-running transcript that
// was touched recently must not have its whole undated history counted
// as "in the last 5 hours".
func TestWalk_UndatedRecordsFollowTheirNeighbours(t *testing.T) {
	root := t.TempDir()
	old := time.Now().Add(-10 * time.Hour).UTC().Format(time.RFC3339)
	recent := time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339)
	write(t, filepath.Join(root, "s.jsonl"),
		`{"role":"user","content":"undated, before anything old"}`,
		`{"role":"user","timestamp":"`+old+`"}`,
		`{"role":"assistant","usage":{"input_tokens":1000,"output_tokens":1000}}`, // undated, old
		`{"role":"user","timestamp":"`+recent+`"}`,
		`{"role":"assistant","usage":{"input_tokens":7,"output_tokens":3}}`, // undated, recent
	)
	s, err := Walk(root, 5*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if s.Prompts != 1 || s.InputTokens != 7 || s.OutputTokens != 3 {
		t.Errorf("got prompts=%d in=%d out=%d, want 1/7/3", s.Prompts, s.InputTokens, s.OutputTokens)
	}
}

// TestWalk_FullyUndatedFileCountsWhole — agents that never write
// timestamps still show up (the file mtime is the only clock).
func TestWalk_FullyUndatedFileCountsWhole(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "s.jsonl"),
		`{"role":"user"}`,
		`{"role":"assistant","usage":{"input_tokens":4,"output_tokens":2}}`,
	)
	s, err := Walk(root, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if s.Prompts != 1 || s.InputTokens != 4 {
		t.Errorf("got prompts=%d in=%d, want 1/4", s.Prompts, s.InputTokens)
	}
}

// TestWalk_LenientFieldTypes — regression: timestamp/time/created_at
// and role/type were typed as strings, so a line with a numeric epoch
// timestamp (or an object under `time`) failed to unmarshal and its
// tokens were silently dropped. Epoch seconds and milliseconds (number
// or numeric string) now place the record in time; other shapes leave
// it undated; a mistyped token field doesn't sink the rest of the line.
func TestWalk_LenientFieldTypes(t *testing.T) {
	now := time.Now()
	recent := now.Add(-10 * time.Minute)
	old := now.Add(-10 * time.Hour)
	sec := strconv.FormatInt(recent.Unix(), 10)
	ms := strconv.FormatInt(recent.UnixMilli(), 10)
	oldMS := strconv.FormatInt(old.UnixMilli(), 10)
	root := t.TempDir()
	write(t, filepath.Join(root, "s.jsonl"),
		`{"role":"user","timestamp":`+sec+`}`,
		`{"role":"assistant","timestamp":`+sec+`.5,"usage":{"input_tokens":1,"output_tokens":1}}`,
		`{"role":"assistant","created_at":`+ms+`,"usage":{"input_tokens":2,"output_tokens":2}}`,
		`{"role":"assistant","time":"`+ms+`","usage":{"input_tokens":4,"output_tokens":4}}`,
		`{"role":"assistant","time":{"created":`+ms+`},"type":{"kind":"reply"},"usage":{"input_tokens":8,"output_tokens":8}}`,
		`{"role":"assistant","usage":{"input_tokens":16,"output_tokens":16},"prompt_tokens":"n/a"}`,
	)
	write(t, filepath.Join(root, "old.jsonl"),
		`{"role":"user","timestamp":`+oldMS+`}`,
		`{"role":"assistant","timestamp":`+oldMS+`,"usage":{"input_tokens":1000,"output_tokens":1000}}`,
	)
	s, err := Walk(root, 5*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if s.Prompts != 1 || s.InputTokens != 31 || s.OutputTokens != 31 {
		t.Errorf("got prompts=%d in=%d out=%d, want 1/31/31", s.Prompts, s.InputTokens, s.OutputTokens)
	}
}

func TestParseWhen(t *testing.T) {
	want := time.Date(2026, 5, 12, 23, 17, 43, 0, time.UTC)
	for raw, ok := range map[string]bool{
		`"2026-05-12T23:17:43Z"`:     true,
		`"2026-05-12T23:17:43.000Z"`: true,
		`1778627863`:                 true,
		`1778627863000`:              true,
		`"1778627863000"`:            true,
		`""`:                         false,
		`null`:                       false,
		`0`:                          false,
		`-5`:                         false,
		`{"created":1}`:              false,
		`"yesterday"`:                false,
		`true`:                       false,
	} {
		got, gotOK := parseWhen(json.RawMessage(raw))
		if gotOK != ok || (ok && !got.Equal(want)) {
			t.Errorf("parseWhen(%s) = %v, %v; want ok=%v", raw, got, gotOK, ok)
		}
	}
}
