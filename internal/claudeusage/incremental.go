package claudeusage

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/skzv/ccmux/internal/jsonl"
)

// cacheRetention bounds how far back the transcript cache keeps parsed
// records: 31 days, the largest window /v1/usage serves (cmd/ccmuxd's
// maxUsageWindow). A walk looking back further than this parses its
// files in full, uncached, as every walk did before the cache existed.
const cacheRetention = 31 * 24 * time.Hour

// transcripts caches each transcript's parsed records across walks, so
// the dashboard's 15-second refresh and /v1/usage read only what was
// appended since the previous walk. See jsonl.Cache for what it keeps
// and for how long.
var transcripts = jsonl.NewCache(cacheRetention, maxScanLineBytes, newFileRecords)

// record is one transcript line that can change the result of some
// walk: an assistant response carrying usage, a user record that is or
// cancels a prompt (see promptKindOf), or both. Lines that can't —
// other record types, malformed JSON, unparseable timestamps — are
// dropped when parsed, since scanFile ignores them in every window.
type record struct {
	ts     time.Time
	tokens Tokens
	model  string
	// key is the response's (message.id, requestId) dedupe key: an
	// index into its file's key table, or -1 when either id is missing
	// (never deduped). Only meaningful when usage is set.
	key       int32
	usage     bool       // carries a usage block
	sidechain bool       // isSidechain
	kind      promptKind // notPrompt unless an eligible user record
}

// parseLine decodes one transcript line into a record — the per-line
// half of scanFile, with everything that depends on the walk's window
// left to replay. key is the dedupe key ("" when either id is
// missing); ok is false when the line can't affect any walk.
func parseLine(line []byte, subagent bool) (rec record, key string, ok bool) {
	// Two cheap byte-level pre-filters: only json-decode lines that
	// might be assistant-usage or user-prompt records.
	hasUsage := maybeContains(line, []byte(`"usage":`))
	isUser := maybeContains(line, []byte(`"type":"user"`))
	if !hasUsage && !isUser {
		return rec, "", false
	}

	var m struct {
		Type             string `json:"type"`
		Timestamp        string `json:"timestamp"`
		RequestID        string `json:"requestId"`
		IsMeta           bool   `json:"isMeta"`
		IsCompactSummary bool   `json:"isCompactSummary"`
		IsSidechain      bool   `json:"isSidechain"`
		Message          struct {
			ID      string          `json:"id"`
			Role    string          `json:"role"`
			Model   string          `json:"model"`
			Content json.RawMessage `json:"content"`
			Usage   *struct {
				Input         int `json:"input_tokens"`
				Output        int `json:"output_tokens"`
				CacheCreation int `json:"cache_creation_input_tokens"`
				CacheRead     int `json:"cache_read_input_tokens"`
				CacheBreakout *struct {
					OneHour int `json:"ephemeral_1h_input_tokens"`
				} `json:"cache_creation"`
			} `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &m); err != nil {
		return rec, "", false
	}
	ts, err := time.Parse(time.RFC3339, m.Timestamp)
	if err != nil {
		return rec, "", false
	}
	rec = record{ts: ts, key: -1, sidechain: m.IsSidechain}
	if u := m.Message.Usage; u != nil {
		rec.usage = true
		rec.model = m.Message.Model
		rec.tokens = Tokens{
			Input:         u.Input,
			Output:        u.Output,
			CacheCreation: u.CacheCreation,
			CacheRead:     u.CacheRead,
		}
		if cb := u.CacheBreakout; cb != nil {
			rec.tokens.CacheCreation1h = cb.OneHour
		}
		if m.Message.ID != "" && m.RequestID != "" {
			key = m.Message.ID + "\x00" + m.RequestID
		}
	}
	if m.Type == "user" && !subagent && !m.IsMeta && !m.IsCompactSummary && !m.IsSidechain {
		rec.kind = promptKindOf(m.Message.Content)
	}
	return rec, key, rec.usage || rec.kind != notPrompt
}

// fileRecords is one transcript's cached parse: its records in file
// order, plus the dedupe key table they index. It implements
// jsonl.Parser.
type fileRecords struct {
	// subagent: a file under a subagents/ directory, whose "user"
	// turns are the parent agent's instructions and tool results,
	// never the human.
	subagent bool
	recs     []record
	// tail holds the record parsed from an unterminated last line — a
	// write in progress — which is re-read on every change until its
	// newline lands. Its key is -1 when not already in the table: as
	// the file's very last record, it can neither be a duplicate of an
	// earlier response nor have a later one dedupe against it.
	tail []record
	keys map[string]int32
	strs []string // keys by id
}

func newFileRecords(path string) *fileRecords {
	return &fileRecords{
		subagent: strings.Contains(filepath.ToSlash(path), "/subagents/"),
		keys:     map[string]int32{},
	}
}

// Line implements jsonl.Parser. A record before from is dropped whole,
// dedupe key included: scanFile skips out-of-window lines entirely.
func (f *fileRecords) Line(line []byte, from time.Time) {
	rec, key, ok := parseLine(line, f.subagent)
	if !ok || rec.ts.Before(from) {
		return
	}
	if key != "" {
		rec.key = f.keyID(key)
	}
	f.recs = append(f.recs, rec)
}

// Partial implements jsonl.Parser.
func (f *fileRecords) Partial(line []byte) {
	f.tail = f.tail[:0]
	if line == nil {
		return
	}
	rec, key, ok := parseLine(line, f.subagent)
	if !ok {
		return
	}
	if id, known := f.keys[key]; known && key != "" {
		rec.key = id
	}
	f.tail = append(f.tail, rec)
}

// Restart implements jsonl.Parser.
func (f *fileRecords) Restart() {
	f.recs, f.tail, f.strs = nil, nil, nil
	f.keys = map[string]int32{}
}

// Prune implements jsonl.Parser: it drops records before from and
// rebuilds the key table from the ones left.
func (f *fileRecords) Prune(from time.Time) {
	old := f.strs
	f.keys, f.strs = map[string]int32{}, nil
	kept := f.recs[:0]
	for _, r := range f.recs {
		if r.ts.Before(from) {
			continue
		}
		if r.key >= 0 {
			r.key = f.keyID(old[r.key])
		}
		kept = append(kept, r)
	}
	clear(f.recs[len(kept):])
	f.recs = kept
	for i := range f.tail {
		if k := f.tail[i].key; k >= 0 {
			f.tail[i].key = -1 // a key only pruned records had: now new
			if id, ok := f.keys[old[k]]; ok {
				f.tail[i].key = id
			}
		}
	}
}

// keyID returns key's id in the table, adding it if new.
func (f *fileRecords) keyID(key string) int32 {
	if id, ok := f.keys[key]; ok {
		return id
	}
	id := int32(len(f.strs))
	f.keys[key] = id
	f.strs = append(f.strs, key)
	return id
}

// events replays the file's records for one walk over [cutoff, now]:
// the events scanFile would produce reading the same bytes.
func (f *fileRecords) events(cutoff, now time.Time) []usageEvent {
	return replay(cutoff, now.Add(futureSkewTolerance), len(f.strs), f.recs, f.tail)
}

// replay runs scanFile's per-line state machine over records (in file
// order, across the given slices) for the span [cutoff, maxTS] and
// returns the resulting events. Out-of-span records are skipped whole —
// they neither pair with a prompt nor claim a dedupe key — exactly as
// scanFile skips out-of-window lines. nkeys bounds the records' keys.
func replay(cutoff, maxTS time.Time, nkeys int, parts ...[]record) []usageEvent {
	var events []usageEvent
	seen := make([]bool, nkeys)
	pendingPrompt := -1 // index in events of a prompt awaiting its response
	pendingCmd := false // a slash command awaiting a response
	for _, recs := range parts {
		for i := range recs {
			r := &recs[i]
			if r.ts.Before(cutoff) || r.ts.After(maxTS) {
				continue
			}
			if r.usage {
				// The first response after a prompt is the API call it
				// triggered — sidechain calls belong to a subagent.
				if !r.sidechain {
					if pendingPrompt >= 0 {
						if r.ts.After(events[pendingPrompt].ts) {
							events[pendingPrompt].ts = r.ts
						}
						pendingPrompt = -1
					}
					if pendingCmd {
						events = append(events, usageEvent{ts: r.ts, prompt: true})
						pendingCmd = false
					}
				}
				// Counted once per (message.id, requestId) pair.
				if r.key < 0 || !seen[r.key] {
					if r.key >= 0 {
						seen[r.key] = true
					}
					events = append(events, usageEvent{ts: r.ts, tokens: r.tokens, model: r.model})
				}
			}
			switch r.kind {
			case humanPrompt:
				events = append(events, usageEvent{ts: r.ts, prompt: true})
				pendingPrompt, pendingCmd = len(events)-1, false
			case commandPrompt:
				pendingCmd = true
			case turnBoundary:
				pendingCmd = false
			}
		}
	}
	return events
}
