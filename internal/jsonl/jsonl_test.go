package jsonl

import (
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

func collect(s *Scanner) []string {
	var out []string
	for s.Scan() {
		out = append(out, s.Text())
	}
	return out
}

// TestScanner_SkipsOversizedLineAndContinues — the reason this package
// exists: bufio.Scanner would stop at the long line and lose "c".
func TestScanner_SkipsOversizedLineAndContinues(t *testing.T) {
	long := strings.Repeat("x", 200_000)
	in := "a\n" + long + "\nb\r\nc"
	s := NewScanner(strings.NewReader(in), 1024)
	got := collect(s)
	if strings.Join(got, ",") != "a,b,c" {
		t.Errorf("lines = %q, want [a b c]", got)
	}
	if s.Skipped() != 1 || s.Err() != nil {
		t.Errorf("skipped=%d err=%v, want 1, nil", s.Skipped(), s.Err())
	}
}

func TestScanner_OversizedFinalLine(t *testing.T) {
	s := NewScanner(strings.NewReader("a\n"+strings.Repeat("y", 5000)), 100)
	if got := collect(s); len(got) != 1 || got[0] != "a" {
		t.Errorf("lines = %q", got)
	}
	if s.Skipped() != 1 {
		t.Errorf("skipped = %d, want 1", s.Skipped())
	}
}

func TestScanner_LineAtExactlyTheCapFits(t *testing.T) {
	line := strings.Repeat("z", 99) // plus "\n" = 100 bytes
	s := NewScanner(strings.NewReader(line+"\nnext\n"), 100)
	if got := collect(s); len(got) != 2 || got[0] != line {
		t.Errorf("lines = %d, want the 99-byte line kept", len(got))
	}
}

func TestScanner_EmptyLinesAndBreak(t *testing.T) {
	s := NewScanner(strings.NewReader("\n\nx\ny\n"), 10)
	var got []string
	for s.Scan() {
		got = append(got, s.Text())
		if s.Text() == "x" {
			break
		}
	}
	if strings.Join(got, "|") != "||x" {
		t.Errorf("got %q", got)
	}
}

func TestScanner_ReportsReadError(t *testing.T) {
	boom := errors.New("disk gone")
	s := NewScanner(io.MultiReader(strings.NewReader("a\n"), iotest.ErrReader(boom)), 10)
	if got := collect(s); len(got) != 1 {
		t.Errorf("lines = %q", got)
	}
	if !errors.Is(s.Err(), boom) {
		t.Errorf("Err = %v, want %v", s.Err(), boom)
	}
}

// FuzzScanner — for any input, the lines returned are exactly the
// input's lines that fit, in order.
func FuzzScanner(f *testing.F) {
	f.Add("a\nbb\nccc", 2)
	f.Add("\r\n\n", 1)
	f.Fuzz(func(t *testing.T, in string, max int) {
		if max < 1 || max > 1<<16 {
			return
		}
		var want []string
		parts := strings.SplitAfter(in, "\n")
		for _, p := range parts {
			if p == "" {
				continue
			}
			if len(p) > max {
				continue
			}
			want = append(want, strings.TrimSuffix(strings.TrimSuffix(p, "\n"), "\r"))
		}
		got := collect(NewScanner(strings.NewReader(in), max))
		if strings.Join(got, "\x00") != strings.Join(want, "\x00") || len(got) != len(want) {
			t.Errorf("in=%q max=%d: got %q, want %q", in, max, got, want)
		}
	})
}
