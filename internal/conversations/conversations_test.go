package conversations

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
)

// writeFile is a t.Helper that creates parent dirs and writes content.
// Tests are noisy without it because every fixture needs the same
// boilerplate.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestListClaude_ParsesTranscript covers the happy path: one
// well-formed transcript yields a Conversation with ID, project,
// preview, and a timestamp pulled from the embedded event (not just
// mtime, because event time is what the user sees as "when I last
// chatted").
func TestListClaude_ParsesTranscript(t *testing.T) {
	home := t.TempDir()
	tsRecent := "2026-04-30T10:00:00.000Z"
	tsLater := "2026-04-30T11:30:00.000Z"
	writeFile(t,
		filepath.Join(home, ".claude/projects/-Users-skz-Projects-foo/abc-123.jsonl"),
		// First user prompt is the preview source. Mix in non-user
		// events to ensure they're skipped. Later timestamp must win.
		`{"type":"permission-mode","permissionMode":"default"}`+"\n"+
			`{"type":"user","message":{"role":"user","content":"build the auth flow"},"timestamp":"`+tsRecent+`"}`+"\n"+
			`{"type":"assistant","message":{"role":"assistant","content":"sure"},"timestamp":"`+tsLater+`"}`+"\n",
	)

	got, err := ListClaude(home)
	if err != nil {
		t.Fatalf("ListClaude: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	c := got[0]
	if c.ID != "abc-123" {
		t.Errorf("ID = %q, want abc-123", c.ID)
	}
	if c.Agent != agent.IDClaude {
		t.Errorf("Agent = %q, want claude", c.Agent)
	}
	if c.Project != "/Users/skz/Projects/foo" {
		t.Errorf("Project = %q, want /Users/skz/Projects/foo", c.Project)
	}
	if c.Preview != "build the auth flow" {
		t.Errorf("Preview = %q, want %q", c.Preview, "build the auth flow")
	}
	// LastActivity must reflect the LATEST event, not the first.
	want, _ := time.Parse(time.RFC3339Nano, tsLater)
	if !c.LastActivity.Equal(want) {
		t.Errorf("LastActivity = %v, want %v (latest event)", c.LastActivity, want)
	}
}

// TestListClaude_MissingTree returns nil cleanly. A fresh install
// without ~/.claude must not error — the dashboard surfaces an
// "install hint" for that case.
func TestListClaude_MissingTree(t *testing.T) {
	got, err := ListClaude(t.TempDir())
	if err != nil {
		t.Fatalf("missing tree should not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d conversations, want 0", len(got))
	}
}

// TestListClaude_HandlesArrayContent — Claude sometimes encodes the
// user message as an array of {type, text} parts instead of a plain
// string. The parser must handle both and produce a sensible preview.
func TestListClaude_HandlesArrayContent(t *testing.T) {
	home := t.TempDir()
	writeFile(t,
		filepath.Join(home, ".claude/projects/-Users-skz-Projects-foo/sess.jsonl"),
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hello"},{"type":"text","text":"world"}]},"timestamp":"2026-04-30T10:00:00Z"}`+"\n",
	)
	got, _ := ListClaude(home)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Preview != "hello world" {
		t.Errorf("Preview = %q, want %q", got[0].Preview, "hello world")
	}
}

// TestListClaude_CwdFieldOverridesDecodedPath — a project path with
// hyphens (e.g. ~/Projects/auth-redesign) encodes to the same Claude
// directory name as a nested path (~/Projects/auth/redesign), so the
// decode is ambiguous. The cwd field on user events is the authoritative
// source: when present it must win over the decoded directory name. This
// is the fix for the "conversation ID can't be found" bug where claude
// was launched from the wrong directory.
func TestListClaude_CwdFieldOverridesDecodedPath(t *testing.T) {
	home := t.TempDir()
	// Directory name decodes to /Users/skz/Projects/auth/redesign (wrong).
	// Transcript cwd says /Users/skz/Projects/auth-redesign (right).
	writeFile(t,
		filepath.Join(home, ".claude/projects/-Users-skz-Projects-auth-redesign/sess.jsonl"),
		`{"type":"user","cwd":"/Users/skz/Projects/auth-redesign","message":{"role":"user","content":"hello"},"timestamp":"2026-04-30T10:00:00Z"}`+"\n",
	)
	got, err := ListClaude(home)
	if err != nil {
		t.Fatalf("ListClaude: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Project != "/Users/skz/Projects/auth-redesign" {
		t.Errorf("Project = %q, want /Users/skz/Projects/auth-redesign (cwd from transcript)", got[0].Project)
	}
}

// TestListClaude_TolerantToBadLines — a corrupt or partial JSONL line
// must NOT break the rest of the transcript scan. We've seen these in
// the wild (claude killed mid-write).
func TestListClaude_TolerantToBadLines(t *testing.T) {
	home := t.TempDir()
	writeFile(t,
		filepath.Join(home, ".claude/projects/-Users-skz-Projects-foo/sess.jsonl"),
		`{"type":"permission-mode"`+"\n"+ // truncated
			`not even json`+"\n"+
			`{"type":"user","message":{"role":"user","content":"good prompt"},"timestamp":"2026-04-30T10:00:00Z"}`+"\n",
	)
	got, _ := ListClaude(home)
	if len(got) != 1 || got[0].Preview != "good prompt" {
		t.Errorf("bad lines should be skipped, got: %+v", got)
	}
}

// TestListCodex_ParsesFilename — Codex's filename is the load-bearing
// part: `rollout-<RFC3339-ish>-<uuid>.jsonl`. The UUID is the last
// five dash-separated chunks. Without this parse, every Codex row
// would have an empty ID and the resume button wouldn't work.
func TestListCodex_ParsesFilename(t *testing.T) {
	home := t.TempDir()
	fname := "rollout-2026-05-06T13-48-09-019dff0c-4b4d-7830-af27-408791f87129.jsonl"
	writeFile(t,
		filepath.Join(home, ".codex/sessions/2026/05/06", fname),
		`{"type":"session_meta","cwd":"/Users/skz/Projects/bar"}`+"\n"+
			`{"type":"user_message","text":"refactor the parser"}`+"\n",
	)
	got, err := ListCodex(home)
	if err != nil {
		t.Fatalf("ListCodex: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	c := got[0]
	if c.ID != "019dff0c-4b4d-7830-af27-408791f87129" {
		t.Errorf("ID = %q, want the UUID portion", c.ID)
	}
	if c.Agent != agent.IDCodex {
		t.Errorf("Agent = %q, want codex", c.Agent)
	}
	if c.Project != "/Users/skz/Projects/bar" {
		t.Errorf("Project = %q, want /Users/skz/Projects/bar", c.Project)
	}
	if c.Preview != "refactor the parser" {
		t.Errorf("Preview = %q, want 'refactor the parser'", c.Preview)
	}
}

// TestListCodex_MissingTree — same fresh-install tolerance as Claude.
func TestListCodex_MissingTree(t *testing.T) {
	got, err := ListCodex(t.TempDir())
	if err != nil {
		t.Fatalf("missing tree should not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d, want 0", len(got))
	}
}

// TestListAntigravity_ListsPBFiles — Antigravity stores conversations
// as opaque .pb files. We can't read them, but the filename is the
// UUID and mtime is a useful surrogate for "last activity." The
// Preview field stays empty by design.
func TestListAntigravity_ListsPBFiles(t *testing.T) {
	home := t.TempDir()
	writeFile(t,
		filepath.Join(home, ".gemini/antigravity-cli/conversations/9d34d057-0ba1-4e24-b610-cff3994fb631.pb"),
		"opaque protobuf bytes",
	)
	// Make the file noticeably old so we can verify the mtime survives.
	old := time.Now().Add(-24 * time.Hour)
	_ = os.Chtimes(
		filepath.Join(home, ".gemini/antigravity-cli/conversations/9d34d057-0ba1-4e24-b610-cff3994fb631.pb"),
		old, old,
	)

	got, err := ListAntigravity(home)
	if err != nil {
		t.Fatalf("ListAntigravity: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	c := got[0]
	if c.ID != "9d34d057-0ba1-4e24-b610-cff3994fb631" {
		t.Errorf("ID = %q, want UUID from filename", c.ID)
	}
	if c.Agent != agent.IDAntigravity {
		t.Errorf("Agent = %q, want antigravity", c.Agent)
	}
	if c.Preview != "" {
		t.Errorf("Preview = %q, want empty (can't parse pb)", c.Preview)
	}
	// mtime must round-trip — used by All() for sort order.
	if !c.LastActivity.Equal(old.Truncate(time.Second)) && c.LastActivity.Sub(old).Abs() > 2*time.Second {
		t.Errorf("LastActivity = %v, want ~%v (mtime)", c.LastActivity, old)
	}
}

// TestAll_SortsByRecency — the unified list returns most-recent first
// across all three agents. A regression here would scatter today's
// conversation behind yesterday's stale ones in the picker.
func TestAll_SortsByRecency(t *testing.T) {
	home := t.TempDir()
	// Three transcripts with different times.
	now := time.Now()
	yesterday := now.Add(-24 * time.Hour)
	lastWeek := now.Add(-7 * 24 * time.Hour)

	writeFile(t,
		filepath.Join(home, ".claude/projects/-x/old.jsonl"),
		`{"type":"user","message":{"role":"user","content":"week-old"},"timestamp":"`+lastWeek.UTC().Format(time.RFC3339Nano)+`"}`+"\n",
	)
	writeFile(t,
		filepath.Join(home, ".codex/sessions/2026/05/06/rollout-2026-05-06T13-48-09-aaaa-bbbb-cccc-dddd-eeeeeeeeeeee.jsonl"),
		`{"type":"user_message","text":"yesterday"}`+"\n",
	)
	codexPath := filepath.Join(home, ".codex/sessions/2026/05/06/rollout-2026-05-06T13-48-09-aaaa-bbbb-cccc-dddd-eeeeeeeeeeee.jsonl")
	_ = os.Chtimes(codexPath, yesterday, yesterday)
	writeFile(t,
		filepath.Join(home, ".gemini/antigravity-cli/conversations/now.pb"),
		"recent",
	)
	// Touch to "now" explicitly so the test isn't sensitive to filesystem
	// timestamp granularity.
	_ = os.Chtimes(filepath.Join(home, ".gemini/antigravity-cli/conversations/now.pb"), now, now)

	got, err := All(Options{HomeDir: home})
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (got: %+v)", len(got), got)
	}
	if got[0].Agent != agent.IDAntigravity {
		t.Errorf("[0] agent = %q, want antigravity (most recent)", got[0].Agent)
	}
	if got[1].Agent != agent.IDCodex {
		t.Errorf("[1] agent = %q, want codex", got[1].Agent)
	}
	if got[2].Agent != agent.IDClaude {
		t.Errorf("[2] agent = %q, want claude (oldest)", got[2].Agent)
	}
}

// TestAll_LimitCapsResults — the dashboard's recent-conversations
// panel passes Limit=5; the full Conversations screen passes 0. The
// limit must apply AFTER sorting so the user gets the 5 most recent,
// not 5 arbitrary rows.
func TestAll_LimitCapsResults(t *testing.T) {
	home := t.TempDir()
	for i := 0; i < 7; i++ {
		writeFile(t,
			filepath.Join(home, ".gemini/antigravity-cli/conversations/", "conv"+string(rune('a'+i))+".pb"),
			"x",
		)
	}
	got, _ := All(Options{HomeDir: home, Limit: 3})
	if len(got) != 3 {
		t.Errorf("len = %d, want 3 (limit)", len(got))
	}
}

// TestAll_SinceFiltersStale — Conversations older than Since are
// dropped. Useful for the dashboard's "recent" panel (Since=24h) so
// the user sees today's work, not last year's archived sessions.
func TestAll_SinceFiltersStale(t *testing.T) {
	home := t.TempDir()
	// One recent, one ancient.
	writeFile(t, filepath.Join(home, ".gemini/antigravity-cli/conversations/recent.pb"), "x")
	writeFile(t, filepath.Join(home, ".gemini/antigravity-cli/conversations/ancient.pb"), "x")
	ancient := time.Now().Add(-30 * 24 * time.Hour)
	_ = os.Chtimes(filepath.Join(home, ".gemini/antigravity-cli/conversations/ancient.pb"), ancient, ancient)

	got, _ := All(Options{HomeDir: home, Since: 7 * 24 * time.Hour})
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1 (only recent passes 7-day filter)", len(got))
	}
	if got[0].ID != "recent" {
		t.Errorf("kept the wrong one: %+v", got[0])
	}
}

// TestResumeArgs_AgentDialects pins the per-agent CLI shape. A regression
// here would mean clicking "resume" on a Codex row tries `codex --resume`
// (which doesn't exist; codex uses positional `resume <id>`).
func TestResumeArgs_AgentDialects(t *testing.T) {
	cases := []struct {
		agent agent.ID
		want  []string
	}{
		{agent.IDClaude, []string{"claude", "--resume", "u-1"}},
		{agent.IDCodex, []string{"codex", "resume", "u-1"}},
		{agent.IDAntigravity, []string{"agy", "--conversation", "u-1"}},
		{agent.ID("imaginary"), nil},
	}
	for _, tc := range cases {
		t.Run(string(tc.agent), func(t *testing.T) {
			got := Conversation{ID: "u-1", Agent: tc.agent}.ResumeArgs()
			if !equalStringSlice(got, tc.want) {
				t.Errorf("ResumeArgs = %v, want %v", got, tc.want)
			}
		})
	}
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestTruncatedPreview_CollapsesWhitespace — list rows are single-line
// by design; a multi-paragraph user prompt should collapse to one
// readable summary. Length cap also pinned here.
func TestTruncatedPreview_CollapsesWhitespace(t *testing.T) {
	in := "  hello\n\nworld\t\tthis is\n   a long  prompt  "
	out := truncatedPreview(in)
	if strings.Contains(out, "\n") || strings.Contains(out, "\t") {
		t.Errorf("preview contains whitespace newlines/tabs: %q", out)
	}
	if !strings.HasPrefix(out, "hello world") {
		t.Errorf("preview lost the first words: %q", out)
	}
}

func TestTruncatedPreview_LengthCap(t *testing.T) {
	in := strings.Repeat("a", 500)
	out := truncatedPreview(in)
	// Cap is in runes (display width), not bytes — the trailing "…"
	// is a multi-byte rune so len(out) in bytes is naturally a bit
	// higher than the rune count.
	if got := len([]rune(out)); got > 120 {
		t.Errorf("preview not capped: runes=%d", got)
	}
	if !strings.HasSuffix(out, "…") {
		t.Errorf("long preview should be ellipsized: %q", out)
	}
}

// TestDelete_RemovesClaudeTranscript — the happy path. A Claude
// transcript under ~/.claude/projects is removed; ListClaude no
// longer returns it afterward.
func TestDelete_RemovesClaudeTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".claude/projects/-Users-skz-Projects-foo/abc-123.jsonl")
	writeFile(t, path,
		`{"type":"user","message":{"role":"user","content":"hi"},"timestamp":"2026-04-30T10:00:00Z"}`+"\n")

	c := Conversation{ID: "abc-123", Agent: agent.IDClaude, Path: path}
	if err := Delete(c); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("transcript still exists after Delete: stat err = %v", err)
	}
	got, _ := ListClaude(home)
	if len(got) != 0 {
		t.Errorf("ListClaude still returns %d conversations after delete", len(got))
	}
}

// TestDelete_RemovesAntigravityPB — Antigravity transcripts are .pb
// files under a different root; Delete must handle that agent's
// path + extension too.
func TestDelete_RemovesAntigravityPB(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".gemini/antigravity-cli/conversations/xyz.pb")
	writeFile(t, path, "opaque")

	c := Conversation{ID: "xyz", Agent: agent.IDAntigravity, Path: path}
	if err := Delete(c); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("pb file still exists after Delete")
	}
}

// TestDelete_RejectsPathOutsideRoot — the safety guard. A Conversation
// whose Path points outside the agent's transcript root must be
// refused, NOT deleted. This is what stops a corrupted or hand-crafted
// Conversation from turning Delete into an arbitrary `rm`.
func TestDelete_RejectsPathOutsideRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// A file the test owns, but NOT under any transcript root.
	victim := filepath.Join(home, "important.txt")
	writeFile(t, victim, "do not delete me")

	c := Conversation{ID: "evil", Agent: agent.IDClaude, Path: victim}
	err := Delete(c)
	if err == nil {
		t.Fatal("Delete accepted a path outside the transcript root")
	}
	if _, statErr := os.Stat(victim); statErr != nil {
		t.Errorf("guard failed — the out-of-root file was deleted: %v", statErr)
	}
}

// TestDelete_RejectsTraversalEscape — `..` segments must not let a
// path climb out of the transcript root. filepath.Clean collapses
// them before the prefix check, so an escape attempt is caught.
func TestDelete_RejectsTraversalEscape(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	victim := filepath.Join(home, "secret.txt")
	writeFile(t, victim, "secret")

	// Path that, uncleaned, looks like it's under .claude/projects but
	// climbs back out via `..`.
	sneaky := filepath.Join(home, ".claude/projects", "..", "..", "secret.txt")
	c := Conversation{ID: "x", Agent: agent.IDClaude, Path: sneaky}
	if err := Delete(c); err == nil {
		t.Error("Delete accepted a `..` traversal path")
	}
	if _, err := os.Stat(victim); err != nil {
		t.Errorf("traversal guard failed — secret.txt was deleted: %v", err)
	}
}

// TestDelete_RejectsWrongExtension — a path under the right root but
// with the wrong extension (e.g. a stray .txt in ~/.claude/projects)
// is refused. Defense against deleting a non-transcript file that
// happens to live in the tree.
func TestDelete_RejectsWrongExtension(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	notATranscript := filepath.Join(home, ".claude/projects/-x/notes.txt")
	writeFile(t, notATranscript, "x")

	c := Conversation{ID: "x", Agent: agent.IDClaude, Path: notATranscript}
	if err := Delete(c); err == nil {
		t.Error("Delete accepted a non-.jsonl file under the Claude root")
	}
	if _, err := os.Stat(notATranscript); err != nil {
		t.Errorf("extension guard failed — notes.txt was deleted: %v", err)
	}
}

// TestDelete_EmptyPath — a Conversation with no Path can't be deleted;
// Delete must error rather than attempt os.Remove("").
func TestDelete_EmptyPath(t *testing.T) {
	if err := Delete(Conversation{ID: "x", Agent: agent.IDClaude}); err == nil {
		t.Error("Delete on an empty-path conversation should error")
	}
}

// TestDelete_UnknownAgent — an unrecognized agent has no transcript
// root, so Delete can't validate the path and must refuse.
func TestDelete_UnknownAgent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, "somewhere.jsonl")
	writeFile(t, path, "x")
	c := Conversation{ID: "x", Agent: agent.ID("imaginary"), Path: path}
	if err := Delete(c); err == nil {
		t.Error("Delete on an unknown agent should error")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("unknown-agent guard failed — file was deleted: %v", err)
	}
}
