package jsonl

import (
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Parser turns one file's lines into whatever a Cache keeps for it.
// A Cache calls these with the file's lock held, one at a time.
type Parser interface {
	// Line parses one complete line. A record timestamped before from
	// is outside every span the cache will serve from this state, so
	// the Parser may drop it — but state it carries from line to line
	// regardless of timestamps (Codex's current model, say) must still
	// advance.
	Line(line []byte, from time.Time)
	// Partial parses the file's unterminated last line (nil when there
	// is none), replacing what the previous Partial produced: a write
	// in progress is delivered again, finished or not, on every read.
	Partial(line []byte)
	// Restart forgets everything parsed so far; the file will be read
	// again from its first byte.
	Restart()
	// Prune drops what was parsed from records timestamped before from.
	Prune(from time.Time)
}

// Cache keeps per-file parse state for a tree of append-only JSONL
// transcripts, so repeated walks over a time window read only the
// bytes appended since the last walk instead of re-parsing every file.
//
// What is kept, and for how long:
//
//   - Records timestamped within the largest lookback any walk asked
//     for in the last keepTTL (at least the current walk's) — the
//     "floor". A walk that reaches further back than a file's kept
//     records re-parses that file from the start; one that reaches
//     back beyond the retention bypasses the cache entirely.
//   - A file no walk has touched for idleTTL is dropped, as is one
//     that disappeared from its root (see Sweep).
//
// The cache lives in memory for the life of the process; nothing is
// written to disk. It is safe for concurrent use: the map is guarded
// by one mutex held only for lookups, and each file has its own lock,
// held while that file is read — concurrent walks read different
// files in parallel, and the second walk to reach a file finds it
// already up to date.
type Cache[P Parser] struct {
	newParser func(path string) P
	retention time.Duration
	maxLen    int
	fsys      fs.FS // nil: the OS

	mu    sync.Mutex
	files map[string]*cacheEntry[P]
	// recent maps each lookback walks asked for in the last keepTTL to
	// when it was last asked for; the floor honors the largest.
	recent map[time.Duration]time.Time
}

type cacheEntry[P Parser] struct {
	used time.Time // last walk to touch it; guarded by Cache.mu

	mu     sync.Mutex
	ok     bool      // parser, from and cur are initialized
	from   time.Time // every record at or after this has been kept
	cur    Cursor
	parser P
}

const (
	// keepTTL is how long a walk's lookback keeps its records in the
	// cache after the last walk that asked for it.
	keepTTL = time.Hour
	// idleTTL drops a file no walk has touched for this long.
	idleTTL = time.Hour
	// pruneStep is how far the floor may advance past a file's oldest
	// kept record before the stale records are pruned — pruning is a
	// pass over the file's records, so it isn't done on every walk.
	pruneStep = time.Hour
)

// NewCache returns an empty cache. retention bounds how far back any
// cached walk may look; lines longer than maxLen are skipped, as
// Scanner does; newParser makes the empty state for a file.
func NewCache[P Parser](retention time.Duration, maxLen int, newParser func(path string) P) *Cache[P] {
	return &Cache[P]{
		newParser: newParser,
		retention: retention,
		maxLen:    maxLen,
		files:     map[string]*cacheEntry[P]{},
		recent:    map[time.Duration]time.Time{},
	}
}

// WithFS makes the cache read files from fsys instead of the OS, so
// tests can drive it over an in-memory tree; see Cursor.FollowFS. Call
// it before the cache is used.
func (c *Cache[P]) WithFS(fsys fs.FS) *Cache[P] {
	c.fsys = fsys
	return c
}

// Span is the time range one walk reads: records at or after Cutoff,
// as of Now. Make one with Cache.Span.
type Span struct {
	Cutoff, Now time.Time
	floor       time.Time // kept for other walks: records at or after this
	uncached    bool      // reaches back past the retention
}

// Span starts a walk covering [now-lookback, now].
func (c *Cache[P]) Span(now time.Time, lookback time.Duration) Span {
	sp := Span{Cutoff: now.Add(-lookback), Now: now}
	if lookback > c.retention {
		sp.floor, sp.uncached = sp.Cutoff, true
		return sp
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recent[lookback] = now
	keep := lookback
	for l, at := range c.recent {
		if now.Sub(at) > keepTTL {
			delete(c.recent, l)
		} else if l > keep {
			keep = l
		}
	}
	sp.floor = now.Add(-keep)
	return sp
}

// Parse brings the cached state for path up to date for sp and passes
// it to use, which must not keep it: the state belongs to the cache
// and is only stable while use runs. info is the file's stat from the
// directory walk (nil when unavailable); when it shows the file
// unchanged since the last read, no IO happens at all.
//
// A file that can't be read yields an empty state, as a fresh parse of
// it would.
func (c *Cache[P]) Parse(path string, info fs.FileInfo, sp Span, use func(P)) {
	if sp.uncached {
		p := c.newParser(path)
		var cur Cursor
		_ = cur.FollowFS(c.fsys, path, c.maxLen, p.Restart,
			func(line []byte) { p.Line(line, sp.floor) }, p.Partial)
		use(p)
		return
	}
	e := c.entry(path, sp.Now)
	e.mu.Lock()
	defer e.mu.Unlock()
	switch {
	case !e.ok || sp.Cutoff.Before(e.from):
		// First sight of the file, or a walk reaching back past what
		// was kept: start over from the first byte.
		e.parser, e.from, e.cur, e.ok = c.newParser(path), sp.floor, Cursor{}, true
	case sp.floor.Sub(e.from) >= pruneStep:
		e.parser.Prune(sp.floor)
		e.from = sp.floor
	}
	if !e.cur.Unchanged(info) {
		from := e.from
		// A read error leaves the state consistent with the lines
		// that were delivered, and the cursor retries from there.
		_ = e.cur.FollowFS(c.fsys, path, c.maxLen, e.parser.Restart,
			func(line []byte) { e.parser.Line(line, from) }, e.parser.Partial)
	}
	use(e.parser)
}

// ParseFile feeds every line of path to p once, bypassing any cache,
// dropping records before from.
func ParseFile(path string, maxLen int, from time.Time, p Parser) error {
	var cur Cursor
	return cur.Follow(path, maxLen, p.Restart, func(line []byte) { p.Line(line, from) }, p.Partial)
}

// entry returns path's cache entry, creating it if needed, and marks
// it used at now.
func (c *Cache[P]) entry(path string, now time.Time) *cacheEntry[P] {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.files[path]
	if e == nil {
		e = &cacheEntry[P]{}
		c.files[path] = e
	}
	e.used = now
	return e
}

// Sweep ends a walk of root: files under root that the walk didn't see
// (present holds every path it listed) are dropped, and so is any file
// idle for longer than idleTTL.
func (c *Cache[P]) Sweep(root string, present map[string]bool, now time.Time) {
	prefix := filepath.Clean(root) + string(filepath.Separator)
	c.mu.Lock()
	defer c.mu.Unlock()
	for path, e := range c.files {
		if (strings.HasPrefix(path, prefix) && !present[path]) || now.Sub(e.used) > idleTTL {
			delete(c.files, path)
		}
	}
}

// Len reports how many files the cache holds.
func (c *Cache[P]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.files)
}
