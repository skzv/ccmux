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
	Messages    int           // total assistant messages with usage
	Total       Tokens
	ByModel     map[string]*Tokens
	ByProject   map[string]*Tokens
	// FirstMessageInWindow is the timestamp of the earliest assistant
	// message that still falls inside the window — used to compute the
	// "next reset at" time for Pro/Max subscription windows.
	FirstMessageInWindow time.Time
}

// ProjectTotal is one row in the per-project breakdown returned by
// AggregateReport.TopProjects.
type ProjectTotal struct {
	Project string
	Tokens  Tokens
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
		// The project directory name is the absolute path with slashes
		// replaced by dashes. We invert that mapping for display.
		proj := projectFromEncoded(parts[0])
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
			tot, byModel, firstMsg, msgCount := scanFile(task.path, cutoff)
			if msgCount == 0 {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			agg.Total.Add(tot)
			agg.Messages += msgCount
			proj := agg.ByProject[task.proj]
			if proj == nil {
				proj = &Tokens{}
				agg.ByProject[task.proj] = proj
			}
			proj.Add(tot)
			for model, t := range byModel {
				mt := agg.ByModel[model]
				if mt == nil {
					mt = &Tokens{}
					agg.ByModel[model] = mt
				}
				mt.Add(*t)
			}
			if agg.FirstMessageInWindow.IsZero() || firstMsg.Before(agg.FirstMessageInWindow) {
				agg.FirstMessageInWindow = firstMsg
			}
		}(t)
	}
	wg.Wait()

	return agg, nil
}

// scanFile parses one JSONL and returns the totals within `cutoff..now`.
// Built to tolerate large lines (cached system prompts can push lines
// well above the default 64KB Scanner buffer).
func scanFile(path string, cutoff time.Time) (total Tokens, byModel map[string]*Tokens, firstMsg time.Time, msgs int) {
	byModel = map[string]*Tokens{}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<17), 1<<25) // up to 32 MB / line
	for sc.Scan() {
		line := sc.Bytes()
		if !maybeHasUsage(line) {
			continue
		}
		var m struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Message   struct {
				Model string `json:"model"`
				Usage *struct {
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
		if m.Message.Usage == nil {
			continue
		}
		ts, err := time.Parse(time.RFC3339, m.Timestamp)
		if err != nil {
			continue
		}
		if ts.Before(cutoff) {
			continue
		}
		t := Tokens{
			Input:         m.Message.Usage.Input,
			Output:        m.Message.Usage.Output,
			CacheCreation: m.Message.Usage.CacheCreation,
			CacheRead:     m.Message.Usage.CacheRead,
		}
		total.Add(t)
		msgs++
		if mb := byModel[m.Message.Model]; mb != nil {
			mb.Add(t)
		} else {
			byModel[m.Message.Model] = &Tokens{Input: t.Input, Output: t.Output, CacheCreation: t.CacheCreation, CacheRead: t.CacheRead}
		}
		if firstMsg.IsZero() || ts.Before(firstMsg) {
			firstMsg = ts
		}
	}
	return
}

// maybeHasUsage is a cheap byte-level pre-filter: only attempt to JSON-
// parse lines that mention "usage". Cuts CPU when the transcript file
// has many non-usage event records (file-history-snapshot, permission-
// mode, tool_use, etc.).
func maybeHasUsage(line []byte) bool {
	for i := 0; i+7 < len(line); i++ {
		if line[i] == '"' && line[i+1] == 'u' && line[i+2] == 's' && line[i+3] == 'a' &&
			line[i+4] == 'g' && line[i+5] == 'e' && line[i+6] == '"' && line[i+7] == ':' {
			return true
		}
	}
	return false
}

// projectFromEncoded inverts the "/Users/skz/Projects/foo" →
// "-Users-skz-Projects-foo" encoding Claude Code uses for its project
// directory names. We don't try to reproduce the full path — just the
// basename, which is what shows up in the TUI.
func projectFromEncoded(enc string) string {
	if i := strings.LastIndex(enc, "-"); i >= 0 && i < len(enc)-1 {
		return enc[i+1:]
	}
	return enc
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
