package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/scaffold"
)

// TestPrintScaffoldReport_Created exercises the CLI formatter for the
// realistic "this upgrade actually did work" case. The friend-reported
// bug was that upgrade was silent; this is the test that locks in the
// fix — `+` lines for each created dir/file, a trailing summary.
func TestPrintScaffoldReport_Created(t *testing.T) {
	var buf bytes.Buffer
	printScaffoldReport(&buf, &scaffold.Result{
		Dir:          "/tmp/p",
		CreatedDirs:  []string{"docs/01_Specs", "docs/02_Architecture"},
		CreatedFiles: []string{"README.md", ".gitignore"},
	})
	out := buf.String()
	for _, want := range []string{
		"Upgrading /tmp/p:",
		"+ docs/01_Specs/",
		"+ docs/02_Architecture/",
		"+ README.md",
		"+ .gitignore",
		"added 2 dirs, README.md, .gitignore",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// TestPrintScaffoldReport_NoOp covers the previously-silent case:
// every dir/file already exists. Output should make it clear that
// upgrade ran and decided nothing needed doing, not that upgrade
// silently broke.
func TestPrintScaffoldReport_NoOp(t *testing.T) {
	var buf bytes.Buffer
	printScaffoldReport(&buf, &scaffold.Result{
		Dir:          "/tmp/p",
		SkippedDirs:  []string{"docs/01_Specs"},
		SkippedFiles: []string{"README.md"},
	})
	out := buf.String()
	for _, want := range []string{
		"Upgrading /tmp/p:",
		"· docs/01_Specs/ (exists)",
		"· README.md (exists)",
		"Already up to date.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "+ ") {
		t.Errorf("no-op run should have no '+ ' lines:\n%s", out)
	}
}

// TestPrintScaffoldReport_Nil — defensive guard against a nil result
// crashing the CLI if Scaffold ever returns (nil, err) and the caller
// hands the nil through.
func TestPrintScaffoldReport_Nil(t *testing.T) {
	var buf bytes.Buffer
	printScaffoldReport(&buf, nil)
	if buf.Len() != 0 {
		t.Errorf("expected empty output for nil result, got %q", buf.String())
	}
}
