package conversations

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/agenthome"
	"github.com/skzv/ccmux/internal/jsonl"
)

// antigravityStep is one line from Antigravity CLI's
// ~/.gemini/antigravity-cli/brain/<uuid>/.system_generated/logs/transcript.jsonl
// (or transcript_full.jsonl).
type antigravityStep struct {
	StepIndex       int      `json:"step_index"`
	Source          string   `json:"source"`
	Type            string   `json:"type"`
	Status          string   `json:"status"`
	CreatedAt       string   `json:"created_at"`
	Content         string   `json:"content"`
	TruncatedFields []string `json:"truncated_fields"`
}

// antigravitySummaryRow mirrors the metadata columns we read out of
// ~/.gemini/antigravity-cli/conversation_summaries.db.
type antigravitySummaryRow struct {
	ID           string
	Title        string
	Preview      string
	LastModified time.Time
	Project      string
	Subagent     bool
}

// ListAntigravity enumerates Antigravity CLI conversations by combining:
//
//  1. ~/.gemini/antigravity-cli/conversation_summaries.db (SQLite index
//     with titles, previews, workspace_uris, timestamps, and parent IDs),
//  2. ~/.gemini/antigravity-cli/brain/<uuid>/.system_generated/logs/transcript.jsonl
//     (plain JSONL turns, readable even when the summary DB is absent), and
//  3. ~/.gemini/antigravity-cli/conversations/<uuid>.pb (fallback for older
//     installs and hermetic fixtures that only have .pb files).
//
// Each source is optional so a partial install or locked SQLite file
// degrades cleanly rather than hiding the user's history.
func ListAntigravity(home string) ([]Conversation, error) {
	byID := map[string]*Conversation{}
	ensure := func(id string) *Conversation {
		if c, ok := byID[id]; ok {
			return c
		}
		c := &Conversation{
			ID:    id,
			Agent: agent.IDAntigravity,
		}
		byID[id] = c
		return c
	}

	// 1. Discover .pb transcripts (primary Path for backward-compatible delete).
	pbRoot := agenthome.AntigravityConversations(home)
	if entries, err := os.ReadDir(pbRoot); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".pb") {
				continue
			}
			id := strings.TrimSuffix(e.Name(), ".pb")
			if id == "" {
				continue
			}
			path := filepath.Join(pbRoot, e.Name())
			c := ensure(id)
			c.Path = path
			if info, err := e.Info(); err == nil {
				c.LastActivity = info.ModTime()
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", pbRoot, err)
	}

	// 2. Discover plain JSONL transcripts under brain/<uuid>/.system_generated/logs/.
	brainRoot := agenthome.AntigravityBrain(home)
	if entries, err := os.ReadDir(brainRoot); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			id := e.Name()
			if id == "" || strings.HasPrefix(id, ".") {
				continue
			}
			tPath := agenthome.AntigravityTranscript(home, id)
			info, err := os.Stat(tPath)
			if err != nil || info.IsDir() {
				continue
			}
			c := ensure(id)
			if c.Path == "" {
				c.Path = tPath
			} else if c.Path != tPath {
				c.Paths = appendUniquePath(c.Paths, tPath)
			}
			if info.ModTime().After(c.LastActivity) {
				c.LastActivity = info.ModTime()
			}
		}
	}

	// 3. Enrich from conversation_summaries.db when present.
	if rows, err := readAntigravitySummaryDB(agenthome.AntigravitySummaryDB(home)); err == nil {
		for _, r := range rows {
			// Skip DB-only rows that have neither a .pb nor a transcript file
			// on disk, unless they carry a real title/preview and project so
			// live sessions still show up.
			c, exists := byID[r.ID]
			if !exists {
				if r.Title == "" && r.Preview == "" {
					continue
				}
				c = ensure(r.ID)
			}
			if r.Project != "" && c.Project == "" {
				c.Project = r.Project
			}
			if c.Preview == "" {
				if p := truncatedPreview(r.Title); p != "" {
					c.Preview = p
				} else if p := truncatedPreview(r.Preview); p != "" {
					c.Preview = p
				}
			}
			if r.LastModified.After(c.LastActivity) {
				c.LastActivity = r.LastModified
			}
			if r.Subagent {
				c.Subagent = true
			}
		}
	}

	// 4. For any conversation that has a transcript.jsonl on disk but still
	// lacks a preview (e.g. DB absent or new conversation before summary flush),
	// extract the first user turn and latest timestamp from the JSONL.
	out := make([]Conversation, 0, len(byID))
	for _, c := range byID {
		if tPath := antigravityJSONLPath(*c); tPath != "" {
			if c.Preview == "" || c.LastActivity.IsZero() {
				if prev, lastTS := scanAntigravityTranscriptMeta(tPath); prev != "" || !lastTS.IsZero() {
					if c.Preview == "" {
						c.Preview = prev
					}
					if lastTS.After(c.LastActivity) {
						c.LastActivity = lastTS
					}
				}
			}
		}
		out = append(out, *c)
	}
	return out, nil
}

func appendUniquePath(paths []string, p string) []string {
	for _, existing := range paths {
		if existing == p {
			return paths
		}
	}
	return append(paths, p)
}

func readAntigravitySummaryDB(dbPath string) ([]antigravitySummaryRow, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, err
	}
	slashPath := filepath.ToSlash(dbPath)
	if !strings.HasPrefix(slashPath, "/") {
		slashPath = "/" + slashPath
	}
	dsn := "file:" + (&url.URL{Path: slashPath}).EscapedPath() + "?mode=ro&_pragma=busy_timeout(2000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT conversation_id, title, preview, last_modified_time, workspace_uris, parent_conversation_id, nesting_depth
		FROM conversation_summaries
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []antigravitySummaryRow
	for rows.Next() {
		var (
			id           string
			title        sql.NullString
			preview      sql.NullString
			rawTime      any
			rawURIs      sql.NullString
			parentID     sql.NullString
			nestingDepth sql.NullInt64
		)
		if err := rows.Scan(&id, &title, &preview, &rawTime, &rawURIs, &parentID, &nestingDepth); err != nil {
			continue
		}
		if id == "" {
			continue
		}
		out = append(out, antigravitySummaryRow{
			ID:           id,
			Title:        strings.TrimSpace(title.String),
			Preview:      strings.TrimSpace(preview.String),
			LastModified: parseAntigravitySQLiteTime(rawTime),
			Project:      parseAntigravityWorkspaceURIs(rawURIs.String),
			Subagent:     strings.TrimSpace(parentID.String) != "" || nestingDepth.Int64 > 0,
		})
	}
	return out, rows.Err()
}

// parseAntigravityWorkspaceURIs extracts the first filesystem path from
// Antigravity's JSON-encoded workspace_uris column, e.g.
// `["file:///Users/skz/Projects/ccmux"]`.
func parseAntigravityWorkspaceURIs(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var uris []string
	if err := json.Unmarshal([]byte(raw), &uris); err != nil || len(uris) == 0 {
		return ""
	}
	for _, u := range uris {
		if p := fileURIToPath(u); p != "" {
			return p
		}
	}
	return ""
}

func fileURIToPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.HasPrefix(raw, "file://") {
		if filepath.IsAbs(raw) {
			return filepath.Clean(raw)
		}
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	p := u.Path
	if runtime.GOOS == "windows" && len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	if p == "" {
		return ""
	}
	return filepath.Clean(filepath.FromSlash(p))
}

func parseAntigravitySQLiteTime(v any) time.Time {
	switch t := v.(type) {
	case time.Time:
		return t
	case string:
		return parseAntigravityTimestamp(t)
	case []byte:
		return parseAntigravityTimestamp(string(t))
	default:
		return time.Time{}
	}
}

func parseAntigravityTimestamp(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	} {
		if ts, err := time.Parse(layout, s); err == nil {
			return ts
		}
	}
	return time.Time{}
}

// antigravityJSONLPath finds the readable JSONL transcript path on c, if
// any. A legacy .pb-only Conversation returns "" so callers degrade to
// empty messages / 0 count without erroring.
func antigravityJSONLPath(c Conversation) string {
	for _, p := range transcriptPaths(c) {
		if strings.HasSuffix(p, ".jsonl") {
			return p
		}
	}
	return ""
}

// preferFullTranscriptPath swaps transcript.jsonl for the sibling
// transcript_full.jsonl when it exists, so message previews aren't
// truncated on long steps.
func preferFullTranscriptPath(path string) string {
	if filepath.Base(path) != "transcript.jsonl" {
		return path
	}
	full := filepath.Join(filepath.Dir(path), "transcript_full.jsonl")
	if info, err := os.Stat(full); err == nil && !info.IsDir() {
		return full
	}
	return path
}

func antigravityVisibleBody(ev antigravityStep) (role, body string, ok bool) {
	switch {
	case ev.Type == "USER_INPUT" || ev.Source == "USER_EXPLICIT":
		role = "user"
	case ev.Type == "PLANNER_RESPONSE":
		role = "assistant"
	default:
		return "", "", false
	}
	body, ok = visibleTurn(role, ev.Content)
	return role, body, ok
}

func scanAntigravityTranscriptMeta(path string) (preview string, lastTS time.Time) {
	f, err := os.Open(path)
	if err != nil {
		return "", time.Time{}
	}
	defer f.Close()
	sc := jsonl.NewScanner(f, 4*1024*1024)
	for sc.Scan() {
		var ev antigravityStep
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		if ts := parseAntigravityTimestamp(ev.CreatedAt); ts.After(lastTS) {
			lastTS = ts
		}
		if preview == "" && (ev.Type == "USER_INPUT" || ev.Source == "USER_EXPLICIT") {
			preview = truncatedPreview(ev.Content)
		}
	}
	return preview, lastTS
}

func readAntigravityMessages(c Conversation, limit int) ([]Message, error) {
	path := antigravityJSONLPath(c)
	if path == "" {
		return nil, nil
	}
	path = preferFullTranscriptPath(path)
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	sc := jsonl.NewScanner(f, 8*1024*1024)
	var all []Message
	for sc.Scan() {
		var ev antigravityStep
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		role, body, ok := antigravityVisibleBody(ev)
		if !ok {
			continue
		}
		all = append(all, Message{
			Role:      role,
			Content:   body,
			Timestamp: parseAntigravityTimestamp(ev.CreatedAt),
		})
	}
	if len(all) > limit {
		all = all[len(all)-limit:]
	}
	return all, nil
}

func countAntigravityMessages(c Conversation) (int, error) {
	path := antigravityJSONLPath(c)
	if path == "" {
		return 0, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	sc := jsonl.NewScanner(f, 4*1024*1024)
	n := 0
	for sc.Scan() {
		var ev antigravityStep
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		if _, _, ok := antigravityVisibleBody(ev); ok {
			n++
		}
	}
	return n, nil
}
