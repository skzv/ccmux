package claudeusage

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/jsonl"
)

// fuzzBase anchors the generated transcripts' timestamps.
var fuzzBase = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

// genLine renders one transcript line of kind op; arg varies its ids,
// tokens and model. ts is the line's timestamp.
func genLine(op, arg byte, ts time.Time) string {
	stamp := ts.Format(time.RFC3339)
	model := []string{"claude-opus-4-7", "claude-sonnet-4-6"}[arg%2]
	usage := fmt.Sprintf(`{"input_tokens":%d,"output_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"cache_creation":{"ephemeral_1h_input_tokens":%d}}`,
		int(arg)+1, arg%7, arg%5*10, arg%11*100, arg%3)
	assistant := func(extra string) string {
		return fmt.Sprintf(`{"type":"assistant","timestamp":%q%s,"message":{"id":"msg_%d","role":"assistant","model":%q,"usage":%s}}`,
			stamp, extra, arg%4, model, usage)
	}
	user := func(extra, content string) string {
		return fmt.Sprintf(`{"type":"user","timestamp":%q%s,"message":{"role":"user","content":%s}}`, stamp, extra, content)
	}
	switch op % 20 {
	case 0:
		return user("", `"fix the bug"`)
	case 1:
		return user("", `[{"type":"text","text":"what is this"}]`)
	case 2:
		return user("", `[{"type":"tool_result","content":"ok"}]`)
	case 3: // a response from a small id space, so content blocks repeat pairs
		return assistant(fmt.Sprintf(`,"requestId":"req_%d"`, arg%3))
	case 4: // no ids: never deduped
		return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"role":"assistant","model":%q,"usage":%s}}`, stamp, model, usage)
	case 5:
		return assistant(fmt.Sprintf(`,"requestId":"req_%d","isSidechain":true`, arg%3))
	case 6:
		return user("", `"<command-name>/plan</command-name>\n<command-args>go</command-args>"`)
	case 7:
		return user("", `"<task-notification><task-id>1</task-id></task-notification>"`)
	case 8:
		return user(`,"isMeta":true`, `"<local-command-caveat>x</local-command-caveat>"`)
	case 9:
		return user(`,"isCompactSummary":true`, `"summary"`)
	case 10:
		return `{"type":"assistant","timestamp":"` + stamp + `","message":{"usage":` // malformed
	case 11:
		return fmt.Sprintf(`{"type":"system","timestamp":%q,"cwd":"/w/p"}`, stamp)
	case 12: // near or well past "now": the future-skew filter
		future := fuzzBase.Add(time.Duration(arg%10) * time.Minute).Format(time.RFC3339)
		return strings.Replace(assistant(fmt.Sprintf(`,"requestId":"req_f%d"`, arg%2)), stamp, future, 1)
	case 13:
		return ""
	case 14:
		return assistant(fmt.Sprintf(`,"requestId":"req_%d"`, arg%3)) + "\r"
	case 15:
		return user(`,"isSidechain":true`, `"subagent instructions"`)
	case 16:
		return strings.Replace(assistant(`,"requestId":"req_x"`), stamp, "yesterday", 1)
	case 17:
		return user("", `[{"type":"text","text":"[Request interrupted by user]"}]`)
	case 18:
		return user("", `"<local-command-stdout>done</local-command-stdout>"`)
	default: // a user record that also carries usage
		return fmt.Sprintf(`{"type":"user","timestamp":%q,"requestId":"req_u","message":{"id":"msg_u","content":"hi","usage":%s}}`, stamp, usage)
	}
}

// genTranscript turns program — (kind, arg) byte pairs — into
// transcript text, timestamps spread over the ~30 hours before
// fuzzBase in no particular order.
func genTranscript(program []byte) string {
	var b strings.Builder
	for i := 0; i+1 < len(program); i += 2 {
		ts := fuzzBase.Add(-time.Duration(program[i+1]) * 7 * time.Minute)
		b.WriteString(genLine(program[i], program[i+1], ts))
		b.WriteByte('\n')
	}
	return b.String()
}

// sameEvents compares event lists, treating nil and empty alike.
func sameEvents(a, b []usageEvent) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// checkIncremental asserts the cache's events for path match a
// one-shot legacy scan of the file as it is now: over one span picked
// by sel, or over all of them when all is set.
func checkIncremental(t *testing.T, c *jsonl.Cache[*fileRecords], path string, step int, sel byte, all bool) {
	t.Helper()
	lookbacks := []time.Duration{time.Hour, 5 * time.Hour, 10 * time.Hour, 48 * time.Hour}
	spans := []jsonl.Span{
		c.Span(fuzzBase, lookbacks[int(sel)%len(lookbacks)]),
		c.Span(fuzzBase.Add(time.Duration(sel%7)*25*time.Minute), 10*time.Hour),
	}
	if !all {
		spans = spans[sel%2 : sel%2+1]
	}
	for _, sp := range spans {
		fi, _ := os.Stat(path)
		var got []usageEvent
		c.Parse(path, fi, sp, func(f *fileRecords) { got = f.events(sp.Cutoff, sp.Now) })
		want := legacyScanFile(path, sp.Cutoff, sp.Now).events
		if !sameEvents(got, want) {
			t.Fatalf("step %d, span %v..%v: incremental events differ from a fresh scan\n got  %d events: %+v\n want %d events: %+v",
				step, sp.Cutoff.Format(time.RFC3339), sp.Now.Format(time.RFC3339), len(got), got, len(want), want)
		}
	}
}

// FuzzIncrementalMatchesOneShot splits a generated transcript into
// append chunks — cut anywhere, mid-line included — with occasional
// truncations and replacements, and checks after every step that the
// cached, incremental parse yields exactly the events a one-shot scan
// of the same bytes does.
func FuzzIncrementalMatchesOneShot(f *testing.F) {
	f.Add([]byte{0, 10, 3, 11, 3, 11, 2, 12, 3, 13, 6, 20, 18, 21, 3, 22, 7, 23, 4, 24}, []byte{40, 7, 90, 255, 3, 120})
	f.Add([]byte{3, 200, 3, 1, 0, 150, 5, 2, 12, 3, 13, 0, 14, 9, 19, 5, 1, 1, 3, 1}, []byte{1, 1, 1, 254, 60, 60})
	f.Add([]byte{16, 1, 10, 2, 11, 3, 15, 4, 17, 5, 8, 6, 9, 7}, []byte{200, 200})
	// One directory per fuzz worker, one file per exec: creating and
	// removing a temp dir on every exec dominated the run time.
	dir := f.TempDir()
	subDir := filepath.Join(dir, "sess", "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		f.Fatal(err)
	}
	var execs atomic.Int64
	f.Fuzz(func(t *testing.T, program, splits []byte) {
		if len(program) > 160 || len(splits) > 24 {
			return // keeps an exec cheap: every step re-scans the file
		}
		content := genTranscript(program)
		name := fmt.Sprintf("s%d.jsonl", execs.Add(1))
		path := filepath.Join(dir, name)
		if len(splits) > 0 && splits[0]%2 == 1 {
			path = filepath.Join(subDir, name) // subagent transcript: no prompts
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(path)
		c := jsonl.NewCache(cacheRetention, maxScanLineBytes, newFileRecords)
		written := 0
		for step, b := range append(splits, 0) {
			switch {
			case step == len(splits): // the rest, in one go
				appendBytes(t, path, content[written:])
				written = len(content)
			case b == 255 && written > 0: // truncate to half
				written /= 2
				if err := os.Truncate(path, int64(written)); err != nil {
					t.Fatal(err)
				}
			case b == 254: // atomically replace with the same bytes
				tmp := path + ".tmp"
				if err := os.WriteFile(tmp, []byte(content[:written]), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(tmp, path); err != nil {
					t.Fatal(err)
				}
			default:
				n := min(1+int(b)*3, len(content)-written)
				appendBytes(t, path, content[written:written+n])
				written += n
			}
			checkIncremental(t, c, path, step, b, step == len(splits))
		}
	})
}

func appendBytes(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

// walkFresh runs walk against an empty cache: what every walk computed
// before transcripts were cached.
func walkFresh(t *testing.T, now time.Time, d time.Duration, block bool) *Aggregate {
	t.Helper()
	saved := transcripts
	transcripts = jsonl.NewCache(cacheRetention, maxScanLineBytes, newFileRecords)
	defer func() { transcripts = saved }()
	agg, err := walk(now, d, block)
	if err != nil {
		t.Fatal(err)
	}
	return agg
}

// TestWalk_IncrementalMatchesFreshWalk drives the cached walker through
// the life of a transcript tree — appends, a half-written line,
// truncation, atomic replacement, deletion, new files, dedupe pairs
// split across appends and window changes — and after every step
// checks it reports exactly what a walk with an empty cache does.
func TestWalk_IncrementalMatchesFreshWalk(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	transcripts = jsonl.NewCache(cacheRetention, maxScanLineBytes, newFileRecords)
	now := time.Now()
	projects := filepath.Join(home, ".claude", "projects")
	file := func(project, name string) string {
		dir := filepath.Join(projects, project)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		return filepath.Join(dir, name)
	}
	stamp := func(ago time.Duration) string { return now.Add(-ago).UTC().Format(time.RFC3339) }
	prompt := func(ago time.Duration, cwd string) string {
		return fmt.Sprintf(`{"type":"user","timestamp":%q,"cwd":%q,"message":{"content":"go"}}`+"\n", stamp(ago), cwd)
	}
	response := func(ago time.Duration, id string, in int) string {
		return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"requestId":"r%s","message":{"id":"m%s","model":"claude-sonnet-4-6","usage":{"input_tokens":%d,"output_tokens":1}}}`+"\n", stamp(ago), id, id, in)
	}
	check := func(step string, d time.Duration, block bool) {
		t.Helper()
		warm, err := walk(now, d, block)
		if err != nil {
			t.Fatal(err)
		}
		if fresh := walkFresh(t, now, d, block); !reflect.DeepEqual(warm, fresh) {
			t.Fatalf("%s (d=%v block=%v): cached walk differs from a fresh walk\n cached %+v\n fresh  %+v", step, d, block, warm, fresh)
		}
	}
	checkAll := func(step string) {
		t.Helper()
		check(step, SessionBlock, true)
		check(step, time.Hour, false)
		check(step, SessionBlock, false)
	}

	a := file("-w-alpha", "a.jsonl")
	b := file("-w-beta", "b.jsonl")
	writeFileT(t, a, prompt(3*time.Hour, "/w/alpha")+response(3*time.Hour-time.Minute, "1", 100)+response(2*time.Hour, "2", 50))
	writeFileT(t, b, prompt(90*time.Minute, "/w/beta")+response(89*time.Minute, "1", 7))
	checkAll("initial")

	appendBytes(t, a, prompt(30*time.Minute, "/w/alpha")+response(29*time.Minute, "3", 5))
	checkAll("append")

	// A half-written line that is already valid JSON counts — as it
	// would for a fresh scan — but only once, however often it is
	// re-read before its newline lands.
	appendBytes(t, b, strings.TrimSuffix(prompt(25*time.Minute, "/w/beta"), "\n"))
	checkAll("partial line, complete JSON")
	line := strings.TrimSuffix(response(20*time.Minute, "4", 11), "\n")
	appendBytes(t, b, "\n"+line+"\n"+line[:40])
	checkAll("partial line, cut mid-JSON")
	appendBytes(t, b, line[40:]+"\n")
	checkAll("partial line finished (a content-block repeat of id 4)")

	// Dedupe spans appends: the same (id, requestId) pair again.
	appendBytes(t, a, response(29*time.Minute, "3", 5))
	checkAll("duplicate pair in a later chunk")

	writeFileT(t, a, prompt(3*time.Hour, "/w/alpha"))
	checkAll("truncated")

	tmp := b + ".new"
	writeFileT(t, tmp, prompt(time.Hour, "/w/beta")+response(59*time.Minute, "9", 1000)+response(58*time.Minute, "10", 1)+response(57*time.Minute, "11", 1))
	if err := os.Rename(tmp, b); err != nil {
		t.Fatal(err)
	}
	checkAll("replaced")

	c := file("-w-gamma", "c.jsonl")
	writeFileT(t, c, prompt(40*time.Minute, "/w/gamma")+response(39*time.Minute, "1", 3))
	sub := file(filepath.Join("-w-gamma", "sess", "subagents"), "agent-1.jsonl")
	writeFileT(t, sub, prompt(35*time.Minute, "/w/gamma")+response(34*time.Minute, "1", 4))
	checkAll("new files")

	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	checkAll("deleted")

	// Window changes: longer (re-reads past the kept floor), past the
	// retention (uncached), and a later "now" (prunes).
	appendBytes(t, c, response(30*time.Hour, "old", 9)+response(3*24*time.Hour, "older", 9))
	check("24h", 24*time.Hour, false)
	check("5 days", 5*24*time.Hour, false)
	check("40 days, uncached", 40*24*time.Hour, false)
	check("5h after 5 days", SessionBlock, true)
	now = now.Add(2 * time.Hour)
	checkAll("two hours later")
}

// TestWalk_ConcurrentWalksWhileAppending — the TUI refresh and
// /v1/usage requests can overlap: Walk and WalkRolling racing each other
// and a writer (run under -race) must leave the cache agreeing with a
// fresh walk once the writer stops.
func TestWalk_ConcurrentWalksWhileAppending(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	transcripts = jsonl.NewCache(cacheRetention, maxScanLineBytes, newFileRecords)
	now := time.Now()
	var paths []string
	for _, p := range []string{"-w-one", "-w-two"} {
		dir := filepath.Join(home, ".claude", "projects", p)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "s.jsonl")
		writeFileT(t, path, "")
		paths = append(paths, path)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := walk(now, time.Duration(1+w)*time.Hour, w%2 == 0); err != nil {
					t.Error(err)
					return
				}
			}
		}(w)
	}
	for i := 0; i < 100; i++ {
		ts := now.Add(-time.Duration(100-i) * time.Second).UTC().Format(time.RFC3339)
		for _, p := range paths {
			appendBytes(t, p, fmt.Sprintf(`{"type":"user","timestamp":%q,"message":{"content":"go"}}`+"\n", ts)+
				fmt.Sprintf(`{"type":"assistant","timestamp":%q,"requestId":"r%d","message":{"id":"m%d","model":"claude-sonnet-4-6","usage":{"input_tokens":1}}}`+"\n", ts, i, i))
		}
	}
	close(stop)
	wg.Wait()
	for _, block := range []bool{true, false} {
		warm, err := walk(now, time.Hour, block)
		if err != nil {
			t.Fatal(err)
		}
		// (A 1h block can split the 100s of messages at an hour
		// boundary, so only the rolling window must hold all 200.)
		if fresh := walkFresh(t, now, time.Hour, block); !reflect.DeepEqual(warm, fresh) || (!block && warm.Messages != 200) {
			t.Fatalf("block=%v: cached %+v, fresh %+v (want 200 messages)", block, warm, fresh)
		}
	}
}

func writeFileT(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// BenchmarkWalk compares a walk that parses every transcript (cold, as
// before the cache) with the dashboard's steady state: one transcript
// grew by a line since the last walk.
func BenchmarkWalk(b *testing.B) {
	home := b.TempDir()
	b.Setenv("HOME", home)
	now := time.Now()
	const files, exchanges = 24, 400 // ~24 × 200 KB
	pad := strings.Repeat("x", 200)
	var paths []string
	for i := 0; i < files; i++ {
		dir := filepath.Join(home, ".claude", "projects", fmt.Sprintf("-w-p%d", i%6))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			b.Fatal(err)
		}
		var sb strings.Builder
		for j := 0; j < exchanges; j++ {
			ts := now.Add(-time.Duration(exchanges-j) * 20 * time.Second).UTC().Format(time.RFC3339)
			fmt.Fprintf(&sb, `{"type":"user","timestamp":%q,"cwd":"/w/p","message":{"content":"prompt %s"}}`+"\n", ts, pad)
			fmt.Fprintf(&sb, `{"type":"assistant","timestamp":%q,"requestId":"r%d","message":{"id":"m%d","model":"claude-sonnet-4-6","content":[{"type":"text","text":%q}],"usage":{"input_tokens":10,"output_tokens":5}}}`+"\n", ts, j, j, pad)
		}
		p := filepath.Join(dir, fmt.Sprintf("s%d.jsonl", i))
		if err := os.WriteFile(p, []byte(sb.String()), 0o644); err != nil {
			b.Fatal(err)
		}
		paths = append(paths, p)
	}
	b.Run("cold", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			transcripts = jsonl.NewCache(cacheRetention, maxScanLineBytes, newFileRecords)
			if _, err := Walk(SessionBlock); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("warm", func(b *testing.B) {
		transcripts = jsonl.NewCache(cacheRetention, maxScanLineBytes, newFileRecords)
		if _, err := Walk(SessionBlock); err != nil {
			b.Fatal(err)
		}
		line := fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"model":"claude-sonnet-4-6","usage":{"input_tokens":1}}}`+"\n", now.UTC().Format(time.RFC3339))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			f, err := os.OpenFile(paths[i%len(paths)], os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				b.Fatal(err)
			}
			_, _ = f.WriteString(line)
			f.Close()
			b.StartTimer()
			if _, err := Walk(SessionBlock); err != nil {
				b.Fatal(err)
			}
		}
	})
}
