// Package claudeusage aggregates token usage and message counts from
// Claude Code's transcript JSONL files under ~/.claude/projects/. No API
// calls, no auth — pure local parse. We use this to power the dashboard
// usage panel, per-project breakdowns, and the subscription 5-hour
// session-block indicator (Pro / Max plan reset model; see Walk).
package claudeusage

import (
	"encoding/json"
	"fmt"
	"github.com/skzv/ccmux/internal/jsonl"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
	// CacheCreation1h is the part of CacheCreation written with the
	// 1-hour TTL, which is billed at 2x input instead of 1.25x.
	CacheCreation1h int `json:"cache_creation_1h,omitempty"`
}

// Add accumulates another Tokens into this one.
func (t *Tokens) Add(o Tokens) {
	t.Input += o.Input
	t.Output += o.Output
	t.CacheCreation += o.CacheCreation
	t.CacheRead += o.CacheRead
	t.CacheCreation1h += o.CacheCreation1h
}

// Total returns the sum of all four token categories. Useful for the
// dashboard headline number.
func (t Tokens) Total() int {
	return t.Input + t.Output + t.CacheCreation + t.CacheRead
}

// Aggregate is one rolled-up result from Walk() (the active session
// block) or WalkRolling() (a plain rolling window). Every count and
// token total covers the same span: [WindowStart, WindowEnd].
type Aggregate struct {
	Window time.Duration // block length (Walk) or window size (WalkRolling)
	// WindowStart is where the counted span begins: the active block's
	// hour-floored start for Walk (now, i.e. an empty span, when no
	// block is active), now-window for WalkRolling.
	WindowStart time.Time
	WindowEnd   time.Time // "now" at walk time
	Messages    int       // total assistant API responses with usage data
	// UserPrompts counts prompts the human sent — tool-result
	// follow-ups, harness-injected records (slash-command caveats and
	// stdout, image notes, compact summaries, task notifications,
	// interrupt markers) and subagent transcripts excluded. This is
	// what Anthropic's per-block quota counts toward.
	UserPrompts int
	Total       Tokens
	ByModel     map[string]*Tokens
	ByProject   map[string]*Tokens
	// FirstMessageInWindow is the timestamp of the earliest assistant
	// message inside the counted span.
	FirstMessageInWindow time.Time
	// BlockStart is the active session block's start (Walk only); zero
	// from WalkRolling or when no block is active.
	BlockStart time.Time
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

// ResetAt reports when the active session block ends and the quota
// resets: BlockStart + window. Aggregates without a block (hand-built,
// or from WalkRolling) fall back to the oldest message + window, a
// rough estimate at best.
//
// Returns the zero time when there is no active block — the next
// message starts a fresh one.
func (a *Aggregate) ResetAt(window time.Duration) time.Time {
	if !a.BlockStart.IsZero() {
		return a.BlockStart.Add(window)
	}
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
		write1h := min(t.CacheCreation1h, t.CacheCreation)
		cost += float64(t.Input)/1e6*p.Input +
			float64(t.Output)/1e6*p.Output +
			float64(t.CacheCreation-write1h)/1e6*p.CacheWrite5m +
			float64(write1h)/1e6*p.CacheWrite1h +
			float64(t.CacheRead)/1e6*p.CacheRead
	}
	return cost
}

// price is per-million-token cost in USD.
type price struct {
	Input, Output, CacheWrite5m, CacheWrite1h, CacheRead float64
}

// priceFor returns USD-per-million-token rates for an Anthropic model
// id. Rates differ by version within a family (Opus 4.5 cut Opus from
// $15/$75 to $5/$25), so the version is parsed out of ids in both the
// current "claude-opus-4-5-20251101" and the older "claude-3-5-haiku"
// shapes. Cache writes are 1.25x input (5-minute TTL) or 2x (1-hour),
// cache reads 0.1x unless the model has its own read rate. Unknown
// models are priced as Sonnet 4.6.
func priceFor(model string) price {
	m := strings.ToLower(model)
	var in, out float64
	read := -1.0 // -1: the standard 0.1x input
	switch {
	case strings.Contains(m, "fable") || strings.Contains(m, "mythos"):
		in, out, read = 10, 50, 1.0
		if v := familyVersion(m, "fable"); v >= 5.1 {
			read = 0.25
		}
	case strings.Contains(m, "opus"):
		switch v := familyVersion(m, "opus"); {
		case v >= 5.5:
			in, out, read = 4, 20, 0.20
		case v >= 4.5:
			in, out = 5, 25
		default: // Opus 3, 4, 4.1
			in, out = 15, 75
		}
	case strings.Contains(m, "haiku"):
		switch v := familyVersion(m, "haiku"); {
		case v >= 4:
			in, out = 1, 5
		case v >= 3.5:
			in, out = 0.80, 4
		case v > 0:
			in, out = 0.25, 1.25
		default:
			in, out = 1, 5
		}
	case strings.Contains(m, "sonnet") && familyVersion(m, "sonnet") >= 5:
		in, out = 2, 10
	default: // Sonnet 3.x-4.x, and unknown models
		in, out = 3, 15
	}
	if read < 0 {
		read = in * 0.1
	}
	return price{Input: in, Output: out, CacheWrite5m: in * 1.25, CacheWrite1h: in * 2, CacheRead: read}
}

// familyVersion extracts the model version around a family name:
// "claude-opus-4-5-20251101" → 4.5, "claude-opus-5" → 5,
// "claude-3-5-haiku-20241022" → 3.5, "claude-3-opus" → 3. Returns 0
// when no version is present. Date suffixes (8 digits) are ignored.
func familyVersion(m, family string) float64 {
	parts := strings.Split(m, "-")
	idx := -1
	for i, p := range parts {
		if p == family {
			idx = i
			break
		}
	}
	if idx < 0 {
		return 0
	}
	small := func(i int) (int, bool) {
		if i < 0 || i >= len(parts) || len(parts[i]) == 0 || len(parts[i]) > 2 {
			return 0, false
		}
		n, err := strconv.Atoi(parts[i])
		return n, err == nil
	}
	combine := func(major, minor int, hasMinor bool) float64 {
		if !hasMinor {
			return float64(major)
		}
		return float64(major) + float64(minor)/10
	}
	// New shape: family-major[-minor]
	if major, ok := small(idx + 1); ok {
		minor, hasMinor := small(idx + 2)
		return combine(major, minor, hasMinor)
	}
	// Old shape: major[-minor]-family
	if n, ok := small(idx - 1); ok {
		if major, ok := small(idx - 2); ok {
			return combine(major, n, true)
		}
		return float64(n)
	}
	return 0
}

// SessionBlock is the length of Anthropic's subscription usage block —
// the "5-hour limit" on Pro/Max plans.
const SessionBlock = 5 * time.Hour

// Walk returns usage for the currently active session block of length
// `block` (SessionBlock for Claude subscriptions). This is what the
// dashboard's quota bar and "resets in" line read.
//
// Anthropic's limit is NOT a rolling [now-5h, now] window. A block
// starts at the first message after the previous block ended and lasts
// `block`; everything sent inside it counts against it. Blocks are
// computed the way ccusage (`ccusage blocks`) computes them, so the two
// readouts on the dashboard agree:
//
//   - assistant API responses are sorted by timestamp;
//   - the first one opens a block whose start is floored to the UTC hour;
//   - a response more than `block` after the current block's start
//     opens the next block (again floored to the hour);
//   - the last block is active while now < start+block and the most
//     recent response is less than `block` old.
//
// Every Aggregate field — token totals, ByModel, ByProject, Messages and
// UserPrompts — covers only the active block: responses from its first
// response onward, and the user prompts those responses answered. When
// no block is active (idle for a while, or the last block ran out) the
// aggregate is empty and ResetAt is zero: the next message opens a new
// block.
//
// Blocks are chained from activity in the last 2×block. Under
// continuous use longer than that, the chain's phase can differ from
// one computed over the full history (ccusage reads everything); a
// ≥block idle gap anywhere in the lookback, which any break of that
// length provides, makes the result exact.
//
// Walk is safe to call concurrently with itself, but is not designed
// for high frequency — Bubble Tea dashboards should poll it every 5-10
// seconds at most.
func Walk(block time.Duration) (*Aggregate, error) {
	return walk(time.Now(), block, true)
}

// WalkRolling aggregates every message whose timestamp falls inside the
// plain rolling window [now-window, now] — no session-block semantics,
// so BlockStart stays zero. Used by the cross-agent usage summary
// (internal/usage), which reports every agent over the same window.
func WalkRolling(window time.Duration) (*Aggregate, error) {
	return walk(time.Now(), window, false)
}

// fileEvents is one transcript's scan result tagged with its project.
type fileEvents struct {
	proj   string
	events []usageEvent
}

func walk(now time.Time, d time.Duration, block bool) (*Aggregate, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	root := filepath.Join(home, ".claude", "projects")
	agg := &Aggregate{
		Window:      d,
		WindowStart: now.Add(-d),
		WindowEnd:   now,
		ByModel:     map[string]*Tokens{},
		ByProject:   map[string]*Tokens{},
	}
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			if block {
				agg.WindowStart = now
			}
			return agg, nil
		}
		return nil, err
	}

	// A block can have started up to one block-length ago, and finding
	// where it started needs the block before it too.
	lookback := d
	if block {
		lookback = 2 * d
	}
	cutoff := now.Add(-lookback)

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
		// Skip files whose mtime is older than the lookback — saves IO
		// when the user has a huge transcript history.
		if info, _ := d.Info(); info != nil && info.ModTime().Before(cutoff) {
			return nil
		}
		tasks = append(tasks, fileTask{path, proj})
		return nil
	}); err != nil {
		return nil, err
	}

	// Scan files in parallel; results are merged once all are in,
	// because the block boundaries depend on every file's timestamps.
	results := make([]fileEvents, len(tasks))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, t := range tasks {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, task fileTask) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = fileEvents{proj: task.proj, events: scanFile(task.path, cutoff, now).events}
		}(i, t)
	}
	wg.Wait()

	from := cutoff
	if block {
		var responses []time.Time
		for _, fe := range results {
			for _, ev := range fe.events {
				if !ev.prompt {
					responses = append(responses, ev.ts)
				}
			}
		}
		start, first, ok := activeBlock(responses, d, now)
		if !ok {
			agg.WindowStart = now
			return agg, nil
		}
		agg.BlockStart = start
		agg.WindowStart = start
		from = first
	}

	for _, fe := range results {
		var r scanResult
		r.tally(fe.events, from)
		if r.assistantCount == 0 && r.userPrompts == 0 {
			continue
		}
		agg.Total.Add(r.total)
		agg.Messages += r.assistantCount
		agg.UserPrompts += r.userPrompts
		proj := agg.ByProject[fe.proj]
		if proj == nil {
			proj = &Tokens{}
			agg.ByProject[fe.proj] = proj
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
		if !r.firstMsg.IsZero() && (agg.FirstMessageInWindow.IsZero() || r.firstMsg.Before(agg.FirstMessageInWindow)) {
			agg.FirstMessageInWindow = r.firstMsg
		}
	}
	return agg, nil
}

// activeBlock chains `responses` into session blocks of length `block`
// the way ccusage does (see Walk) and reports the block active at
// `now`: its hour-floored start, and the timestamp of its first
// response — the membership boundary, since a response in
// [start, first) belongs to the previous block. ok is false when no
// block is active.
func activeBlock(responses []time.Time, block time.Duration, now time.Time) (start, first time.Time, ok bool) {
	if len(responses) == 0 {
		return time.Time{}, time.Time{}, false
	}
	sort.Slice(responses, func(i, j int) bool { return responses[i].Before(responses[j]) })
	for i, ts := range responses {
		if i == 0 || ts.Sub(start) > block {
			// Truncate rounds down relative to the zero time, which is
			// UTC-hour aligned — ccusage's floorToHour.
			start, first = ts.Truncate(time.Hour), ts
		}
	}
	last := responses[len(responses)-1]
	if now.Sub(last) >= block || !now.Before(start.Add(block)) {
		return time.Time{}, time.Time{}, false
	}
	return start, first, true
}

// usageEvent is one countable record from a transcript.
type usageEvent struct {
	ts     time.Time
	prompt bool // a user prompt; otherwise an assistant API response
	tokens Tokens
	model  string
}

// scanResult bundles everything one transcript scan produces.
type scanResult struct {
	events         []usageEvent
	total          Tokens
	byModel        map[string]*Tokens
	firstMsg       time.Time
	assistantCount int // assistant messages with usage (drives token totals)
	userPrompts    int // prompts the human sent (see promptKindOf) —
	// the Anthropic quota counter
}

// tally (re)computes the summary fields over the events at or after
// `from`.
func (r *scanResult) tally(events []usageEvent, from time.Time) {
	r.total, r.byModel, r.firstMsg = Tokens{}, map[string]*Tokens{}, time.Time{}
	r.assistantCount, r.userPrompts = 0, 0
	for _, ev := range events {
		if ev.ts.Before(from) {
			continue
		}
		if ev.prompt {
			r.userPrompts++
			continue
		}
		r.total.Add(ev.tokens)
		r.assistantCount++
		if mb := r.byModel[ev.model]; mb != nil {
			mb.Add(ev.tokens)
		} else {
			tc := ev.tokens
			r.byModel[ev.model] = &tc
		}
		if r.firstMsg.IsZero() || ev.ts.Before(r.firstMsg) {
			r.firstMsg = ev.ts
		}
	}
}

// scanFile parses one JSONL and returns the per-file scan result over
// `cutoff..now`. Two distinct things are counted:
//
//   - assistant messages with a `usage` block → token totals + Aggregate.Messages
//   - prompts the human actually sent → Aggregate.UserPrompts. Tool-result
//     follow-ups, harness-injected records (isMeta caveats and image
//     notes, compact summaries, slash-command stdout, task
//     notifications, interrupt markers) and subagent/sidechain
//     transcripts are not prompts; see promptKindOf.
//
// A prompt is stamped with the timestamp of the first assistant
// response after it (falling back to its own), so it lands in the same
// session block as the API call it triggered. A slash-command record
// counts as a prompt only when the model answers it: `/plan do X`
// sends a turn, `/model` does not.
//
// Built to tolerate large lines (cached system prompts can push JSONL
// lines well above the default 64KB Scanner buffer). A line beyond the
// 32 MB per-line cap (giant base64 pastes) is skipped individually and
// the scan continues — a bufio.Scanner would instead stop at ErrTooLong
// and silently drop every later message in the transcript.
//
// Messages stamped in the future (clock skew, a machine restored from a
// bad RTC) are excluded — beyond a small tolerance — so they can't
// inflate the current window or skew the ResetAt forecast.
//
// Dedup: Claude Code writes one JSONL line per content block of the same
// API response, and every one of those lines repeats the identical
// `usage` object. Counting each line multiplied token totals ~2-2.6x.
// Each (message.id, requestId) pair is therefore counted once per file —
// retries stay within one transcript, so a per-file set is sufficient.
// Lines missing either id are counted unconditionally (fail open — we'd
// rather slightly over-count than silently drop usage).
func scanFile(path string, cutoff, now time.Time) (r scanResult) {
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
	forEachLine(f, maxScanLineBytes, func(line []byte) {
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
			if !alreadyCounted(seenUsage, m.Message.ID, m.RequestID) {
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

// maxScanLineBytes caps how much of one JSONL line scanFile buffers —
// the same 32 MB budget the previous bufio.Scanner had, but enforced
// per line instead of aborting the whole file.
const maxScanLineBytes = 1 << 25

// futureSkewTolerance is how far into the future a message timestamp
// may sit and still be counted. Covers ordinary clock skew between the
// machine that wrote the transcript and the one aggregating it without
// letting a badly future-dated message (broken RTC, TZ mishap) pollute
// the current window.
const futureSkewTolerance = 2 * time.Minute

// forEachLine invokes fn for every line in rd of at most maxLen bytes;
// a longer line is skipped and iteration continues (see internal/jsonl
// for why bufio.Scanner can't be used here).
func forEachLine(rd io.Reader, maxLen int, fn func(line []byte)) {
	sc := jsonl.NewScanner(rd, maxLen)
	for sc.Scan() {
		fn(sc.Bytes())
	}
}

// alreadyCounted reports whether this (message.id, requestId) pair has
// already contributed usage in this file, recording it if not. When
// either id is missing there is nothing safe to key on, so the line is
// treated as new (never deduped) — fail open.
func alreadyCounted(seen map[string]struct{}, msgID, requestID string) bool {
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

// promptKind classifies a type:"user" transcript record for the quota
// counter. Records flagged isMeta / isCompactSummary / isSidechain, and
// everything in a subagent transcript, are filtered out before this.
type promptKind int

const (
	// notPrompt: harness-written text that neither counts nor affects
	// a pending slash command — slash-command stdout and caveats,
	// bash-mode echoes, "[Request interrupted by user]" markers.
	notPrompt promptKind = iota
	// humanPrompt: something the user typed and sent to the model.
	humanPrompt
	// commandPrompt: a slash-command record. It counts only when the
	// model answers it (`/plan do X` sends a turn; `/model` does not).
	commandPrompt
	// turnBoundary: a record that starts a model turn the human did not
	// type (tool results, background-task notifications). It cancels a
	// pending slash command so that turn's response isn't credited to it.
	turnBoundary
)

// promptKindOf classifies a user record by its message content.
func promptKindOf(raw json.RawMessage) promptKind {
	if !isFreshUserPrompt(raw) {
		return turnBoundary // tool_result follow-up
	}
	text := strings.TrimSpace(leadingText(raw))
	switch {
	case strings.HasPrefix(text, "<command-name>"), strings.HasPrefix(text, "<command-message>"):
		return commandPrompt
	case strings.HasPrefix(text, "<task-notification>"):
		return turnBoundary
	case strings.HasPrefix(text, "<command-"),
		strings.HasPrefix(text, "<local-command-"),
		strings.HasPrefix(text, "<bash-"),
		strings.HasPrefix(text, "[Request interrupted"):
		return notPrompt
	}
	return humanPrompt
}

// leadingText returns a message content's string form, or the text of
// its first {type:"text"} block; "" for anything else.
func leadingText(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		for _, p := range parts {
			if p.Type == "text" {
				return p.Text
			}
		}
	}
	return ""
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
	sc := jsonl.NewScanner(f, 1<<24)
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
