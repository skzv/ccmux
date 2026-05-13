package claudeusage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTokens_AddAndTotal(t *testing.T) {
	a := Tokens{Input: 100, Output: 50, CacheCreation: 10, CacheRead: 5}
	b := Tokens{Input: 1, Output: 2, CacheCreation: 3, CacheRead: 4}
	a.Add(b)
	want := Tokens{Input: 101, Output: 52, CacheCreation: 13, CacheRead: 9}
	if a != want {
		t.Fatalf("Add: got %+v, want %+v", a, want)
	}
	if a.Total() != 101+52+13+9 {
		t.Errorf("Total mismatch: %d", a.Total())
	}
}

func TestPriceFor_KnownAndUnknownModels(t *testing.T) {
	cases := []struct {
		model           string
		wantInput       float64
		isOpus, isHaiku bool
	}{
		{"claude-opus-4-7", 15.0, true, false},
		{"claude-opus-4-6", 15.0, true, false},
		{"claude-sonnet-4-6", 3.0, false, false},
		{"claude-haiku-4-5", 1.0, false, true},
		{"some-future-model", 3.0, false, false}, // sonnet default
		{"", 3.0, false, false},
	}
	for _, tc := range cases {
		got := priceFor(tc.model)
		if got.Input != tc.wantInput {
			t.Errorf("priceFor(%q).Input = %v, want %v", tc.model, got.Input, tc.wantInput)
		}
		// Haiku should be cheapest, opus most expensive — sanity check.
		switch {
		case tc.isOpus && got.Input < priceFor("sonnet").Input:
			t.Errorf("opus should cost more than sonnet")
		case tc.isHaiku && got.Input > priceFor("sonnet").Input:
			t.Errorf("haiku should cost less than sonnet")
		}
	}
}

func TestIsFreshUserPrompt(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"plain string", `"hello"`, true},
		{"empty string still counts", `""`, true},
		{"text block", `[{"type":"text","text":"hi"}]`, true},
		{"mixed text+tool_result block", `[{"type":"tool_result"},{"type":"text"}]`, true},
		{"only tool_result", `[{"type":"tool_result","content":"x"}]`, false},
		{"two tool_results", `[{"type":"tool_result"},{"type":"tool_result"}]`, false},
		{"empty content", ``, false},
		{"weird shape — fail safe", `{"foo":"bar"}`, true},
		{"malformed array — fail safe", `[`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isFreshUserPrompt(json.RawMessage(tc.raw))
			if got != tc.want {
				t.Errorf("isFreshUserPrompt(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestTrimSpace(t *testing.T) {
	cases := []struct{ in, want string }{
		{"  hello  ", "hello"},
		{"\n\t x \r\n", "x"},
		{"", ""},
		{"   ", ""},
		{"abc", "abc"},
	}
	for _, tc := range cases {
		if got := string(trimSpace([]byte(tc.in))); got != tc.want {
			t.Errorf("trimSpace(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMaybeContains(t *testing.T) {
	if !maybeContains([]byte("hello world"), []byte("world")) {
		t.Error("should match substring")
	}
	if maybeContains([]byte("hello"), []byte("world")) {
		t.Error("should not match missing substring")
	}
	if maybeContains([]byte("hi"), []byte("")) {
		t.Error("empty needle should not match")
	}
	if maybeContains([]byte(""), []byte("x")) {
		t.Error("empty haystack should not match")
	}
}

func TestProjectFromEncoded(t *testing.T) {
	cases := []struct{ in, want string }{
		{"-Users-skz-Projects-ccmux", "ccmux"},
		{"-Users-skz-Projects-with-dashes", "dashes"}, // lossy: last segment only — that's why projectNameFromDir exists
		{"plainname", "plainname"},
		{"-", "-"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := projectFromEncoded(tc.in); got != tc.want {
			t.Errorf("projectFromEncoded(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestProjectNameFromDir_RecoversDashedNames(t *testing.T) {
	// Simulate Claude Code's project directory: encoded name on disk,
	// but the JSONL inside carries the real `cwd`.
	root := t.TempDir()
	dir := filepath.Join(root, "-Users-skz-Projects-my-plain-blog")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","cwd":"/Users/skz/Projects/my-plain-blog","timestamp":"2026-05-11T00:00:00Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := projectNameFromDir(dir); got != "my-plain-blog" {
		t.Fatalf("projectNameFromDir = %q, want \"my-plain-blog\" (would otherwise truncate to \"blog\")", got)
	}
}

func TestProjectNameFromDir_MissingDir(t *testing.T) {
	if got := projectNameFromDir("/no/such/dir"); got != "" {
		t.Fatalf("missing dir should return empty, got %q", got)
	}
}

func TestProjectNameFromDir_NoCwdInJSONL(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.jsonl"), []byte("{\"type\":\"system\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := projectNameFromDir(dir); got != "" {
		t.Fatalf("dir without cwd records should return empty, got %q", got)
	}
}

// TestWalk_DashedProjectNamesSurviveRoundTrip is the regression test
// for the dashboard's truncated "top projects" entries. Before this
// fix, `my-plain-blog` appeared as `blog`.
func TestWalk_DashedProjectNamesSurviveRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude", "projects", "-Users-skz-Projects-my-plain-blog")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ts := time.Now().Add(-15 * time.Minute).Format(time.RFC3339)
	f, err := os.Create(filepath.Join(dir, "s.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// Record contains cwd, so projectNameFromDir picks up the real basename.
	writeJSONL(t, f, map[string]any{"type": "user", "cwd": "/Users/skz/Projects/my-plain-blog", "timestamp": ts, "message": map[string]any{"content": "hi"}})
	// And the usage record so the aggregate has tokens to bucket.
	writeJSONL(t, f, map[string]any{
		"type": "assistant", "timestamp": ts, "cwd": "/Users/skz/Projects/my-plain-blog",
		"message": map[string]any{
			"role": "assistant", "model": "claude-sonnet-4-6",
			"usage": map[string]any{"input_tokens": 100, "output_tokens": 50, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0},
		},
	})
	f.Close()

	agg, err := Walk(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := agg.ByProject["my-plain-blog"]; !ok {
		got := make([]string, 0, len(agg.ByProject))
		for k := range agg.ByProject {
			got = append(got, k)
		}
		t.Fatalf("ByProject missing \"my-plain-blog\"; got keys %v", got)
	}
}

func TestHumanCount(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1.0K"},
		{1234, "1.2K"},
		{1_000_000, "1.00M"},
		{2_500_000, "2.50M"},
	}
	for _, tc := range cases {
		if got := HumanCount(tc.n); got != tc.want {
			t.Errorf("HumanCount(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

// writeJSONL is a small helper to build a transcript line.
func writeJSONL(t *testing.T, w *os.File, line map[string]any) {
	t.Helper()
	b, err := json.Marshal(line)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
}

func TestScanFile_CountsAssistantUsageAndUserPrompts(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "trans.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	inWindow := now.Add(-1 * time.Hour).Format(time.RFC3339)
	outOfWindow := now.Add(-200 * time.Hour).Format(time.RFC3339)

	// Assistant message inside the window.
	writeJSONL(t, f, map[string]any{
		"type":      "assistant",
		"timestamp": inWindow,
		"message": map[string]any{
			"role":  "assistant",
			"model": "claude-opus-4-7",
			"usage": map[string]any{
				"input_tokens":                100,
				"output_tokens":               50,
				"cache_creation_input_tokens": 10,
				"cache_read_input_tokens":     5,
			},
		},
	})
	// Assistant message OUTSIDE the window — should be skipped.
	writeJSONL(t, f, map[string]any{
		"type":      "assistant",
		"timestamp": outOfWindow,
		"message": map[string]any{
			"role":  "assistant",
			"model": "claude-opus-4-7",
			"usage": map[string]any{
				"input_tokens": 9999,
			},
		},
	})
	// User prompt inside window.
	writeJSONL(t, f, map[string]any{
		"type":      "user",
		"timestamp": inWindow,
		"message":   map[string]any{"role": "user", "content": "hello"},
	})
	// Tool-result follow-up — should NOT count toward UserPrompts.
	writeJSONL(t, f, map[string]any{
		"type":      "user",
		"timestamp": inWindow,
		"message": map[string]any{
			"role":    "user",
			"content": []map[string]any{{"type": "tool_result", "content": "x"}},
		},
	})
	f.Close()

	cutoff := now.Add(-12 * time.Hour)
	r := scanFile(path, cutoff)
	if r.assistantCount != 1 {
		t.Errorf("assistantCount = %d, want 1", r.assistantCount)
	}
	if r.userPrompts != 1 {
		t.Errorf("userPrompts = %d, want 1 (tool_result should be excluded)", r.userPrompts)
	}
	if r.total.Total() != 100+50+10+5 {
		t.Errorf("total = %d, want 165", r.total.Total())
	}
	if _, ok := r.byModel["claude-opus-4-7"]; !ok {
		t.Error("byModel missing opus entry")
	}
}

func TestScanFile_SkipsMalformedAndNonRelevantLines(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "trans.jsonl")
	body := "not json at all\n" +
		`{"type":"system","subtype":"summary"}` + "\n" + // no usage, no user-type — skipped pre-filter
		`{"type":"assistant","timestamp":"not-a-time","message":{"usage":{"input_tokens":10}}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	r := scanFile(path, time.Now().Add(-24*time.Hour))
	if r.assistantCount != 0 || r.userPrompts != 0 {
		t.Fatalf("expected no counts from malformed file: %+v", r)
	}
}

func TestWalk_MissingProjectsDirReturnsZero(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got, err := Walk(5 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got.Messages != 0 || got.UserPrompts != 0 || got.Total.Total() != 0 {
		t.Errorf("missing projects dir should be empty aggregate: %+v", got)
	}
}

func TestWalk_AggregatesAcrossProjects(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	projRoot := filepath.Join(home, ".claude", "projects")

	mkProject := func(name string, lines []map[string]any) {
		dir := filepath.Join(projRoot, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(filepath.Join(dir, "session.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		for _, line := range lines {
			writeJSONL(t, f, line)
		}
	}

	now := time.Now()
	ts := now.Add(-30 * time.Minute).Format(time.RFC3339)
	mkProject("-Users-skz-Projects-foo", []map[string]any{
		{"type": "assistant", "timestamp": ts, "message": map[string]any{
			"role": "assistant", "model": "claude-opus-4-7",
			"usage": map[string]any{"input_tokens": 100, "output_tokens": 50, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0},
		}},
		{"type": "user", "timestamp": ts, "message": map[string]any{"content": "hi"}},
	})
	mkProject("-Users-skz-Projects-bar", []map[string]any{
		{"type": "assistant", "timestamp": ts, "message": map[string]any{
			"role": "assistant", "model": "claude-sonnet-4-6",
			"usage": map[string]any{"input_tokens": 200, "output_tokens": 100, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0},
		}},
		{"type": "user", "timestamp": ts, "message": map[string]any{"content": "yo"}},
	})

	agg, err := Walk(5 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if agg.Messages != 2 || agg.UserPrompts != 2 {
		t.Errorf("counts: messages=%d prompts=%d (want 2/2)", agg.Messages, agg.UserPrompts)
	}
	if agg.Total.Input != 300 || agg.Total.Output != 150 {
		t.Errorf("totals wrong: %+v", agg.Total)
	}
	if len(agg.ByModel) != 2 {
		t.Errorf("ByModel = %v, want 2 entries", agg.ByModel)
	}
	if len(agg.ByProject) != 2 {
		t.Errorf("ByProject = %v, want 2 entries", agg.ByProject)
	}
	if _, ok := agg.ByProject["foo"]; !ok {
		t.Error("foo missing from ByProject")
	}
	if _, ok := agg.ByProject["bar"]; !ok {
		t.Error("bar missing from ByProject")
	}
}

func TestTopProjects_OrdersByTotalDesc(t *testing.T) {
	agg := &Aggregate{ByProject: map[string]*Tokens{
		"low":  {Input: 10},
		"high": {Input: 1000},
		"mid":  {Input: 100},
	}}
	got := agg.TopProjects(2)
	if len(got) != 2 || got[0].Project != "high" || got[1].Project != "mid" {
		t.Fatalf("TopProjects ordering: %v", got)
	}
	all := agg.TopProjects(0)
	if len(all) != 3 {
		t.Errorf("n=0 should return all, got %d", len(all))
	}
}

func TestResetAt(t *testing.T) {
	now := time.Now()
	agg := &Aggregate{FirstMessageInWindow: now.Add(-2 * time.Hour)}
	want := now.Add(-2 * time.Hour).Add(5 * time.Hour)
	if got := agg.ResetAt(5 * time.Hour); !got.Equal(want) {
		t.Errorf("ResetAt = %v, want %v", got, want)
	}
	empty := &Aggregate{}
	if got := empty.ResetAt(5 * time.Hour); !got.IsZero() {
		t.Errorf("empty aggregate should give zero time, got %v", got)
	}
}

func TestEstimatedCost(t *testing.T) {
	agg := &Aggregate{ByModel: map[string]*Tokens{
		"claude-opus-4-7": {Input: 1_000_000, Output: 1_000_000}, // 15 + 75 = 90
	}}
	got := agg.EstimatedCost()
	if got < 89.9 || got > 90.1 {
		t.Errorf("EstimatedCost = %v, want ~90", got)
	}
}

// TestWalk_HandlesLargeLines confirms the scanner buffer is wide enough
// to swallow JSONL lines that include cached system prompts. The default
// bufio scanner only allows 64KB; the implementation must raise it.
func TestWalk_HandlesLargeLines(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude", "projects", "-x")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	huge := make([]byte, 200*1024) // 200 KB padding
	for i := range huge {
		huge[i] = 'A'
	}
	ts := time.Now().Add(-5 * time.Minute).Format(time.RFC3339)
	line := fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"role":"assistant","model":"claude-sonnet-4-6","usage":{"input_tokens":1},"big":%q}}`, ts, string(huge))
	if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	agg, err := Walk(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if agg.Messages != 1 {
		t.Fatalf("large line dropped: messages=%d", agg.Messages)
	}
}
