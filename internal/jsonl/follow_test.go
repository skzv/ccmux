package jsonl

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// follower records what a Cursor delivers across Follows.
type follower struct {
	cur      Cursor
	lines    []string // committed lines, reset by restart
	partial  string   // the last Follow's partial line ("" when none)
	restarts int
}

func (f *follower) follow(t *testing.T, path string, maxLen int) error {
	t.Helper()
	return f.cur.Follow(path, maxLen,
		func() { f.lines = nil; f.restarts++ },
		func(l []byte) { f.lines = append(f.lines, string(l)) },
		func(l []byte) { f.partial = string(l) })
}

func (f *follower) got() string { return strings.Join(f.lines, "|") }

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendFile(t *testing.T, path, body string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(body); err != nil {
		t.Fatal(err)
	}
}

func stat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

// TestCursor_ReadsOnlyAppendedLines — each Follow delivers just the
// lines appended since the previous one; a line still being written is
// handed over as partial, not consumed, and delivered whole once its
// newline lands.
func TestCursor_ReadsOnlyAppendedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	writeFile(t, path, "a\nb\n")
	var f follower
	if err := f.follow(t, path, 100); err != nil {
		t.Fatal(err)
	}
	if f.got() != "a|b" || f.partial != "" || f.cur.Offset() != 4 {
		t.Fatalf("first read: lines %q partial %q off %d", f.got(), f.partial, f.cur.Offset())
	}

	appendFile(t, path, "c\r\nd") // d is mid-write
	f.lines = nil
	if err := f.follow(t, path, 100); err != nil {
		t.Fatal(err)
	}
	if f.got() != "c" || f.partial != "d" || f.cur.Offset() != 7 {
		t.Fatalf("second read: lines %q partial %q off %d", f.got(), f.partial, f.cur.Offset())
	}

	appendFile(t, path, "one\ne")
	f.lines = nil
	if err := f.follow(t, path, 100); err != nil {
		t.Fatal(err)
	}
	if f.got() != "done" || f.partial != "e" {
		t.Fatalf("third read: lines %q partial %q", f.got(), f.partial)
	}
	appendFile(t, path, "\n")
	f.lines = nil
	if err := f.follow(t, path, 100); err != nil {
		t.Fatal(err)
	}
	if f.got() != "e" || f.partial != "" || f.restarts != 0 {
		t.Fatalf("fourth read: lines %q partial %q restarts %d", f.got(), f.partial, f.restarts)
	}
}

// TestCursor_SkipsOversizedLinesLikeScanner — an oversized complete
// line is consumed and dropped; an oversized unfinished one is neither
// delivered nor consumed.
func TestCursor_SkipsOversizedLinesLikeScanner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	writeFile(t, path, "a\n"+strings.Repeat("x", 50)+"\nb\n"+strings.Repeat("y", 50))
	var f follower
	if err := f.follow(t, path, 10); err != nil {
		t.Fatal(err)
	}
	if f.got() != "a|b" || f.partial != "" || f.cur.Offset() != 55 {
		t.Fatalf("lines %q partial %q off %d", f.got(), f.partial, f.cur.Offset())
	}
	appendFile(t, path, "\nc\n")
	f.lines = nil
	if err := f.follow(t, path, 10); err != nil {
		t.Fatal(err)
	}
	if f.got() != "c" {
		t.Fatalf("after the long line ended: lines %q", f.got())
	}
}

// TestCursor_RestartsWhenFileIsNotAContinuation — truncation, an
// atomic replacement and an in-place rewrite all make the next Follow
// restart from byte 0.
func TestCursor_RestartsWhenFileIsNotAContinuation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.jsonl")
	cases := []struct {
		name   string
		mutate func()
		want   string
	}{
		{"truncated", func() { writeFile(t, path, "z\n") }, "z"},
		{"replaced by rename", func() {
			tmp := filepath.Join(dir, "new")
			writeFile(t, tmp, "first line\nsecond line\nthird line\n")
			if err := os.Rename(tmp, path); err != nil {
				t.Fatal(err)
			}
		}, "first line|second line|third line"},
		// Same inode, grown past the old offset, but the bytes before
		// the offset changed.
		{"rewritten in place", func() {
			fh, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				t.Fatal(err)
			}
			defer fh.Close()
			if _, err := fh.WriteString("rewritten one\nrewritten two\nrewritten three\nfour\n"); err != nil {
				t.Fatal(err)
			}
		}, "rewritten one|rewritten two|rewritten three|four"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writeFile(t, path, "alpha\nbravo\ncharlie\n")
			var f follower
			if err := f.follow(t, path, 100); err != nil {
				t.Fatal(err)
			}
			tc.mutate()
			if err := f.follow(t, path, 100); err != nil {
				t.Fatal(err)
			}
			if f.restarts != 1 || f.got() != tc.want {
				t.Fatalf("restarts %d lines %q, want 1 and %q", f.restarts, f.got(), tc.want)
			}
		})
	}
}

// TestCursor_MissingFileRestarts — a file that vanished yields nothing
// and an error; what was parsed from it is dropped.
func TestCursor_MissingFileRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	writeFile(t, path, "a\n")
	var f follower
	if err := f.follow(t, path, 100); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := f.follow(t, path, 100); err == nil {
		t.Fatal("Follow of a deleted file returned nil")
	}
	if f.restarts != 1 || len(f.lines) != 0 || f.cur.Offset() != 0 {
		t.Fatalf("restarts %d lines %q off %d", f.restarts, f.got(), f.cur.Offset())
	}
}

// TestCursor_FollowFS — the same contract over an in-memory tree, which
// the usage packages' fuzz targets rely on.
func TestCursor_FollowFS(t *testing.T) {
	fsys := fstest.MapFS{"a/t.jsonl": {Data: []byte("a\nb")}}
	var c Cursor
	var lines []string
	var partial string
	follow := func() error {
		return c.FollowFS(fsys, "a/t.jsonl", 100,
			func() { lines = nil },
			func(l []byte) { lines = append(lines, string(l)) },
			func(l []byte) { partial = string(l) })
	}
	if err := follow(); err != nil || strings.Join(lines, "|") != "a" || partial != "b" {
		t.Fatalf("first: err %v lines %q partial %q", err, lines, partial)
	}
	fsys["a/t.jsonl"].Data = []byte("a\nbc\nd\n")
	if err := follow(); err != nil || strings.Join(lines, "|") != "a|bc|d" || partial != "" {
		t.Fatalf("after append: err %v lines %q partial %q", err, lines, partial)
	}
	fsys["a/t.jsonl"].Data = []byte("z\n")
	if err := follow(); err != nil || strings.Join(lines, "|") != "z" {
		t.Fatalf("after truncation: err %v lines %q", err, lines)
	}
	if err := c.FollowFS(noSeekFS{fsys}, "a/t.jsonl", 100, func() {}, func([]byte) {}, func([]byte) {}); err == nil {
		t.Fatal("a file without ReadAt/Seek must be refused")
	}
}

// noSeekFS hides everything but fs.File's own methods.
type noSeekFS struct{ fs.FS }

func (n noSeekFS) Open(name string) (fs.File, error) {
	f, err := n.FS.Open(name)
	return struct{ fs.File }{f}, err
}

// TestCursor_Unchanged — the no-IO fast path holds only while size,
// mtime and identity all match what the last Follow read.
func TestCursor_Unchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	writeFile(t, path, "a\n")
	var f follower
	if f.cur.Unchanged(stat(t, path)) {
		t.Fatal("a cursor that never read is not up to date")
	}
	if err := f.follow(t, path, 100); err != nil {
		t.Fatal(err)
	}
	if !f.cur.Unchanged(stat(t, path)) {
		t.Fatal("file untouched since the Follow must be Unchanged")
	}
	if f.cur.Unchanged(nil) {
		t.Fatal("a nil stat is never Unchanged")
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	if f.cur.Unchanged(stat(t, path)) {
		t.Fatal("a new mtime must force a read")
	}
	if err := f.follow(t, path, 100); err != nil {
		t.Fatal(err)
	}
	appendFile(t, path, "b\n")
	if f.cur.Unchanged(stat(t, path)) {
		t.Fatal("an append must force a read")
	}
}
