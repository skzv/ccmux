package claudeusage

// The pre-cache scanner, kept verbatim (renamed) from before transcripts
// were cached, as the oracle the incremental parser is checked against:
// for any file contents and span, replaying the cached records must
// give exactly the events legacyScanFile produces from the bytes.

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/skzv/ccmux/internal/jsonl"
)

func legacyScanFile(path string, cutoff, now time.Time) (r scanResult) {
	defer func() { r.tally(r.events, time.Time{}) }()
	f, err := os.Open(path)
	if err != nil {
		return r
	}
	defer f.Close()
	// Subagent transcripts: the "user" turns there are the parent
	// agent's instructions and tool results, never the human.
	subagent := strings.Contains(filepath.ToSlash(path), "/subagents/")
	seenUsage := map[string]struct{}{}
	maxTS := now.Add(futureSkewTolerance)
	pendingPrompt := -1 // index in r.events of a prompt awaiting its response
	pendingCmd := false // a slash command awaiting a response
	legacyForEachLine(f, maxScanLineBytes, func(line []byte) {
		// Two cheap byte-level pre-filters: only json-decode lines that
		// might be assistant-usage or user-prompt records.
		hasUsage := maybeContains(line, []byte(`"usage":`))
		isUser := maybeContains(line, []byte(`"type":"user"`))
		if !hasUsage && !isUser {
			return
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
			return
		}
		ts, err := time.Parse(time.RFC3339, m.Timestamp)
		if err != nil || ts.Before(cutoff) || ts.After(maxTS) {
			return
		}

		if m.Message.Usage != nil {
			// The first response after a prompt is the API call it
			// triggered — sidechain calls belong to a subagent.
			if !m.IsSidechain {
				if pendingPrompt >= 0 {
					if ts.After(r.events[pendingPrompt].ts) {
						r.events[pendingPrompt].ts = ts
					}
					pendingPrompt = -1
				}
				if pendingCmd {
					r.events = append(r.events, usageEvent{ts: ts, prompt: true})
					pendingCmd = false
				}
			}
			// Assistant API response with usage — counted once per
			// (message.id, requestId) pair; see the function comment.
			if !legacyAlreadyCounted(seenUsage, m.Message.ID, m.RequestID) {
				t := Tokens{
					Input:         m.Message.Usage.Input,
					Output:        m.Message.Usage.Output,
					CacheCreation: m.Message.Usage.CacheCreation,
					CacheRead:     m.Message.Usage.CacheRead,
				}
				if cb := m.Message.Usage.CacheBreakout; cb != nil {
					t.CacheCreation1h = cb.OneHour
				}
				r.events = append(r.events, usageEvent{ts: ts, tokens: t, model: m.Message.Model})
			}
		}

		if m.Type != "user" || subagent || m.IsMeta || m.IsCompactSummary || m.IsSidechain {
			return
		}
		switch promptKindOf(m.Message.Content) {
		case humanPrompt:
			r.events = append(r.events, usageEvent{ts: ts, prompt: true})
			pendingPrompt, pendingCmd = len(r.events)-1, false
		case commandPrompt:
			pendingCmd = true
		case turnBoundary:
			pendingCmd = false
		}
	})
	return r
}
func legacyAlreadyCounted(seen map[string]struct{}, msgID, requestID string) bool {
	if msgID == "" || requestID == "" {
		return false
	}
	key := msgID + "\x00" + requestID
	if _, dup := seen[key]; dup {
		return true
	}
	seen[key] = struct{}{}
	return false
}

func legacyForEachLine(rd io.Reader, maxLen int, fn func(line []byte)) {
	sc := jsonl.NewScanner(rd, maxLen)
	for sc.Scan() {
		fn(sc.Bytes())
	}
}
