package notes

import (
	"os"
	"path/filepath"
	"testing"
)

// TestExtractH1_ClosingSequence — regression: the H1 pattern ate any
// trailing '#', so "# Learning C#" was listed as "Learning C". Only a
// CommonMark closing sequence (whitespace, then '#'s) is dropped.
func TestExtractH1_ClosingSequence(t *testing.T) {
	cases := []struct{ line, want string }{
		{"# Learning C#", "Learning C#"},
		{"# C# and F#", "C# and F#"},
		{"# Title #", "Title"},
		{"# Title ###   ", "Title"},
		{"#\tTabbed", "Tabbed"},
		{"# Issue #42", "Issue #42"},
		{"# Plain", "Plain"},
		{"#NoSpace", ""},
		{"## Second level", ""},
	}
	dir := t.TempDir()
	for i, tc := range cases {
		p := filepath.Join(dir, string(rune('a'+i))+".md")
		if err := os.WriteFile(p, []byte(tc.line+"\n\nbody\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := extractH1(p); got != tc.want {
			t.Errorf("extractH1(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}
