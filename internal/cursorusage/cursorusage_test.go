package cursorusage

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// TestOpen_NotInstalled — when the database file does not exist,
// Open returns ErrNotInstalled (a sentinel) so the Agents Cursor
// sub-tab can render an empty-state placeholder instead of an error.
func TestOpen_NotInstalled(t *testing.T) {
	dir := t.TempDir()
	_, err := Open(filepath.Join(dir, "missing.db"))
	if !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("Open(missing) = %v, want ErrNotInstalled", err)
	}
}

// TestOpen_AggregatesFixture builds the fixture programmatically and
// asserts the Summary fields match what the queries should compute.
// Fixture rationale:
//
//   - 3 distinct conversations in ai_code_hashes.
//   - "claude-sonnet-4-6" used 5 times, "gpt-5" 3 times, "gemini-3-pro" 1.
//     Top-5 by count: sonnet, gpt-5, gemini-3-pro.
//   - 4 scored_commits: 2 inside the 7-day window, 2 outside.
//     In-window AI lines should be 30+40 = 70.
//   - The latest timestamp in ai_code_hashes is `now` (clock baseline).
func TestOpen_AggregatesFixture(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ai-code-tracking.db")
	now := time.Now().Truncate(time.Millisecond)
	seedFixture(t, dbPath, now)

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if s.Conversations != 3 {
		t.Errorf("Conversations = %d, want 3", s.Conversations)
	}
	wantModels := []string{"claude-sonnet-4-6", "gpt-5", "gemini-3-pro"}
	if !reflect.DeepEqual(s.Models, wantModels) {
		t.Errorf("Models = %v, want %v", s.Models, wantModels)
	}
	if s.AILinesLast7d != 70 {
		t.Errorf("AILinesLast7d = %d, want 70", s.AILinesLast7d)
	}
	if !s.LastActivity.Equal(now) {
		t.Errorf("LastActivity = %v, want %v", s.LastActivity, now)
	}
}

// TestOpen_EmptyTables — an empty database (schema present, no rows)
// returns a zero-valued Summary without error. This is the steady
// state for a freshly-installed Cursor.
func TestOpen_EmptyTables(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ai-code-tracking.db")
	createSchema(t, dbPath)

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if s.Conversations != 0 {
		t.Errorf("Conversations = %d, want 0", s.Conversations)
	}
	if len(s.Models) != 0 {
		t.Errorf("Models = %v, want empty", s.Models)
	}
	if s.AILinesLast7d != 0 {
		t.Errorf("AILinesLast7d = %d, want 0", s.AILinesLast7d)
	}
	if !s.LastActivity.IsZero() {
		t.Errorf("LastActivity = %v, want zero", s.LastActivity)
	}
}

// TestDefaultDBPath pins the canonical layout used by the Agents
// Cursor sub-tab. If Cursor relocates the file we want to find out
// here, not in production.
func TestDefaultDBPath(t *testing.T) {
	got := DefaultDBPath("/home/user")
	want := filepath.Join("/home/user", ".cursor", "ai-tracking", "ai-code-tracking.db")
	if got != want {
		t.Errorf("DefaultDBPath = %q, want %q", got, want)
	}
}

// createSchema builds an empty ai-tracking schema at dbPath. Split
// out from seedFixture so the empty-table case can reuse it.
func createSchema(t *testing.T, dbPath string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	stmts := []string{
		`CREATE TABLE ai_code_hashes (
			conversationId TEXT,
			model TEXT,
			timestamp INTEGER
		)`,
		`CREATE TABLE scored_commits (
			tabLinesAdded INTEGER,
			composerLinesAdded INTEGER,
			scoredAt INTEGER
		)`,
		`CREATE TABLE conversation_summaries (
			conversationId TEXT,
			title TEXT
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
}

// seedFixture writes a deterministic test corpus into dbPath. The
// timestamps are anchored on `now` so the 7-day window assertion is
// stable regardless of wall-clock drift.
func seedFixture(t *testing.T, dbPath string, now time.Time) {
	t.Helper()
	createSchema(t, dbPath)

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	hashes := []struct {
		conv  string
		model string
		ts    time.Time
	}{
		{"conv-a", "claude-sonnet-4-6", now.Add(-30 * time.Minute)},
		{"conv-a", "claude-sonnet-4-6", now.Add(-25 * time.Minute)},
		{"conv-a", "claude-sonnet-4-6", now.Add(-20 * time.Minute)},
		{"conv-a", "claude-sonnet-4-6", now.Add(-15 * time.Minute)},
		{"conv-a", "claude-sonnet-4-6", now.Add(-10 * time.Minute)},
		{"conv-b", "gpt-5", now.Add(-5 * time.Minute)},
		{"conv-b", "gpt-5", now.Add(-4 * time.Minute)},
		{"conv-b", "gpt-5", now.Add(-3 * time.Minute)},
		{"conv-c", "gemini-3-pro", now},
	}
	for _, h := range hashes {
		if _, err := db.Exec(`INSERT INTO ai_code_hashes (conversationId, model, timestamp) VALUES (?, ?, ?)`,
			h.conv, h.model, h.ts.UnixMilli()); err != nil {
			t.Fatalf("insert hash: %v", err)
		}
	}

	commits := []struct {
		tab, comp int
		at        time.Time
	}{
		{10, 20, now.Add(-1 * 24 * time.Hour)},
		{15, 25, now.Add(-3 * 24 * time.Hour)},
		// Outside the 7-day window — must NOT contribute to AILinesLast7d.
		{50, 50, now.Add(-30 * 24 * time.Hour)},
		{100, 100, now.Add(-90 * 24 * time.Hour)},
	}
	for _, c := range commits {
		if _, err := db.Exec(`INSERT INTO scored_commits (tabLinesAdded, composerLinesAdded, scoredAt) VALUES (?, ?, ?)`,
			c.tab, c.comp, c.at.UnixMilli()); err != nil {
			t.Fatalf("insert commit: %v", err)
		}
	}
}
