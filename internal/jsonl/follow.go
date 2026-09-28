package jsonl

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"time"
)

// sigLen is how many bytes just before a Cursor's offset it keeps to
// check, before reading on, that the file still holds what was parsed.
const sigLen = 64

// Cursor remembers how far an append-only JSONL file has been read, so
// the next Follow reads only the bytes appended since. The zero value
// starts at the beginning of the file.
//
// A Cursor is not safe for concurrent use.
type Cursor struct {
	off      int64  // bytes consumed: through the last newline read
	sig      []byte // the (up to) sigLen bytes ending at off
	dev, ino uint64 // file identity; zero where the platform has none

	// size and mtime are the file's as of the last complete Follow;
	// valid is false until one succeeds (and after any error).
	size  int64
	mtime time.Time
	valid bool
}

// Offset reports how many bytes of the file have been consumed: every
// complete (newline-terminated) line before it has been delivered.
func (c *Cursor) Offset() int64 { return c.off }

// Unchanged reports whether fi — a fresh stat of the file — matches the
// file as the last Follow left it, so a Follow now would find nothing
// new. Only a regular file qualifies: a symlink's own stat says
// nothing about its target.
//
// A same-size in-place rewrite that also keeps the modification time
// goes unnoticed until the file changes again; transcripts are
// append-only, so that doesn't happen in practice.
func (c *Cursor) Unchanged(fi fs.FileInfo) bool {
	if !c.valid || fi == nil || !fi.Mode().IsRegular() {
		return false
	}
	dev, ino := fileID(fi)
	return dev == c.dev && ino == c.ino && fi.Size() == c.size && fi.ModTime().Equal(c.mtime)
}

// Follow reads path from the cursor's offset to the end of the file.
// Each complete line is passed to line, in order, and consumed. A final
// line with no newline yet (a write in progress) is passed to partial
// without being consumed — the next Follow delivers it again, finished
// or not — and partial is called exactly once per Follow, with nil when
// there is no such line. Lines longer than maxLen are skipped, exactly
// as Scanner does.
//
// When the file is no longer the one the cursor was reading — replaced
// (a different inode), truncated below the offset, or rewritten so the
// bytes just before the offset differ — restart is called first and
// the file is read from the beginning: the caller must drop everything
// it derived from earlier lines. The same happens when the file can't
// be opened, since there is then nothing to derive anything from; the
// error is returned.
//
// Follow never reads a file the caller didn't ask about and never
// holds any lock; callers serialize access to one Cursor themselves.
func (c *Cursor) Follow(path string, maxLen int, restart func(), line, partial func([]byte)) error {
	c.valid = false
	f, err := os.Open(path)
	if err != nil {
		c.rewind(restart)
		partial(nil)
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		c.rewind(restart)
		partial(nil)
		return err
	}
	dev, ino := fileID(fi)
	if c.off > 0 && (dev != c.dev || ino != c.ino || fi.Size() < c.off || !c.sigMatches(f)) {
		c.rewind(restart)
	}
	if c.off > 0 {
		if _, err := f.Seek(c.off, io.SeekStart); err != nil {
			partial(nil)
			return err
		}
	}
	sc := NewScanner(f, maxLen)
	var tail []byte
	for sc.Scan() {
		if sc.eol {
			line(sc.Bytes())
		} else {
			tail = sc.Bytes() // only ever the last line
		}
	}
	partial(tail)
	c.off += sc.lastNL
	c.dev, c.ino = dev, ino
	c.sig = c.readSig(f)
	if sc.Err() != nil {
		return sc.Err()
	}
	if len(c.sig) == int(min(c.off, sigLen)) {
		c.size, c.mtime, c.valid = fi.Size(), fi.ModTime(), true
	}
	return nil
}

// rewind moves the cursor back to the start of the file, telling the
// caller to forget what it parsed when anything had been consumed.
func (c *Cursor) rewind(restart func()) {
	had := c.off > 0
	*c = Cursor{}
	if had {
		restart()
	}
}

// readSig returns the (up to) sigLen bytes ending at the offset, or
// nil when they can't be read.
func (c *Cursor) readSig(f *os.File) []byte {
	n := min(c.off, sigLen)
	if n == 0 {
		return nil
	}
	buf := make([]byte, n)
	if _, err := f.ReadAt(buf, c.off-n); err != nil {
		return nil
	}
	return buf
}

// sigMatches reports whether the bytes just before the offset are
// still the ones read last time.
func (c *Cursor) sigMatches(f *os.File) bool {
	if len(c.sig) != int(min(c.off, sigLen)) {
		return false
	}
	got := c.readSig(f)
	return got != nil && bytes.Equal(got, c.sig)
}
