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

// TestCursor_SameSizeRewriteRestarts — an early line changed in place,
// same inode, same size, so nothing appended: only the sigLen bytes
// before the offset were checked, and they hadn't changed, so the file
// was taken for up to date and the old line kept until a restart. Its
// modification time moved without the file growing — no append does
// that — so the next Follow reads it again from the start.
func TestCursor_SameSizeRewriteRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	tail := strings.Repeat("unchanged tail line\n", 5) // > sigLen after the edit
	writeFile(t, path, "tokens=100\n"+tail)
	var f follower
	if err := f.follow(t, path, 100); err != nil {
		t.Fatal(err)
	}
	fi := stat(t, path)
	fh, err := os.OpenFile(path, os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fh.WriteAt([]byte("tokens=900"), 0); err != nil {
		t.Fatal(err)
	}
	fh.Close()
	later := fi.ModTime().Add(time.Second) // however coarse the filesystem's clock
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	if got := stat(t, path); got.Size() != fi.Size() {
		t.Fatalf("setup: size %d → %d, want a same-size rewrite", fi.Size(), got.Size())
	}
	if err := f.follow(t, path, 100); err != nil {
		t.Fatal(err)
	}
	if f.restarts != 1 || !strings.HasPrefix(f.got(), "tokens=900|") {
		t.Fatalf("restarts %d lines %q, want 1 and the rewritten first line", f.restarts, f.got())
	}

	// Appends still read on from the offset.
	appendFile(t, path, "appended\n")
	f.lines = nil
	if err := f.follow(t, path, 100); err != nil {
		t.Fatal(err)
	}
	if f.restarts != 1 || f.got() != "appended" {
		t.Fatalf("after an append: restarts %d lines %q, want 1 and just the new line", f.restarts, f.got())
	}
}

// TestCursor_AppendDuringReadIsNotARewrite — an append that lands
// between a Follow's stat and its read is consumed by that Follow, past
// the size its stat reported. The next Follow finds a new modification
// time and a size equal to the offset; that is the append already read,
// not a rewrite, and must not cost a reparse.
func TestCursor_AppendDuringReadIsNotARewrite(t *testing.T) {
	t0 := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	file := &fstest.MapFile{Data: []byte("a\nb\n"), ModTime: t0.Add(time.Second)}
	fsys := fstest.MapFS{"t.jsonl": file}
	var c Cursor
	restarts, lines := 0, 0
	follow := func(fsys fs.FS) {
		t.Helper()
		if err := c.FollowFS(fsys, "t.jsonl", 100, func() { restarts++ }, func([]byte) { lines++ }, func([]byte) {}); err != nil {
			t.Fatal(err)
		}
	}
	// The stat saw "a\n" at t0; "b\n" was appended before the read.
	follow(staleStatFS{fsys, 2, t0})
	if lines != 2 || c.Offset() != 4 {
		t.Fatalf("setup: %d lines, offset %d; want both lines read", lines, c.Offset())
	}
	follow(fsys)
	if restarts != 0 || lines != 2 {
		t.Fatalf("restarts %d lines %d, want no restart and nothing new", restarts, lines)
	}
}

// staleStatFS serves fsys's files with a Stat that reports size and mtime
// as they were before the last append.
type staleStatFS struct {
	fstest.MapFS
	size int64
	mod  time.Time
}

func (s staleStatFS) Open(name string) (fs.File, error) {
	f, err := s.MapFS.Open(name)
	if err != nil {
		return nil, err
	}
	return staleFile{f.(randomAccessFile), s.size, s.mod}, nil
}

type staleFile struct {
	randomAccessFile
	size int64
	mod  time.Time
}

func (f staleFile) Stat() (fs.FileInfo, error) {
	fi, err := f.randomAccessFile.Stat()
	return staleInfo{fi, f.size, f.mod}, err
}

type staleInfo struct {
	fs.FileInfo
	size int64
	mod  time.Time
}

func (i staleInfo) Size() int64        { return i.size }
func (i staleInfo) ModTime() time.Time { return i.mod }

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
