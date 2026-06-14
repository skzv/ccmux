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
	"bufio"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
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
type record struct {
	// Timestamp candidates. Different agents use different keys; we try
	// each. RFC3339 string forms are parsed; absent → the file mtime
	// gate (applied by the caller) is the only time filter.
	Timestamp string `json:"timestamp"`
	Time      string `json:"time"`
	CreatedAt string `json:"created_at"`

	// Role/type markers used to count user turns.
	Role string `json:"role"`
	Type string `json:"type"`

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
			// A permission error on one subdir shouldn't sink the walk.
			if os.IsPermission(err) {
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
	sc := bufio.NewScanner(f)
	// Transcript lines can be long (a full assistant message); give the
	// scanner room so it doesn't choke on them.
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var r record
		if err := json.Unmarshal(line, &r); err != nil {
			continue
		}
		// Per-message time gate when a timestamp is present; otherwise
		// the file-mtime pre-filter already admitted this file.
		if ts, ok := r.when(); ok && ts.Before(cutoff) {
			continue
		}
		if r.isUserTurn() {
			sum.Prompts++
		}
		in, out := r.tokens()
		sum.InputTokens += in
		sum.OutputTokens += out
	}
}

// when extracts a message timestamp from whichever key the agent used.
func (r record) when() (time.Time, bool) {
	for _, s := range []string{r.Timestamp, r.Time, r.CreatedAt} {
		if s == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t, true
		}
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// isUserTurn reports whether this record represents a user-initiated
// turn (the thing per-window quotas count). Matches the common
// role/type markers; tool-result follow-ups (role=tool / type=tool_*)
// are deliberately excluded.
func (r record) isUserTurn() bool {
	role := strings.ToLower(strings.TrimSpace(r.Role))
	typ := strings.ToLower(strings.TrimSpace(r.Type))
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
