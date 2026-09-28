package codexusage

// The pre-cache scanner, kept verbatim (renamed) from before rollouts
// were cached, as the oracle the incremental parser is checked against.
// The one change: it opens the file through fsys (nil: the OS), so the
// fuzz target can keep its rollouts in memory.

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"time"

	"github.com/skzv/ccmux/internal/jsonl"
)

func legacyOpen(fsys fs.FS, path string) (io.ReadCloser, error) {
	if fsys == nil {
		return os.Open(path)
	}
	return fsys.Open(path)
}

func legacyScanFile(fsys fs.FS, path string, cutoff, now time.Time) scanResult {
	r := scanResult{byModel: map[string]*Tokens{}}
	f, err := legacyOpen(fsys, path)
	if err != nil {
		return r
	}
	defer f.Close()
	sc := jsonl.NewScanner(f, 1<<25)
	currentModel := "" // last model seen from a turn_context record
	subagent := false
	var prevTotal *tokenUsage // total_token_usage on the previous token_count event
	for sc.Scan() {
		line := sc.Bytes()
		// Cheap byte-level prefilter — only json-decode lines that
		// could possibly carry usage, a turn_context model, a user
		// response_item, or the session_meta source.
		isTokenCount := bytes.Contains(line, []byte(`"token_count"`))
		isTurnContext := bytes.Contains(line, []byte(`"turn_context"`))
		isResponseItem := bytes.Contains(line, []byte(`"response_item"`))
		isSessionMeta := bytes.Contains(line, []byte(`"session_meta"`))
		if !isTokenCount && !isTurnContext && !isResponseItem && !isSessionMeta {
			continue
		}

		var env struct {
			Timestamp string          `json:"timestamp"`
			Type      string          `json:"type"`
			Payload   json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(line, &env); err != nil {
			continue
		}
		ts := parseTimestamp(env.Timestamp)
		inWindow := !ts.IsZero() && !ts.Before(cutoff) && !ts.After(now)

		switch env.Type {
		case "session_meta":
			var p struct {
				Source json.RawMessage `json:"source"`
			}
			if err := json.Unmarshal(env.Payload, &p); err == nil && IsSubagentSource(p.Source) {
				subagent = true
			}
		case "turn_context":
			var p struct {
				Model string `json:"model"`
			}
			if err := json.Unmarshal(env.Payload, &p); err == nil && p.Model != "" {
				currentModel = p.Model
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
				continue
			}
			if p.Type != "token_count" || p.Info.Last == nil {
				continue
			}
			// Tracked across the whole file, not just the window, so a
			// repeat of the last event before the cutoff is caught too.
			repeat := p.Info.Total != nil && prevTotal != nil && *p.Info.Total == *prevTotal
			if p.Info.Total != nil {
				prevTotal = p.Info.Total
			}
			if repeat || !inWindow {
				continue
			}
			tok := Tokens{Input: p.Info.Last.Input, Output: p.Info.Last.Output, Cached: p.Info.Last.Cached}
			r.total.Add(tok)
			r.events++
			model := currentModel
			if model == "" {
				model = "unknown"
			}
			mt := r.byModel[model]
			if mt == nil {
				mt = &Tokens{}
				r.byModel[model] = mt
			}
			mt.Add(tok)
		case "response_item":
			if !inWindow {
				continue
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
				continue
			}
			if p.Type != "message" || p.Role != "user" {
				continue
			}
			if isSyntheticUserContent(p.Content) {
				continue
			}
			r.userPrompts++
		}
	}
	if subagent {
		r.userPrompts = 0
	}
	return r
}
