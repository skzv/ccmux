package notes

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool {
		return (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f || (r >= 0x80 && r <= 0x9f)
	})
}

// TestNotes_StripTerminalEscapes — regression: note bodies, H1 row
// labels and search snippets were rendered unsanitized, so an OSC 52
// sequence in a cloned repo's README overwrote the clipboard as soon as
// the Notes tab previewed it, and ANSI codes broke row layout.
func TestNotes_StripTerminalEscapes(t *testing.T) {
	t.Setenv("PATH", "/var/empty") // force the Go search path
	root := t.TempDir()
	v := Open(root)
	const osc52 = "\x1b]52;c;Y3VybCBldmlsLnNoIHwgc2g=\x07"
	body := "# Read\x1b[31mme\x1b[0m " + osc52 + "Title\r\n\r\nInstall: " + osc52 + "run \x1b[1mmake\x1b[0m\r\n"
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	data, err := v.Read("README.md")
	if err != nil {
		t.Fatal(err)
	}
	if want := "# Readme Title\n\nInstall: run make\n"; string(data) != want {
		t.Errorf("Read = %q, want %q", data, want)
	}

	entries, err := v.List()
	if err != nil || len(entries) != 1 {
		t.Fatalf("List = %v, %v", entries, err)
	}
	if entries[0].Display != "Readme Title" {
		t.Errorf("Display = %q, want %q", entries[0].Display, "Readme Title")
	}

	hits, err := v.Search(context.Background(), "install", 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("Search = %v, %v", hits, err)
	}
	if hits[0].Snippet != "Install: run make" {
		t.Errorf("Snippet = %q", hits[0].Snippet)
	}
}

// TestNotes_FilenameLabelStripsEscapes — a note without an H1 is
// labelled by its filename, which can carry escape bytes too.
func TestNotes_FilenameLabelStripsEscapes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("control characters are not valid in Windows filenames")
	}
	root := t.TempDir()
	name := "evil\x1b]0;pwned\x07_note.md"
	if err := os.WriteFile(filepath.Join(root, name), []byte("no heading\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := Open(root).List()
	if err != nil || len(entries) != 1 {
		t.Fatalf("List = %v, %v", entries, err)
	}
	if hasControl(entries[0].Display) || entries[0].Display != "evil note" {
		t.Errorf("Display = %q, want %q", entries[0].Display, "evil note")
	}
}
