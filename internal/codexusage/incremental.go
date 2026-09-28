package codexusage

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/skzv/ccmux/internal/jsonl"
)

// cacheRetention bounds how far back the rollout cache keeps parsed
// records: 31 days, the largest window /v1/usage serves (cmd/ccmuxd's
// maxUsageWindow). A longer window parses its files in full, uncached.
const cacheRetention = 31 * 24 * time.Hour

// maxLineBytes caps one rollout line; longer lines are skipped.
const maxLineBytes = 1 << 25

// rollouts caches each rollout's parsed records across walks, so a
// refresh reads only what was appended since the previous one. See
// jsonl.Cache for what it keeps and for how long.
var rollouts = jsonl.NewCache(cacheRetention, maxLineBytes, newFileRecords)

// record is one countable rollout event: a token_count carrying new
// usage (tokens, model), or a user prompt (prompt set). Everything
// scanFile works out from earlier lines — the model in effect, whether
// the event repeats the previous one — is resolved when the line is
// parsed, since it doesn't depend on the window; only the timestamp
// test is left for each walk.
type record struct {
	ts     time.Time
	prompt bool
	tokens Tokens
	model  string
}

// lineState is what reading a rollout carries from line to line,
// independent of any window.
type lineState struct {
	model    string     // from the last turn_context record
	prev     tokenUsage // total_token_usage on the previous token_count event
	hasPrev  bool
	subagent bool // session_meta marked this a subagent rollout
}

// parseLine applies one rollout line to st and returns the record it
// yields, if any. Two distinct things get counted:
//
//   - token_count events with last_token_usage → token totals,
//     attributed to the most recent model seen on a turn_context
//     record (Codex emits one per turn, before its associated
//     token_count event). Codex sometimes writes the same event twice
//     in a row; an event whose cumulative total_token_usage hasn't
//     moved since the previous one in the file reports no new API
//     call and is skipped.
//   - response_item records with role=user → user prompts, excluding
//     synthetic injections whose first text block starts with
//     "<environment_context>" (Codex prepends one per session) or
//     other angle-bracketed system tags. This matches what a human
//     would count as "I sent a message". A subagent rollout (its
//     session_meta source is {"subagent": …}) has no human prompts at
//     all: its "user" turns are what the parent agent sent. Its tokens
//     are still real API usage and still count (see result).
//
// Built to tolerate large lines: rollout entries can include the
// full system-instructions blob, easily 100KB+ (see maxLineBytes).
func (st *lineState) parseLine(line []byte) (rec record, ok bool) {
	// Cheap byte-level prefilter — only json-decode lines that could
	// possibly carry usage, a turn_context model, a user response_item,
	// or the session_meta source.
	isTokenCount := bytes.Contains(line, []byte(`"token_count"`))
	isTurnContext := bytes.Contains(line, []byte(`"turn_context"`))
	isResponseItem := bytes.Contains(line, []byte(`"response_item"`))
	isSessionMeta := bytes.Contains(line, []byte(`"session_meta"`))
	if !isTokenCount && !isTurnContext && !isResponseItem && !isSessionMeta {
		return rec, false
	}

	var env struct {
		Timestamp string          `json:"timestamp"`
		Type      string          `json:"type"`
		Payload   json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(line, &env); err != nil {
		return rec, false
	}
	// A zero timestamp is in no window, so it yields no record — but
	// the line still updates the state.
	ts := parseTimestamp(env.Timestamp)

	switch env.Type {
	case "session_meta":
		var p struct {
			Source json.RawMessage `json:"source"`
		}
		if err := json.Unmarshal(env.Payload, &p); err == nil && IsSubagentSource(p.Source) {
			st.subagent = true
		}
	case "turn_context":
		var p struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(env.Payload, &p); err == nil && p.Model != "" {
			st.model = p.Model
		}
	case "event_msg":
		var p struct {
			Type string `json:"type"`
			Info struct {
				Total *tokenUsage `json:"total_token_usage"`
				Last  *tokenUsage `json:"last_token_usage"`
			} `json:"info"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return rec, false
		}
		if p.Type != "token_count" || p.Info.Last == nil {
			return rec, false
		}
		// Tracked across the whole file, not just the window, so a
		// repeat of the last event before the cutoff is caught too.
		repeat := p.Info.Total != nil && st.hasPrev && *p.Info.Total == st.prev
		if p.Info.Total != nil {
			st.prev, st.hasPrev = *p.Info.Total, true
		}
		if repeat || ts.IsZero() {
			return rec, false
		}
		model := st.model
		if model == "" {
			model = "unknown"
		}
		tok := Tokens{Input: p.Info.Last.Input, Output: p.Info.Last.Output, Cached: p.Info.Last.Cached}
		return record{ts: ts, tokens: tok, model: model}, true
	case "response_item":
		if ts.IsZero() {
			return rec, false
		}
		var p struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return rec, false
		}
		if p.Type != "message" || p.Role != "user" || isSyntheticUserContent(p.Content) {
			return rec, false
		}
		return record{ts: ts, prompt: true}, true
	}
	return rec, false
}

// fileRecords is one rollout's cached parse. It implements
// jsonl.Parser.
type fileRecords struct {
	st   lineState
	recs []record
	// tail is what an unterminated last line (a write in progress)
	// yields, parsed from a copy of st: it counts for walks until the
	// line is re-read, but mustn't advance the committed state.
	tail         []record
	tailSubagent bool
}

func newFileRecords(string) *fileRecords { return &fileRecords{} }

// Line implements jsonl.Parser.
func (f *fileRecords) Line(line []byte, from time.Time) {
	if rec, ok := f.st.parseLine(line); ok && !rec.ts.Before(from) {
		f.recs = append(f.recs, rec)
	}
}

// Partial implements jsonl.Parser.
func (f *fileRecords) Partial(line []byte) {
	f.tail, f.tailSubagent = f.tail[:0], false
	if line == nil {
		return
	}
	st := f.st
	if rec, ok := st.parseLine(line); ok {
		f.tail = append(f.tail, rec)
	}
	f.tailSubagent = st.subagent
}

// Restart implements jsonl.Parser.
func (f *fileRecords) Restart() { *f = fileRecords{} }

// Prune implements jsonl.Parser.
func (f *fileRecords) Prune(from time.Time) {
	kept := f.recs[:0]
	for _, r := range f.recs {
		if !r.ts.Before(from) {
			kept = append(kept, r)
		}
	}
	clear(f.recs[len(kept):])
	f.recs = kept
}

// result tallies the file's records inside [cutoff, now]: what
// scanFile returns for the same bytes.
func (f *fileRecords) result(cutoff, now time.Time) scanResult {
	r := scanResult{byModel: map[string]*Tokens{}}
	for _, recs := range [][]record{f.recs, f.tail} {
		for _, rec := range recs {
			if rec.ts.Before(cutoff) || rec.ts.After(now) {
				continue
			}
			if rec.prompt {
				r.userPrompts++
				continue
			}
			r.total.Add(rec.tokens)
			r.events++
			mt := r.byModel[rec.model]
			if mt == nil {
				mt = &Tokens{}
				r.byModel[rec.model] = mt
			}
			mt.Add(rec.tokens)
		}
	}
	// A subagent rollout's "user" turns are what the parent agent sent.
	if f.st.subagent || f.tailSubagent {
		r.userPrompts = 0
	}
	return r
}
