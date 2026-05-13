// Package claudeusage aggregates token usage and message counts from
// Claude Code's transcript JSONL files under ~/.claude/projects/. No API
// calls, no auth — pure local parse. We use this to power the dashboard
// usage panel, per-project breakdowns, and the subscription "5-hour
// rolling window" indicator (Pro / Max plan reset model).
package claudeusage

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Tokens is the four-way breakdown each assistant message carries in its
// `usage` block.
type Tokens struct {
	Input         int `json:"input"`
	Output        int `json:"output"`
	CacheCreation int `json:"cache_creation"`
	CacheRead     int `json:"cache_read"`
}

// Add accumulates another Tokens into this one.
func (t *Tokens) Add(o Tokens) {
	t.Input += o.Input
	t.Output += o.Output
	t.CacheCreation += o.CacheCreation
	t.CacheRead += o.CacheRead
}

// Total returns the sum of all four token categories. Useful for the
// dashboard headline number.
func (t Tokens) Total() int {
	return t.Input + t.Output + t.CacheCreation + t.CacheRead
}

// Aggregate is one rolled-up result from Walk().
type Aggregate struct {
	Window      time.Duration // size of the window this aggregate covers
	WindowStart time.Time     // earliest message timestamp considered
	WindowEnd   time.Time     // latest message timestamp (or "now")
	Messages    int           // total assistant API responses with usage data
	UserPrompts int           // distinct user-initiated turns (filters out
	// tool-result follow-ups, which JSONL also
	// records with type="user"). This is what
	// Anthropic's per-window quota counts toward.
	Total     Tokens
	ByModel   map[string]*Tokens
	ByProject map[string]*Tokens
	// FirstMessageInWindow is the timestamp of the earliest assistant
	// message that still falls inside the window — used to compute the
	// "next reset at" time for Pro/Max subscription windows.
	FirstMessageInWindow time.Time
}

// ProjectTotal is one row in the per-project breakdown returned by
// AggregateReport.TopProjects.
type ProjectTotal struct {
	Project  string
	Tokens   Tokens
	Messages int
}

// TopProjects returns up to `n` projects sorted by total token usage
// descending.
func (a *Aggregate) TopProjects(n int) []ProjectTotal {
	out := make([]ProjectTotal, 0, len(a.ByProject))
	for proj, t := range a.ByProject {
		out = append(out, ProjectTotal{Project: proj, Tokens: *t})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Tokens.Total() > out[j].Tokens.Total()
	})
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// ResetAt estimates when the next 5-hour subscription window opens. For
// Pro/Max plans the window starts at the first message after the
// previous window ended; we approximate by taking the oldest message
// still in the window + the window duration.
//
// Returns the zero time if no messages fall in the window.
func (a *Aggregate) ResetAt(window time.Duration) time.Time {
	if a.FirstMessageInWindow.IsZero() {
		return time.Time{}
	}
	return a.FirstMessageInWindow.Add(window)
}

// EstimatedCost returns a rough USD figure using current published
// Anthropic API pricing. Approximate — primarily useful as a relative
// signal across days, not as a billing source of truth.
func (a *Aggregate) EstimatedCost() float64 {
	var cost float64
	for model, t := range a.ByModel {
		p := priceFor(model)
		cost += float64(t.Input)/1e6*p.Input +
			float64(t.Output)/1e6*p.Output +
			float64(t.CacheCreation)/1e6*p.CacheCreation +
			float64(t.CacheRead)/1e6*p.CacheRead
	}
	return cost
}

// price is per-million-token cost in USD.
type price struct {
	Input, Output, CacheCreation, CacheRead float64
}

// priceFor returns USD-per-million-token rates for the listed Anthropic
// models. Conservatively defaults to Sonnet 4.6 pricing for unknown
// models so we never under-report cost on a new release.
func priceFor(model string) price {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "opus"):
		return price{Input: 15.0, Output: 75.0, CacheCreation: 18.75, CacheRead: 1.50}
	case strings.Contains(m, "haiku"):
		return price{Input: 1.0, Output: 5.0, CacheCreation: 1.25, CacheRead: 0.10}
	default: // sonnet / unknown
		return price{Input: 3.0, Output: 15.0, CacheCreation: 3.75, CacheRead: 0.30}
	}
}

// Walk scans every .jsonl under ~/.claude/projects/ and returns a single
// aggregate covering messages whose `timestamp` falls inside the requested
// window (now - duration ≤ ts ≤ now). Walk is safe to call concurrently
// with itself, but is not designed for high frequency — Bubble Tea
// dashboards should poll it every 5-10 seconds at most.
func Walk(window time.Duration) (*Aggregate, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	root := filepath.Join(home, ".claude", "projects")
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return &Aggregate{Window: window, ByModel: map[string]*Tokens{}, ByProject: map[string]*Tokens{}}, nil
		}
		return nil, err
	}

	now := time.Now()
	cutoff := now.Add(-window)
	agg := &Aggregate{
		Window:      window,
		WindowEnd:   now,
		ByModel:     map[string]*Tokens{},
		ByProject:   map[string]*Tokens{},
		WindowStart: cutoff,
	}

	type fileTask struct {
		path string
		proj string
	}
	// Cache project-name lookups per directory. The encoded directory
	// name (e.g. "-Users-skz-Projects-my-plain-blog") is lossy because
	// real paths can contain `-`, so projectFromEncoded would return
	// "blog" for "my-plain-blog". We avoid that by reading the `cwd`
	// field out of the first JSONL we open in each dir — Claude Code
	// records the real absolute path on every entry — and using
	// filepath.Base(cwd). One cache entry per encoded dir keeps the
	// cost to one peek per project, not one per transcript file.
	projCache := map[string]string{}
	var tasks []fileTask
	if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		parts := strings.SplitN(filepath.ToSlash(rel), "/", 2)
		encoded := parts[0]
		proj, ok := projCache[encoded]
		if !ok {
			proj = projectNameFromDir(filepath.Join(root, encoded))
			if proj == "" {
				proj = projectFromEncoded(encoded)
			}
			projCache[encoded] = proj
		}
		// Skip files whose mtime is older than the window — saves IO
		// when the user has a huge transcript history.
		if info, _ := d.Info(); info != nil && info.ModTime().Before(cutoff) {
			return nil
		}
		tasks = append(tasks, fileTask{path, proj})
		return nil
	}); err != nil {
		return nil, err
	}

	// Process files in parallel; merge under a mutex.
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, t := range tasks {
		wg.Add(1)
		sem <- struct{}{}
		go func(task fileTask) {
			defer wg.Done()
			defer func() { <-sem }()
			r := scanFile(task.path, cutoff)
			if r.assistantCount == 0 && r.userPrompts == 0 {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			agg.Total.Add(r.total)
			agg.Messages += r.assistantCount
			agg.UserPrompts += r.userPrompts
			proj := agg.ByProject[task.proj]
			if proj == nil {
				proj = &Tokens{}
				agg.ByProject[task.proj] = proj
			}
			proj.Add(r.total)
			for model, t := range r.byModel {
				mt := agg.ByModel[model]
				if mt == nil {
					mt = &Tokens{}
					agg.ByModel[model] = mt
				}
				mt.Add(*t)
			}
			if agg.FirstMessageInWindow.IsZero() || (!r.firstMsg.IsZero() && r.firstMsg.Before(agg.FirstMessageInWindow)) {
				agg.FirstMessageInWindow = r.firstMsg
			}
		}(t)
	}
	wg.Wait()

	return agg, nil
}

// scanResult bundles everything one transcript scan produces.
type scanResult struct {
	total          Tokens
	byModel        map[string]*Tokens
	firstMsg       time.Time
	assistantCount int // assistant messages with usage (drives token totals)
	userPrompts    int // type:"user" messages whose content is real text,
	// not tool_result blocks — Anthropic quota counter
}

// scanFile parses one JSONL and returns the per-file scan result over
// `cutoff..now`. Two distinct things are counted:
//
//   - assistant messages with a `usage` block → token totals + Aggregate.Messages
//   - type:"user" messages whose content is a fresh user prompt (string
//     content, OR an array with at least one {type:"text"} block) →
//     Aggregate.UserPrompts. We deliberately exclude tool_result follow-
//     ups, attachment events, and the like, because those don't count
//     toward Anthropic's per-window quota.
//
// Built to tolerate large lines (cached system prompts can push JSONL
// lines well above the default 64KB Scanner buffer).
func scanFile(path string, cutoff time.Time) scanResult {
	r := scanResult{byModel: map[string]*Tokens{}}
	f, err := os.Open(path)
	if err != nil {
		return r
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<17), 1<<25) // up to 32 MB / line
	for sc.Scan() {
		line := sc.Bytes()

		// Two cheap byte-level pre-filters: only json-decode lines that
		// might be assistant-usage or user-prompt records.
		hasUsage := maybeContains(line, []byte(`"usage":`))
		isUser := maybeContains(line, []byte(`"type":"user"`))
		if !hasUsage && !isUser {
			continue
		}

		var m struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Message   struct {
				Role    string          `json:"role"`
				Model   string          `json:"model"`
				Content json.RawMessage `json:"content"`
				Usage   *struct {
					Input         int `json:"input_tokens"`
					Output        int `json:"output_tokens"`
					CacheCreation int `json:"cache_creation_input_tokens"`
					CacheRead     int `json:"cache_read_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &m); err != nil {
			continue
		}
		ts, err := time.Parse(time.RFC3339, m.Timestamp)
		if err != nil || ts.Before(cutoff) {
			continue
		}

		// Assistant API response with usage.
		if m.Message.Usage != nil {
			t := Tokens{
				Input:         m.Message.Usage.Input,
				Output:        m.Message.Usage.Output,
				CacheCreation: m.Message.Usage.CacheCreation,
				CacheRead:     m.Message.Usage.CacheRead,
			}
			r.total.Add(t)
			r.assistantCount++
			if mb := r.byModel[m.Message.Model]; mb != nil {
				mb.Add(t)
			} else {
				r.byModel[m.Message.Model] = &Tokens{Input: t.Input, Output: t.Output, CacheCreation: t.CacheCreation, CacheRead: t.CacheRead}
			}
			if r.firstMsg.IsZero() || ts.Before(r.firstMsg) {
				r.firstMsg = ts
			}
		}

		// User prompt (filters out tool_result follow-ups).
		if m.Type == "user" && isFreshUserPrompt(m.Message.Content) {
			r.userPrompts++
		}
	}
	return r
}

// isFreshUserPrompt returns true when a JSONL "user" record's content
// represents a brand-new prompt from the human (vs. a tool_result
// follow-up). Two valid shapes:
//
//   - "content": "plain text"
//   - "content": [{"type":"text", ...}, ...]
//
// A pure tool_result message looks like:
//   - "content": [{"type":"tool_result", ...}]
//
// Anything else we conservatively count as a prompt — better to slightly
// over-count than to undercount and tell a user they have headroom they
// don't.
func isFreshUserPrompt(raw json.RawMessage) bool {
	raw = trimSpace(raw)
	if len(raw) == 0 {
		return false
	}
	// Plain string content → real prompt.
	if raw[0] == '"' {
		return true
	}
	// Array content: walk types. If any "text" present → real prompt.
	// If only tool_result entries → skip.
	if raw[0] == '[' {
		var arr []struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &arr); err != nil {
			return true // fail safe = count
		}
		hasText, allToolResult := false, true
		for _, e := range arr {
			if e.Type == "text" {
				hasText = true
			}
			if e.Type != "tool_result" {
				allToolResult = false
			}
		}
		if hasText {
			return true
		}
		if allToolResult {
			return false
		}
		return true
	}
	return true
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t' || b[0] == '\n' || b[0] == '\r') {
		b = b[1:]
	}
	for len(b) > 0 {
		c := b[len(b)-1]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			b = b[:len(b)-1]
			continue
		}
		break
	}
	return b
}

// maybeContains is a cheap byte-level substring check.
func maybeContains(line, sub []byte) bool {
	if len(sub) == 0 || len(line) < len(sub) {
		return false
	}
	for i := 0; i+len(sub) <= len(line); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			if line[i+j] != sub[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// (maybeHasUsage was inlined into scanFile alongside the user-prompt
// pre-filter via the more-general maybeContains helper above.)

// projectFromEncoded inverts the "/Users/skz/Projects/foo" →
// "-Users-skz-Projects-foo" encoding Claude Code uses for its project
// directory names. This is the lossy fallback path — projects whose
// basename contains a dash (`my-plain-blog`, `stickerly-import-bot`)
// will be truncated to the segment after the last dash. Prefer
// projectNameFromDir when a JSONL is available to read.
func projectFromEncoded(enc string) string {
	if i := strings.LastIndex(enc, "-"); i >= 0 && i < len(enc)-1 {
		return enc[i+1:]
	}
	return enc
}

// projectNameFromDir reads the first JSONL in `dir` and returns
// filepath.Base(cwd) where `cwd` is Claude Code's record of the real
// working directory. This recovers project names that contain dashes,
// which projectFromEncoded mangles. Returns "" on any failure so the
// caller can fall back gracefully.
func projectNameFromDir(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		if cwd := readCwdFromJSONL(filepath.Join(dir, e.Name())); cwd != "" {
			return filepath.Base(cwd)
		}
	}
	return ""
}

// readCwdFromJSONL scans up to the first ~32 lines of a transcript
// looking for a "cwd":"…" field. Claude Code stamps cwd on most
// message entries, but the very first lines are system records that
// may not carry it — we don't want to read the whole file just for
// one field.
func readCwdFromJSONL(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<16), 1<<24)
	for i := 0; sc.Scan() && i < 32; i++ {
		line := sc.Bytes()
		if !maybeContains(line, []byte(`"cwd":`)) {
			continue
		}
		var probe struct {
			Cwd string `json:"cwd"`
		}
		if err := json.Unmarshal(line, &probe); err != nil {
			continue
		}
		if probe.Cwd != "" {
			return probe.Cwd
		}
	}
	return ""
}

// HumanCount turns a token count into "1.2K" / "5.7M" form for the TUI.
func HumanCount(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 1_000_000:
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	default:
		return fmt.Sprintf("%.2fM", float64(n)/1_000_000)
	}
}
