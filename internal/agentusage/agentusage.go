// Package agentusage is a best-effort, format-agnostic token-usage
// walker for agents whose sessions are stored as JSONL with a
// recognizable per-message usage block. It exists so the long tail of
// terminal coding agents (OpenCode, Kimi, Droid, …) can show real
// "tokens used in the window" numbers without a bespoke parser each —
// the rich, agent-specific walkers (internal/claudeusage,
// internal/codexusage) stay for the agents whose formats we've pinned.
//
// What it recognizes: any JSON object anywhere in a *.jsonl file that
// carries a `usage` object (or top-level token fields) in either of the
// two dominant shapes:
//
//   - OpenAI style:    {"usage":{"prompt_tokens":N,"completion_tokens":M}}
//   - Anthropic style: {"usage":{"input_tokens":N,"output_tokens":M}}
//
// User turns are counted from objects whose role/type marks them as a
// user message. Everything is best-effort: an agent whose transcripts
// don't match either shape simply yields HasData=false, which the
// dashboard renders as the install-hint placeholder — the same graceful
// "we can't see inside yet" state as an agent with no walker at all. No
// cost is computed (the model/pricing varies per agent); the caller can
// layer OpenRouter pricing on top when the agent is routed there.
package agentusage

import (
	"encoding/json"
	"errors"
	"github.com/skzv/ccmux/internal/jsonl"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Summary is the result of one Walk: token totals + user-turn count for
// the window. HasData is false when no recognizable usage was found.
type Summary struct {
	HasData      bool
	Window       time.Duration
	Prompts      int
	InputTokens  int
	OutputTokens int
}

// record is the union of the fields we look for across both usage
// shapes plus the turn-role markers. All optional — a line that has
// none contributes nothing.
//
// The timestamp and marker fields are raw JSON because agents disagree
// on their types: a timestamp can be an RFC3339 string or epoch
// seconds/milliseconds, and some agents nest objects under `time` or
// `type`. Typing them as strings made json.Unmarshal fail on those
// lines, which dropped their tokens.
type record struct {
	// Timestamp candidates. Different agents use different keys; we try
	// each (see parseWhen). Absent or unrecognized → the record is
	// undated, and the file mtime gate (applied by the caller) is the
	// only time filter.
	Timestamp json.RawMessage `json:"timestamp"`
	Time      json.RawMessage `json:"time"`
	CreatedAt json.RawMessage `json:"created_at"`

	// Role/type markers used to count user turns. Only string values
	// mean anything.
	Role json.RawMessage `json:"role"`
	Type json.RawMessage `json:"type"`

	Usage *usageBlock `json:"usage"`

	// Some agents put token fields at the top level rather than under
	// `usage`. Captured here as a fallback.
	PromptTokens     *int `json:"prompt_tokens"`
	CompletionTokens *int `json:"completion_tokens"`
	InputTokens      *int `json:"input_tokens"`
	OutputTokens     *int `json:"output_tokens"`
}

type usageBlock struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	InputTokens      int `json:"input_tokens"`
	OutputTokens     int `json:"output_tokens"`
}

// input returns the input-token count from whichever shape is present
// (Anthropic input_tokens or OpenAI prompt_tokens).
func (u usageBlock) input() int {
	if u.InputTokens > 0 {
		return u.InputTokens
	}
	return u.PromptTokens
}

func (u usageBlock) output() int {
	if u.OutputTokens > 0 {
		return u.OutputTokens
	}
	return u.CompletionTokens
}

// Walk scans every *.jsonl under root (recursively) and aggregates token
// usage for messages within the window. Files whose mtime is older than
// the window are skipped wholesale (cheap pre-filter); within a file,
// per-message timestamps refine the window when present. Returns
// HasData=false (not an error) when root is missing or nothing matched —
// callers render that as the placeholder row.
func Walk(root string, window time.Duration) (Summary, error) {
	cutoff := time.Now().Add(-window)
	sum := Summary{Window: window}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// A permission error on one subdir, or a file/dir removed
			// while we walk (agents rotate transcripts), shouldn't sink
			// the whole walk.
			if os.IsPermission(err) || os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		// Pre-filter by file mtime: a file last written before the
		// window can't contain in-window messages.
		if info, ierr := d.Info(); ierr == nil && info.ModTime().Before(cutoff) {
			return nil
		}
		scanFile(path, cutoff, &sum)
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return Summary{Window: window}, nil
		}
		return Summary{Window: window}, err
	}
	sum.HasData = sum.Prompts > 0 || sum.InputTokens > 0 || sum.OutputTokens > 0
	return sum, nil
}

// scanFile reads one JSONL file line by line, accumulating into sum.
// Unparseable lines are skipped silently — transcripts often interleave
// non-JSON or partial lines.
func scanFile(path string, cutoff time.Time, sum *Summary) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	// Transcript lines can be long (a full assistant message); allow up
	// to 8 MiB, and skip — rather than stop at — anything longer.
	sc := jsonl.NewScanner(f, 8*1024*1024)
	// An undated record is placed at the time of the dated record before
	// it (transcripts are append-only, so that's when it was written).
	// Undated records before the first dated one are held until we see
	// that one; a file with no timestamps at all is counted whole, since
	// the mtime pre-filter already put it inside the window.
	var lastTS time.Time
	var pending Summary
	add := func(dst *Summary, r record) {
		if r.isUserTurn() {
			dst.Prompts++
		}
		in, out := r.tokens()
		dst.InputTokens += in
		dst.OutputTokens += out
	}
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var r record
		if err := json.Unmarshal(line, &r); err != nil {
			// A token field of an unexpected type is left zero and the
			// rest of the record still counts; only malformed JSON is
			// skipped.
			var typeErr *json.UnmarshalTypeError
			if !errors.As(err, &typeErr) {
				continue
			}
		}
		ts, dated := r.when()
		if !dated {
			if lastTS.IsZero() {
				add(&pending, r)
				continue
			}
			ts = lastTS
		} else if lastTS.IsZero() && !ts.Before(cutoff) {
			// First dated record is in the window, so the undated ones
			// written just before it are too.
			sum.Prompts += pending.Prompts
			sum.InputTokens += pending.InputTokens
			sum.OutputTokens += pending.OutputTokens
		}
		if dated {
			lastTS = ts
		}
		if ts.Before(cutoff) {
			continue
		}
		add(sum, r)
	}
	if lastTS.IsZero() {
		sum.Prompts += pending.Prompts
		sum.InputTokens += pending.InputTokens
		sum.OutputTokens += pending.OutputTokens
	}
}

// when extracts a message timestamp from whichever key the agent used.
func (r record) when() (time.Time, bool) {
	for _, raw := range []json.RawMessage{r.Timestamp, r.Time, r.CreatedAt} {
		if t, ok := parseWhen(raw); ok {
			return t, true
		}
	}
	return time.Time{}, false
}

// parseWhen decodes one timestamp field: an RFC3339 string, or a Unix
// epoch in seconds or milliseconds, as a JSON number or a numeric
// string. Anything else (null, an object) is not a timestamp.
func parseWhen(raw json.RawMessage) (time.Time, bool) {
	if len(raw) == 0 {
		return time.Time{}, false
	}
	var n float64
	var s string
	switch {
	case json.Unmarshal(raw, &n) == nil:
	case json.Unmarshal(raw, &s) == nil:
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t, true
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return time.Time{}, false
		}
		n = f
	default:
		return time.Time{}, false
	}
	if n <= 0 || math.IsInf(n, 0) || math.IsNaN(n) {
		return time.Time{}, false
	}
	// Epoch seconds stay below 1e11 until the year 5138; epoch
	// milliseconds passed 1e11 in 1973.
	if n >= 1e11 {
		return time.UnixMilli(int64(n)), true
	}
	sec, frac := math.Modf(n)
	return time.Unix(int64(sec), int64(frac*1e9)), true
}

// jsonString returns raw's value when it is a JSON string, else "".
func jsonString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

// isUserTurn reports whether this record represents a user-initiated
// turn (the thing per-window quotas count). Matches the common
// role/type markers; tool-result follow-ups (role=tool / type=tool_*)
// are deliberately excluded.
func (r record) isUserTurn() bool {
	role := strings.ToLower(strings.TrimSpace(jsonString(r.Role)))
	typ := strings.ToLower(strings.TrimSpace(jsonString(r.Type)))
	if role == "user" {
		return true
	}
	if typ == "user" || typ == "user_message" || typ == "message.user" {
		return true
	}
	return false
}

// tokens returns the input/output token counts from whichever shape is
// present: the nested usage block first, then top-level fields.
func (r record) tokens() (in, out int) {
	if r.Usage != nil {
		return r.Usage.input(), r.Usage.output()
	}
	if r.InputTokens != nil {
		in = *r.InputTokens
	} else if r.PromptTokens != nil {
		in = *r.PromptTokens
	}
	if r.OutputTokens != nil {
		out = *r.OutputTokens
	} else if r.CompletionTokens != nil {
		out = *r.CompletionTokens
	}
	return in, out
}
