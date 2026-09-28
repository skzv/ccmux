package codexusage

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

// fuzzBase anchors the generated rollouts' timestamps.
var fuzzBase = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

// genLine renders one rollout line of kind op; arg varies its model,
// tokens and cumulative total. ts is the line's timestamp.
func genLine(op, arg byte, ts time.Time) string {
	stamp := ts.Format("2006-01-02T15:04:05.000Z")
	usage := func(n int) string {
		return fmt.Sprintf(`{"input_tokens":%d,"cached_input_tokens":%d,"output_tokens":%d,"reasoning_output_tokens":0,"total_tokens":%d}`, n, n/3, n%7, n+n%7)
	}
	switch op % 14 {
	case 0:
		return fmt.Sprintf(`{"timestamp":%q,"type":"session_meta","payload":{"id":"s","source":"cli"}}`, stamp)
	case 1: // marks the whole rollout a subagent run
		return fmt.Sprintf(`{"timestamp":%q,"type":"session_meta","payload":{"id":"s","source":{"subagent":{"other":"guardian"}}}}`, stamp)
	case 2:
		return fmt.Sprintf(`{"timestamp":%q,"type":"turn_context","payload":{"model":%q}}`, stamp, []string{"gpt-5", "gpt-5-mini", "o3", ""}[arg%4])
	case 3, 4: // a small total space, so consecutive repeats happen
		return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":%s,"last_token_usage":%s}}}`, stamp, usage(int(arg%4)*100), usage(int(arg)+1))
	case 5: // no cumulative total: never a repeat
		return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":%s}}}`, stamp, usage(int(arg)))
	case 6:
		return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":null}}`, stamp)
	case 7:
		return fmt.Sprintf(`{"timestamp":%q,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fix it"}]}}`, stamp)
	case 8:
		return fmt.Sprintf(`{"timestamp":%q,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>x</environment_context>"}]}}`, stamp)
	case 9:
		return fmt.Sprintf(`{"timestamp":%q,"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}}`, stamp)
	case 10:
		return `{"timestamp":"` + stamp + `","type":"event_msg","payload":{"type":"token_count"` // malformed
	case 11: // no usable timestamp: in no window, but still moves the state
		return fmt.Sprintf(`{"timestamp":"","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":%s,"last_token_usage":%s}}}`, usage(int(arg%4)*100), usage(5))
	case 12:
		return ""
	default: // past "now"
		future := fuzzBase.Add(time.Duration(arg%10) * time.Minute).Format(time.RFC3339)
		return fmt.Sprintf(`{"timestamp":%q,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"later"}]}}`, future)
	}
}

// genRollout turns program — (kind, arg) byte pairs — into rollout
// text, timestamps spread over the ~30 hours before fuzzBase.
func genRollout(program []byte) string {
	var b strings.Builder
	for i := 0; i+1 < len(program); i += 2 {
		ts := fuzzBase.Add(-time.Duration(program[i+1]) * 7 * time.Minute)
		b.WriteString(genLine(program[i], program[i+1], ts))
		b.WriteByte('\n')
	}
	return b.String()
}

// sameResult compares scan results, treating a nil and an empty
// byModel alike.
func sameResult(a, b scanResult) bool {
	if len(a.byModel) == 0 && len(b.byModel) == 0 {
		a.byModel, b.byModel = nil, nil
	}
	return reflect.DeepEqual(a, b)
}

// FuzzIncrementalMatchesOneShot splits a generated rollout into append
// chunks — cut anywhere, mid-line included — with occasional
// truncations and replacements, and checks after every step that the
// cached, incremental parse tallies exactly what a one-shot scan of the
// same bytes does.
func FuzzIncrementalMatchesOneShot(f *testing.F) {
	f.Add([]byte{0, 200, 2, 150, 7, 140, 3, 139, 3, 138, 4, 137, 2, 30, 5, 29, 8, 28, 3, 1, 11, 1, 3, 2}, []byte{40, 7, 90, 255, 3, 120})
	f.Add([]byte{1, 100, 2, 90, 7, 80, 3, 70, 13, 3, 9, 2, 10, 1, 6, 1, 12, 0}, []byte{1, 1, 254, 60, 60})
	dir := f.TempDir()
	var execs atomic.Int64
	lookbacks := []time.Duration{time.Hour, 5 * time.Hour, 24 * time.Hour, 48 * time.Hour}
	f.Fuzz(func(t *testing.T, program, splits []byte) {
		if len(program) > 160 || len(splits) > 24 {
			return // keeps an exec cheap: every step re-scans the file
		}
		content := genRollout(program)
		path := filepath.Join(dir, fmt.Sprintf("rollout-%d.jsonl", execs.Add(1)))
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(path)
		c := jsonl.NewCache(cacheRetention, maxLineBytes, newFileRecords)
		written := 0
		for step, b := range append(splits, 0) {
			switch {
			case step == len(splits):
				appendBytes(t, path, content[written:])
				written = len(content)
			case b == 255 && written > 0:
				written /= 2
				if err := os.Truncate(path, int64(written)); err != nil {
					t.Fatal(err)
				}
			case b == 254:
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
			now := fuzzBase.Add(time.Duration(b%7) * 25 * time.Minute)
			sp := c.Span(now, lookbacks[int(b)%len(lookbacks)])
			fi, _ := os.Stat(path)
			var got scanResult
			c.Parse(path, fi, sp, func(f *fileRecords) { got = f.result(sp.Cutoff, now) })
			if want := legacyScanFile(path, sp.Cutoff, now); !sameResult(got, want) {
				t.Fatalf("step %d, window %v..%v: incremental %+v, fresh scan %+v", step, sp.Cutoff, now, got, want)
			}
		}
	})
}

func appendBytes(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

// walkFresh runs walkRoot against an empty cache: what every walk
// computed before rollouts were cached.
func walkFresh(t *testing.T, root string, window time.Duration, now time.Time) *Aggregate {
	t.Helper()
	saved := rollouts
	rollouts = jsonl.NewCache(cacheRetention, maxLineBytes, newFileRecords)
	defer func() { rollouts = saved }()
	agg, err := walkRoot(root, window, now)
	if err != nil {
		t.Fatal(err)
	}
	return agg
}

// TestWalkRoot_IncrementalMatchesFreshWalk drives the cached walker
// through appends, a half-written line, a repeated token_count split
// across appends, a late subagent marker, truncation, replacement,
// deletion and window changes, checking after each step that it
// reports exactly what a walk with an empty cache does.
func TestWalkRoot_IncrementalMatchesFreshWalk(t *testing.T) {
	root := t.TempDir()
	rollouts = jsonl.NewCache(cacheRetention, maxLineBytes, newFileRecords)
	now := time.Now()
	ts := func(ago time.Duration) string { return now.Add(-ago).UTC().Format("2006-01-02T15:04:05.000Z") }
	tok := func(ago time.Duration, total, last int) string {
		return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":%d,"output_tokens":1},"last_token_usage":{"input_tokens":%d,"cached_input_tokens":1,"output_tokens":1}}}}`+"\n", ts(ago), total, last)
	}
	check := func(step string, window time.Duration) {
		t.Helper()
		warm, err := walkRoot(root, window, now)
		if err != nil {
			t.Fatal(err)
		}
		if fresh := walkFresh(t, root, window, now); !reflect.DeepEqual(warm, fresh) {
			t.Fatalf("%s (window %v): cached walk differs from a fresh walk\n cached %+v\n fresh  %+v", step, window, warm, fresh)
		}
	}
	checkAll := func(step string) {
		t.Helper()
		check(step, 5*time.Hour)
		check(step, time.Hour)
	}

	a := fixtureRollout(t, root, "a",
		strings.TrimSuffix(turnContextLine(ts(3*time.Hour), "gpt-5"), "\n"),
		userMessageLine(ts(3*time.Hour), "hello"),
		strings.TrimSuffix(tok(3*time.Hour, 100, 100), "\n"))
	b := fixtureRollout(t, root, "b",
		turnContextLine(ts(2*time.Hour), "o3"),
		strings.TrimSuffix(tok(2*time.Hour, 50, 50), "\n"))
	checkAll("initial")

	appendBytes(t, a, turnContextLine(ts(30*time.Minute), "gpt-5-mini")+"\n"+userMessageLine(ts(30*time.Minute), "more")+"\n"+tok(29*time.Minute, 200, 100))
	checkAll("append, model switch")

	// Codex writing the same event twice, the repeat in a later chunk.
	appendBytes(t, a, tok(28*time.Minute, 200, 100))
	checkAll("repeated token_count across appends")

	line := strings.TrimSuffix(tok(20*time.Minute, 300, 100), "\n")
	appendBytes(t, b, line)
	checkAll("partial line, complete JSON")
	appendBytes(t, b, "\n"+line[:30])
	checkAll("partial line, cut mid-JSON")
	appendBytes(t, b, line[30:]+"\n")
	checkAll("partial line finished")

	// A session_meta marking the rollout a subagent run zeroes its
	// prompts wherever it appears.
	appendBytes(t, a, fmt.Sprintf(`{"timestamp":%q,"type":"session_meta","payload":{"source":{"subagent":{"other":"guardian"}}}}`+"\n", ts(10*time.Minute)))
	checkAll("late subagent marker")

	if err := os.WriteFile(a, []byte(userMessageLine(ts(time.Hour), "short")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	checkAll("truncated")

	tmp := b + ".tmp"
	body := turnContextLine(ts(time.Hour), "gpt-5") + "\n" + tok(59*time.Minute, 10, 10) + tok(58*time.Minute, 20, 10) + userMessageLine(ts(57*time.Minute), "again") + "\n"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, b); err != nil {
		t.Fatal(err)
	}
	checkAll("replaced")

	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	checkAll("deleted")

	fixtureRollout(t, root, "c", turnContextLine(ts(40*time.Hour), "o3"), strings.TrimSuffix(tok(40*time.Hour, 5, 5), "\n"), strings.TrimSuffix(tok(3*time.Minute, 6, 1), "\n"))
	check("48h: reaches past the kept floor", 48*time.Hour)
	check("40 days: uncached", 40*24*time.Hour)
	check("5h again", 5*time.Hour)
	now = now.Add(2 * time.Hour)
	checkAll("two hours later")
}

// TestWalkRoot_ConcurrentWalksWhileAppending — concurrent walks over a
// growing tree (run under -race) never disagree with a fresh walk once
// the writer stops.
func TestWalkRoot_ConcurrentWalksWhileAppending(t *testing.T) {
	root := t.TempDir()
	rollouts = jsonl.NewCache(cacheRetention, maxLineBytes, newFileRecords)
	now := time.Now()
	paths := []string{fixtureRollout(t, root, "x", turnContextLine(now.UTC().Format(time.RFC3339), "gpt-5")), fixtureRollout(t, root, "y")}
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
				if _, err := walkRoot(root, time.Duration(1+w)*time.Hour, now); err != nil {
					t.Error(err)
					return
				}
			}
		}(w)
	}
	for i := 0; i < 100; i++ {
		ts := now.Add(-time.Duration(100-i) * time.Second).UTC().Format(time.RFC3339)
		for _, p := range paths {
			appendBytes(t, p, tokenCountLine(ts, i+1, 0, 1)+"\n"+userMessageLine(ts, "hi")+"\n")
		}
	}
	close(stop)
	wg.Wait()
	for _, w := range []time.Duration{time.Hour, 4 * time.Hour} {
		warm, err := walkRoot(root, w, now)
		if err != nil {
			t.Fatal(err)
		}
		if fresh := walkFresh(t, root, w, now); !reflect.DeepEqual(warm, fresh) || warm.Messages != 200 {
			t.Fatalf("window %v: cached %+v, fresh %+v (want 200 events)", w, warm, fresh)
		}
	}
}
