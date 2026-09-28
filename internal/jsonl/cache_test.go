package jsonl

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// stampParser keeps lines of the form "<unix seconds> <text>".
type stampParser struct {
	recs     []stamped
	tail     []stamped
	lines    int // Line calls: how much was actually parsed
	restarts int
	prunes   int
}

type stamped struct {
	ts   time.Time
	text string
}

func parseStamped(line []byte) (stamped, bool) {
	sec, text, ok := strings.Cut(string(line), " ")
	n, err := strconv.ParseInt(sec, 10, 64)
	if !ok || err != nil {
		return stamped{}, false
	}
	return stamped{time.Unix(n, 0), text}, true
}

func (p *stampParser) Line(line []byte, from time.Time) {
	p.lines++
	if s, ok := parseStamped(line); ok && !s.ts.Before(from) {
		p.recs = append(p.recs, s)
	}
}

func (p *stampParser) Partial(line []byte) {
	p.tail = p.tail[:0]
	if s, ok := parseStamped(line); ok {
		p.tail = append(p.tail, s)
	}
}

func (p *stampParser) Restart() { p.recs, p.tail = nil, nil; p.restarts++ }

func (p *stampParser) Prune(from time.Time) {
	p.prunes++
	kept := p.recs[:0]
	for _, r := range p.recs {
		if !r.ts.Before(from) {
			kept = append(kept, r)
		}
	}
	p.recs = kept
}

// texts lists what recs hold inside [cutoff, ∞).
func texts(cutoff time.Time, recs ...[]stamped) string {
	var out []string
	for _, rs := range recs {
		for _, r := range rs {
			if !r.ts.Before(cutoff) {
				out = append(out, r.text)
			}
		}
	}
	return strings.Join(out, ",")
}

func newStampCache() *Cache[*stampParser] {
	return NewCache(31*24*time.Hour, 1<<20, func(string) *stampParser { return &stampParser{} })
}

func stampLine(ts time.Time, text string) string {
	return fmt.Sprintf("%d %s\n", ts.Unix(), text)
}

// parse runs c.Parse and returns what the parser held inside the span
// (tail included) and its counters.
func parse(c *Cache[*stampParser], path string, sp Span) (got string, p stampParser) {
	got, _, p = parseCommitted(c, path, sp)
	return got, p
}

// parseCommitted is parse, also returning just the committed records.
func parseCommitted(c *Cache[*stampParser], path string, sp Span) (got, committed string, p stampParser) {
	fi, _ := os.Stat(path)
	c.Parse(path, fi, sp, func(sp2 *stampParser) {
		got = texts(sp.Cutoff, sp2.recs, sp2.tail)
		committed = texts(sp.Cutoff, sp2.recs)
		p = stampParser{lines: sp2.lines, restarts: sp2.restarts, prunes: sp2.prunes, recs: make([]stamped, len(sp2.recs))}
	})
	return got, committed, p
}

func TestCache_ParsesOnlyAppendedLines(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	path := filepath.Join(t.TempDir(), "a.jsonl")
	writeFile(t, path, stampLine(now.Add(-time.Hour), "one")+stampLine(now.Add(-time.Minute), "two"))
	c := newStampCache()

	got, p := parse(c, path, c.Span(now, 5*time.Hour))
	if got != "one,two" || p.lines != 2 {
		t.Fatalf("first parse: %q after %d lines", got, p.lines)
	}
	// Unchanged file: no IO, same answer.
	if got, p = parse(c, path, c.Span(now, 5*time.Hour)); got != "one,two" || p.lines != 2 {
		t.Fatalf("unchanged: %q after %d lines", got, p.lines)
	}
	appendFile(t, path, stampLine(now, "three"))
	if got, p = parse(c, path, c.Span(now, 5*time.Hour)); got != "one,two,three" || p.lines != 3 {
		t.Fatalf("after append: %q after %d lines, want 3 lines parsed in total", got, p.lines)
	}
}

// TestCache_SpanFloorHonorsRecentLookbacks — the floor keeps what the
// largest lookback asked for within keepTTL needs, then relaxes.
func TestCache_SpanFloorHonorsRecentLookbacks(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	c := newStampCache()
	check := func(sp Span, wantFloor time.Time) {
		t.Helper()
		if !sp.floor.Equal(wantFloor) || sp.uncached {
			t.Errorf("floor %v uncached %v, want %v", sp.floor, sp.uncached, wantFloor)
		}
	}
	check(c.Span(now, time.Hour), now.Add(-time.Hour))
	check(c.Span(now, 10*time.Hour), now.Add(-10*time.Hour))
	check(c.Span(now.Add(30*time.Minute), time.Hour), now.Add(30*time.Minute-10*time.Hour))
	check(c.Span(now.Add(50*time.Minute), 10*time.Hour), now.Add(50*time.Minute-10*time.Hour))
	// 10h was last asked for 70 minutes before this.
	check(c.Span(now.Add(2*time.Hour), time.Hour), now.Add(time.Hour))

	if sp := c.Span(now, 40*24*time.Hour); !sp.uncached {
		t.Error("a lookback past the retention must bypass the cache")
	}
}

// TestCache_ReparsesWhenSpanReachesPastKeptRecords — records older than
// a file's floor were never kept, so a longer lookback re-reads it.
func TestCache_ReparsesWhenSpanReachesPastKeptRecords(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	path := filepath.Join(t.TempDir(), "a.jsonl")
	writeFile(t, path, stampLine(now.Add(-3*time.Hour), "old")+stampLine(now.Add(-time.Minute), "new"))
	c := newStampCache()
	if got, _ := parse(c, path, c.Span(now, time.Hour)); got != "new" {
		t.Fatalf("1h: %q", got)
	}
	got, p := parse(c, path, c.Span(now, 5*time.Hour))
	if got != "old,new" || p.lines != 2 {
		t.Fatalf("5h: %q after %d lines, want a fresh parse of both", got, p.lines)
	}
}

// TestCache_PrunesRecordsBelowTheFloor — once the floor has moved on by
// pruneStep, records below it are dropped; results are unaffected.
func TestCache_PrunesRecordsBelowTheFloor(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	path := filepath.Join(t.TempDir(), "a.jsonl")
	writeFile(t, path, stampLine(now.Add(-4*time.Hour), "a")+stampLine(now.Add(-2*time.Hour), "b")+stampLine(now, "c"))
	c := newStampCache()
	if got, _ := parse(c, path, c.Span(now, 5*time.Hour)); got != "a,b,c" {
		t.Fatalf("got %q", got)
	}
	later := now.Add(90 * time.Minute)
	got, p := parse(c, path, c.Span(later, 5*time.Hour))
	if got != "b,c" || p.prunes != 1 || len(p.recs) != 2 || p.lines != 3 {
		t.Fatalf("after prune: %q prunes=%d recs=%d lines=%d", got, p.prunes, len(p.recs), p.lines)
	}
}

func TestCache_BeyondRetentionIsUncached(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	path := filepath.Join(t.TempDir(), "a.jsonl")
	writeFile(t, path, stampLine(now.Add(-35*24*time.Hour), "ancient")+stampLine(now, "c"))
	c := newStampCache()
	if got, _ := parse(c, path, c.Span(now, 40*24*time.Hour)); got != "ancient,c" {
		t.Fatalf("got %q", got)
	}
	if c.Len() != 0 {
		t.Fatalf("an uncached span left %d entries", c.Len())
	}
}

// TestCache_SweepDropsVanishedAndIdleFiles — a file its root no longer
// lists is dropped at the end of the walk; so is one idle past idleTTL;
// other roots are left alone until they idle out.
func TestCache_SweepDropsVanishedAndIdleFiles(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	root, other := t.TempDir(), t.TempDir()
	a, b, o := filepath.Join(root, "a.jsonl"), filepath.Join(root, "sub", "b.jsonl"), filepath.Join(other, "o.jsonl")
	if err := os.MkdirAll(filepath.Dir(b), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{a, b, o} {
		writeFile(t, p, stampLine(now, "x"))
	}
	c := newStampCache()
	sp := c.Span(now, time.Hour)
	for _, p := range []string{a, b, o} {
		parse(c, p, sp)
	}
	c.Sweep(root, map[string]bool{a: true}, now)
	if c.Len() != 2 {
		t.Fatalf("after sweeping root without b: %d entries, want 2 (a, o)", c.Len())
	}
	later := now.Add(idleTTL + time.Minute)
	parse(c, a, c.Span(later, time.Hour))
	c.Sweep(root, map[string]bool{a: true}, later)
	if c.Len() != 1 {
		t.Fatalf("after the idle sweep: %d entries, want only a", c.Len())
	}
}

// TestCache_ConcurrentWalksWhileAppending — walks racing each other and
// a writer (run under -race) each see a consistent prefix of the file,
// and once the writer stops every line is there exactly once.
func TestCache_ConcurrentWalksWhileAppending(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "a.jsonl"), filepath.Join(dir, "b.jsonl")}
	for _, p := range paths {
		writeFile(t, p, "")
	}
	c := newStampCache()
	const lines = 200
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				sp := c.Span(now, time.Duration(1+w%3)*time.Hour)
				present := map[string]bool{}
				for _, p := range paths {
					present[p] = true
					_, got, _ := parseCommitted(c, p, sp)
					// Every walk sees a gap-free prefix of the complete
					// lines: 0,1,2,…
					for i, s := range strings.Split(got, ",") {
						if got != "" && s != strconv.Itoa(i) {
							t.Errorf("walk saw %q: not a prefix", got)
							return
						}
					}
				}
				c.Sweep(dir, present, now)
			}
		}(w)
	}
	for i := 0; i < lines; i++ {
		for _, p := range paths {
			appendFile(t, p, stampLine(now, strconv.Itoa(i)))
		}
	}
	close(stop)
	wg.Wait()
	want := make([]string, lines)
	for i := range want {
		want[i] = strconv.Itoa(i)
	}
	for _, p := range paths {
		if got, _ := parse(c, p, c.Span(now, time.Hour)); got != strings.Join(want, ",") {
			t.Errorf("%s: final read has %d records, want %d in order", filepath.Base(p), len(strings.Split(got, ",")), lines)
		}
	}
}
