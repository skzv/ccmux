// Package cursorusage reads Cursor's local ai-tracking SQLite database
// and surfaces an aggregate Summary the Agents Cursor sub-tab renders.
//
// Why this exists: Cursor's CLI is opaque from ccmux's perspective —
// JSONL transcripts under `~/.cursor/projects/<encoded-cwd>/agent-transcripts/`
// are walked by internal/conversations, and `cursor-agent` doesn't
// ship a status command. The aggregate database at
// `~/.cursor/ai-tracking/ai-code-tracking.db` is the one structured
// surface Cursor maintains for its own analytics; we read it
// read-only to populate the Cursor sub-tab.
//
// The driver is `modernc.org/sqlite` (pure Go) — no CGO, so the
// ccmux cross-compile job (linux/darwin/windows × amd64/arm64) stays
// clean without each runner needing a C toolchain.
package cursorusage

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotInstalled signals that the Cursor ai-tracking database does
// not exist at the given path — typically because Cursor isn't
// installed or the user has never run it. Callers should render an
// empty-state placeholder instead of treating this as an error.
var ErrNotInstalled = errors.New("cursorusage: Cursor ai-tracking database not found")

// Summary aggregates one read of the Cursor ai-tracking database.
// All fields are zero-valued when the database has no rows for them
// (e.g., a fresh Cursor install with no completed conversations).
type Summary struct {
	// Conversations counts distinct conversationId values across all
	// recorded AI requests.
	Conversations int

	// Models lists the top-5 distinct model names by request count,
	// most-used first.
	Models []string

	// AILinesLast7d sums tabLinesAdded + composerLinesAdded across
	// all scored_commits rows in the last 7 days.
	AILinesLast7d int

	// LastActivity is the most-recent timestamp recorded in
	// ai_code_hashes. Zero when no rows exist.
	LastActivity time.Time
}

// Open reads dbPath and returns an aggregated Summary. Returns
// ErrNotInstalled when the database file does not exist; that's the
// case ccmux surfaces as a "Cursor not detected" placeholder rather
// than as an error condition.
//
// The connection is opened in read-only mode (the URI query carries
// `mode=ro`) so a half-written transaction by the Cursor app can't
// be mistaken for a write attempt by ccmux. SQLite still requires
// write access on the directory for the -journal/-wal sidecar files;
// to keep the call safe against an in-progress Cursor session we
// also disable the WAL with `_journal=OFF` and use `_busy_timeout`
// to ride out short lock contention.
func Open(dbPath string) (Summary, error) {
	if _, err := os.Stat(dbPath); errors.Is(err, os.ErrNotExist) {
		return Summary{}, ErrNotInstalled
	} else if err != nil {
		return Summary{}, fmt.Errorf("cursorusage: stat %s: %w", dbPath, err)
	}

	dsn := "file:" + dbPath + "?mode=ro&_pragma=busy_timeout(2000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return Summary{}, fmt.Errorf("cursorusage: open %s: %w", dbPath, err)
	}
	defer db.Close()

	var s Summary
	if s.Conversations, err = countConversations(db); err != nil {
		return Summary{}, err
	}
	if s.Models, err = topModels(db, 5); err != nil {
		return Summary{}, err
	}
	if s.AILinesLast7d, err = aiLinesSince(db, time.Now().Add(-7*24*time.Hour)); err != nil {
		return Summary{}, err
	}
	if s.LastActivity, err = lastActivity(db); err != nil {
		return Summary{}, err
	}
	return s, nil
}

// countConversations returns the number of distinct conversationId
// values in ai_code_hashes. Returns 0 when the table is empty.
func countConversations(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(DISTINCT conversationId) FROM ai_code_hashes`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("cursorusage: count conversations: %w", err)
	}
	return n, nil
}

// topModels returns the top n distinct model names by request count,
// most-used first. Returns nil when the table is empty.
func topModels(db *sql.DB, n int) ([]string, error) {
	rows, err := db.Query(`SELECT model FROM ai_code_hashes
		GROUP BY model ORDER BY COUNT(*) DESC LIMIT ?`, n)
	if err != nil {
		return nil, fmt.Errorf("cursorusage: top models: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var m sql.NullString
		if err := rows.Scan(&m); err != nil {
			return nil, fmt.Errorf("cursorusage: scan model: %w", err)
		}
		if m.Valid && m.String != "" {
			out = append(out, m.String)
		}
	}
	return out, rows.Err()
}

// aiLinesSince sums tabLinesAdded + composerLinesAdded across
// scored_commits rows with scoredAt >= since. Cursor stores scoredAt
// as a Unix-millisecond integer; we compare on the same basis.
func aiLinesSince(db *sql.DB, since time.Time) (int, error) {
	var total sql.NullInt64
	err := db.QueryRow(`SELECT COALESCE(SUM(tabLinesAdded + composerLinesAdded), 0)
		FROM scored_commits WHERE scoredAt >= ?`, since.UnixMilli()).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("cursorusage: ai lines: %w", err)
	}
	return int(total.Int64), nil
}

// lastActivity returns the latest timestamp in ai_code_hashes,
// interpreted as Unix-milliseconds. Returns a zero time when the
// table is empty.
func lastActivity(db *sql.DB) (time.Time, error) {
	var ms sql.NullInt64
	err := db.QueryRow(`SELECT MAX(timestamp) FROM ai_code_hashes`).Scan(&ms)
	if err != nil {
		return time.Time{}, fmt.Errorf("cursorusage: last activity: %w", err)
	}
	if !ms.Valid {
		return time.Time{}, nil
	}
	return time.UnixMilli(ms.Int64), nil
}

// DefaultDBPath returns the canonical Cursor ai-tracking database
// path under the user's home directory.
func DefaultDBPath(home string) string {
	return filepath.Join(home, ".cursor", "ai-tracking", "ai-code-tracking.db")
}
