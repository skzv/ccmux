// Package codexusage aggregates token usage and prompt counts from the
// Codex CLI's rollout JSONL files under ~/.codex/sessions/. Local-only
// parse — no API calls. Powers the dashboard's per-agent Codex row so
// users see "tokens used in the last 5h" the same way the Claude panel
// does, regardless of which agent the user reaches for.
//
// File shape: one rollout-*.jsonl per session, with `event_msg`
// records of payload type `token_count` carrying both cumulative
// (`total_token_usage`) and per-event (`last_token_usage`) breakdowns,
// plus `response_item` records for user / assistant turns and
// `turn_context` records carrying the active model. We sum
// `last_token_usage` for events inside the window and count
// `response_item` user turns excluding the synthetic
// `<environment_context>` injection Codex prepends on every session,
// and excluding every turn of a subagent rollout (guardian reviews,
// spawned threads) — see IsSubagentSource.
package codexusage

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Tokens is the breakdown each token_count event carries. Cached is
// the subset of Input that came from prompt-cache hits and bills at a
// lower rate; treating it as a separate bucket gives the cost
// calculator the right per-1M rate to apply.
type Tokens struct {
	Input  int
	Output int
	// Cached counts cached_input_tokens — billed at a lower rate by
	// OpenAI but already included in Input. The cost calculation
	// subtracts Cached from Input before applying the un-cached rate.
	Cached int
}

func (t *Tokens) Add(o Tokens) {
	t.Input += o.Input
	t.Output += o.Output
	t.Cached += o.Cached
}

// Total returns Input + Output. Cached is intentionally excluded —
// it's already a subset of Input.
func (t Tokens) Total() int { return t.Input + t.Output }

// Aggregate is one rolled-up result from Walk().
type Aggregate struct {
	Window      time.Duration
	WindowStart time.Time
	WindowEnd   time.Time
	Messages    int // count of token_count events with non-nil last_token_usage
	UserPrompts int // real user turns (excluding env-context injections)
	Total       Tokens
	ByModel     map[string]*Tokens
}

// EstimatedCost returns a best-effort USD figure at OpenAI's published
// rates. Approximate — primarily useful as a relative signal across
// days, not as a billing source of truth.
func (a *Aggregate) EstimatedCost() float64 {
	var cost float64
	for model, t := range a.ByModel {
		p := priceFor(model)
		// Cached is already inside Input; subtract it so we don't
		// double-count, then bill it at the cached rate.
		uncached := t.Input - t.Cached
		if uncached < 0 {
			uncached = 0
		}
		cost += float64(uncached)/1e6*p.Input +
			float64(t.Cached)/1e6*p.Cached +
			float64(t.Output)/1e6*p.Output
	}
	return cost
}

// price is per-million-token cost in USD.
type price struct {
	Input, Cached, Output float64
}

// priceFor returns USD-per-million-token rates for the listed OpenAI
// models. Unknown models fall back to gpt-5 rates — preferred over
// $0 because users would otherwise see a free-looking row for any
// model we haven't catalogued yet, and a wrong-by-2x cost is more
// honest than a missing one.
func priceFor(model string) price {
	m := strings.ToLower(model)
	// Order matters: the o-series and 4o minis must match before the
	// generic "mini" (gpt-5-mini) case, and "o3" before "o1".
	switch {
	case strings.Contains(m, "nano"):
		return price{Input: 0.05, Cached: 0.005, Output: 0.40}
	case strings.Contains(m, "o3-mini"), strings.Contains(m, "o4-mini"):
		return price{Input: 1.10, Cached: 0.275, Output: 4.40}
	case strings.Contains(m, "4o-mini"):
		return price{Input: 0.15, Cached: 0.075, Output: 0.60}
	case strings.Contains(m, "mini"):
		return price{Input: 0.25, Cached: 0.025, Output: 2.00}
	case strings.Contains(m, "o3"):
		return price{Input: 2.00, Cached: 0.50, Output: 8.00}
	case strings.Contains(m, "o1"):
		return price{Input: 15.0, Cached: 7.50, Output: 60.0}
	case strings.Contains(m, "4o"):
		return price{Input: 2.50, Cached: 1.25, Output: 10.0}
	default: // gpt-5 family / unknown
		return price{Input: 1.25, Cached: 0.125, Output: 10.0}
	}
}

// Walk scans every rollout-*.jsonl under ~/.codex/sessions/ and
// returns one aggregate of events whose timestamp falls inside the
// requested window. Safe to call concurrently with itself; built for
// dashboard polling every 5-10s, not per-keystroke. Rollouts parsed by
// an earlier walk are cached in memory (see rollouts), so a repeat walk
// reads only the bytes appended since.
func Walk(window time.Duration) (*Aggregate, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	root := filepath.Join(home, ".codex", "sessions")
	return walkRoot(root, window, time.Now())
}

// walkRoot is the testable core of Walk — same logic, but the
// transcript root and "now" are injected so unit tests can fixture
// files under a t.TempDir() without colliding with the developer's
// actual ~/.codex.
func walkRoot(root string, window time.Duration, now time.Time) (*Aggregate, error) {
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			rollouts.Sweep(root, nil, now)
			return emptyAggregate(window, now), nil
		}
		return nil, err
	}
	cutoff := now.Add(-window)
	agg := &Aggregate{
		Window:      window,
		WindowStart: cutoff,
		WindowEnd:   now,
		ByModel:     map[string]*Tokens{},
	}

	span := rollouts.Span(now, window)
	type fileTask struct {
		path string
		info fs.FileInfo
	}
	present := map[string]bool{} // every rollout listed, for the cache sweep
	var files []fileTask
	if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable
		}
		if d.IsDir() || !strings.HasPrefix(d.Name(), "rollout-") || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		present[path] = true
		// Skip files whose mtime is older than the window. Codex
		// session files are append-only until the session closes, so
		// mtime is a sound cutoff signal.
		info, _ := d.Info()
		if info != nil && info.ModTime().Before(cutoff) {
			return nil
		}
		files = append(files, fileTask{path, info})
		return nil
	}); err != nil {
		return nil, err
	}

	// Bring each file's cached records up to date — only bytes appended
	// since the last walk are read — and tally them over the window.
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, t := range files {
		wg.Add(1)
		sem <- struct{}{}
		go func(task fileTask) {
			defer wg.Done()
			defer func() { <-sem }()
			var r scanResult
			rollouts.Parse(task.path, task.info, span, func(f *fileRecords) { r = f.result(cutoff, now) })
			if r.events == 0 && r.userPrompts == 0 {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			agg.Total.Add(r.total)
			agg.Messages += r.events
			agg.UserPrompts += r.userPrompts
			for model, t := range r.byModel {
				mt := agg.ByModel[model]
				if mt == nil {
					mt = &Tokens{}
					agg.ByModel[model] = mt
				}
				mt.Add(*t)
			}
		}(t)
	}
	wg.Wait()
	rollouts.Sweep(root, present, now)
	return agg, nil
}

func emptyAggregate(window time.Duration, now time.Time) *Aggregate {
	return &Aggregate{
		Window:      window,
		WindowStart: now.Add(-window),
		WindowEnd:   now,
		ByModel:     map[string]*Tokens{},
	}
}

// tokenUsage is one usage breakdown on a token_count event: the
// per-call last_token_usage or the cumulative total_token_usage.
type tokenUsage struct {
	Input     int `json:"input_tokens"`
	Cached    int `json:"cached_input_tokens"`
	Output    int `json:"output_tokens"`
	Reasoning int `json:"reasoning_output_tokens"`
	Total     int `json:"total_tokens"`
}

// scanResult bundles everything one transcript scan produces.
type scanResult struct {
	total       Tokens
	byModel     map[string]*Tokens
	events      int // token_count events with non-nil last_token_usage
	userPrompts int // real user turns inside the window
}

// IsSubagentSource reports whether a rollout's session_meta
// payload.source marks a run Codex spawned itself: the object
// {"subagent": {"other": "guardian"} | {"thread_spawn": …}} rather than
// the plain string ("cli", "vscode", "exec") of a session a user
// started. Shared with internal/conversations, which hides these runs
// from the conversation list.
func IsSubagentSource(source json.RawMessage) bool {
	source = bytes.TrimSpace(source)
	if len(source) == 0 || source[0] != '{' {
		return false
	}
	var src struct {
		Subagent json.RawMessage `json:"subagent"`
	}
	if json.Unmarshal(source, &src) != nil {
		return false
	}
	return len(src.Subagent) > 0 && string(src.Subagent) != "null"
}

// isSyntheticUserContent returns true if the first text block looks
// like a Codex-injected system tag rather than a real user message.
// Codex prepends `<environment_context>...` to every session and
// occasionally emits `<user_instructions>` style blocks for resumes;
// neither is a turn the user actually typed.
func isSyntheticUserContent(content []struct {
	Type string `json:"type"`
	Text string `json:"text"`
}) bool {
	for _, c := range content {
		if c.Type != "input_text" && c.Type != "text" {
			continue
		}
		t := strings.TrimSpace(c.Text)
		if strings.HasPrefix(t, "<environment_context>") ||
			strings.HasPrefix(t, "<user_instructions>") ||
			strings.HasPrefix(t, "# AGENTS.md instructions") {
			return true
		}
		// First real text block decides — bail after seeing one.
		return false
	}
	return false
}

// parseTimestamp accepts the two timestamp shapes Codex emits.
// Modern rollouts use RFC3339 with millisecond precision and a `Z`
// suffix ("2026-05-12T23:17:43.064Z"); older imports sometimes use
// the same shape without the millis. time.RFC3339Nano handles both.
func parseTimestamp(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}
