// Package jsonl reads newline-delimited records (agent transcripts)
// without bufio.Scanner's failure mode: when a line exceeds Scanner's
// buffer it stops with ErrTooLong and every later line in the file is
// silently lost — a single pasted screenshot or huge tool result in a
// transcript used to truncate previews, message counts and token
// totals. Scanner here skips just the oversized line and keeps going.
package jsonl

import (
	"bufio"
	"io"
)

// Scanner mirrors the bufio.Scanner loop shape (Scan / Bytes / Err) so
// call sites convert one-for-one, but skips lines longer than the cap
// instead of aborting.
type Scanner struct {
	br      *bufio.Reader
	max     int
	buf     []byte
	err     error
	done    bool
	skipped int
}

// NewScanner reads lines from r, skipping any longer than maxLen bytes.
func NewScanner(r io.Reader, maxLen int) *Scanner {
	return &Scanner{br: bufio.NewReaderSize(r, 64*1024), max: maxLen}
}

// Scan advances to the next line that fits, reporting false at EOF or
// on a read error. Trailing "\n" / "\r\n" are stripped, as with
// bufio.ScanLines; a final line without a newline is still returned.
func (s *Scanner) Scan() bool {
	if s.done {
		return false
	}
	s.buf = s.buf[:0]
	tooLong := false
	for {
		chunk, err := s.br.ReadSlice('\n')
		if !tooLong && len(chunk) > 0 {
			s.buf = append(s.buf, chunk...)
			if len(s.buf) > s.max {
				s.buf, tooLong = s.buf[:0], true
			}
		}
		switch err {
		case nil: // end of a line
			if tooLong {
				s.skipped++
				tooLong = false
				continue
			}
			s.buf = trimLineEnding(s.buf)
			return true
		case bufio.ErrBufferFull: // same line continues
			continue
		default: // EOF or a read error
			s.done = true
			if err != io.EOF {
				s.err = err
			}
			if tooLong {
				s.skipped++
				return false
			}
			if len(s.buf) > 0 {
				s.buf = trimLineEnding(s.buf)
				return true
			}
			return false
		}
	}
}

// Bytes returns the current line. It is only valid until the next Scan.
func (s *Scanner) Bytes() []byte { return s.buf }

// Text returns the current line as a string.
func (s *Scanner) Text() string { return string(s.buf) }

// Err returns the first non-EOF read error. Oversized lines are not
// errors; see Skipped.
func (s *Scanner) Err() error { return s.err }

// Skipped reports how many lines were dropped for exceeding the cap.
func (s *Scanner) Skipped() int { return s.skipped }

func trimLineEnding(b []byte) []byte {
	if n := len(b); n > 0 && b[n-1] == '\n' {
		b = b[:n-1]
	}
	if n := len(b); n > 0 && b[n-1] == '\r' {
		b = b[:n-1]
	}
	return b
}
