package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/tui/styles"
)

// TestSessionsEmptyState_DigitMatchesProjectsTab — regression for the
// "Press 3 to open Projects" bug. The Sessions screen renders an empty
// state when no sessions exist; the hint must reference the *current*
// Projects tab digit (derived from the Screen enum), not a stale
// hand-written number.
//
// If anyone reorders the Screen const block without updating the empty
// state, this test fails with a clear diagnostic.
func TestSessionsEmptyState_DigitMatchesProjectsTab(t *testing.T) {
	m := sessionsModel{
		st:       styles.Default(),
		sessions: nil, // forces the empty branch
	}
	out := m.renderList(80, 20)

	want := screenKey(ScreenProjects)
	// The hint reads `Press <digit> to open Projects` — the digit
	// must equal the Projects tab key. (Style spans wrap the digit
	// with ANSI codes, so check by substring on the rendered output.)
	hint := "Press " + want
	if !strings.Contains(stripANSIForTabKey(out), hint) && !containsDigitNear(out, want, "Projects") {
		t.Errorf("Sessions empty state should hint %q (the current Projects tab key); got:\n%s",
			hint, out)
	}

	// Belt and braces: it must not embed the *wrong* digit either.
	// A stale "Press 3" with Projects bound to 2 would fail here.
	for d := 1; d <= 9; d++ {
		num := itoaTest(d)
		if num == want {
			continue
		}
		stale := "Press " + num + " "
		if strings.Contains(stripANSIForTabKey(out), stale) {
			t.Errorf("Sessions empty state contains stale tab digit %q (current Projects key is %q):\n%s",
				stale, want, out)
		}
	}
}

// TestNotesEmptyState_DigitMatchesProjectsTab — same regression for
// the Notes screen's "no project selected" hint.
func TestNotesEmptyState_DigitMatchesProjectsTab(t *testing.T) {
	m := notesModel{
		st:      styles.Default(),
		project: nil, // forces the empty branch
	}
	out := m.View(100, 20)

	want := screenKey(ScreenProjects)
	if !containsDigitNear(out, want, "Projects tab") {
		t.Errorf("Notes empty state should reference the current Projects tab key %q; got:\n%s",
			want, out)
	}
}

// TestNoLiteralTabKeyDigits is the structural lint that closes the bug
// class. It scans every non-test Go file under internal/tui/ for two
// antipatterns:
//
//  1. m.st.Key.Render("N") — a styled single-digit key hint. The digit
//     should come from screenKey(ScreenX), not a literal.
//  2. "Press N " or "Press N/" — a raw "Press 3" in a user-facing
//     string. Same fix: use screenKey().
//
// Both shapes silently lie when the Screen const block is reordered.
// Catching them at the source means the next reviewer doesn't have to
// remember the rule — the build does.
//
// The fix in every case is: import the helper and write
// `screenKey(ScreenWhatever)` instead of the literal digit.
func TestNoLiteralTabKeyDigits(t *testing.T) {
	// Render("N") for any single digit — the empty-state pattern.
	renderLiteral := regexp.MustCompile(`Render\("[1-9]"\)`)
	// "Press N" where N is a single digit followed by space, slash, or
	// closing quote. Catches `"Press 1 / F1"` style copy. The leading
	// `\b` avoids matching `Express` etc.
	pressLiteral := regexp.MustCompile(`\bPress [1-9][ /"\\]`)

	root := "."
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Don't descend into the styles/components subpackages —
			// they don't render user copy referencing tab keys, and
			// keeping the scan local avoids false positives from any
			// future sibling file that legitimately needs digits.
			if path != "." && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		// Skip test files (they reference the patterns to assert on
		// them) and skip this lint file itself.
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		src := string(data)

		for i, line := range strings.Split(src, "\n") {
			lineNo := i + 1
			// Skip pure comment lines — the antipattern only matters in
			// real string literals that reach the user; docstring
			// examples and TODO notes are allowed to spell digits out.
			if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "//") {
				continue
			}
			if hit := renderLiteral.FindString(line); hit != "" {
				t.Errorf("%s:%d uses %s — that's a hand-written tab digit. "+
					"Use screenKey(ScreenX) so it tracks the enum.\n  line: %s",
					path, lineNo, hit, strings.TrimSpace(line))
			}
			if hit := pressLiteral.FindString(line); hit != "" {
				t.Errorf("%s:%d contains %q — a literal tab digit in user copy. "+
					"Use screenKey(ScreenX) so it tracks the enum.\n  line: %s",
					path, lineNo, hit, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/tui: %v", err)
	}
}

// containsDigitNear is a soft check: the wanted digit must appear
// within ~80 chars of the anchor word in the rendered output, both
// after ANSI stripping. The empty-state hints wrap the digit in style
// codes, so a naïve `strings.Contains("Press 2 to open Projects")`
// over the styled output misses the match.
func containsDigitNear(rendered, digit, anchor string) bool {
	plain := stripANSIForTabKey(rendered)
	idx := strings.Index(plain, anchor)
	if idx < 0 {
		return false
	}
	start := idx - 80
	if start < 0 {
		start = 0
	}
	end := idx + 80
	if end > len(plain) {
		end = len(plain)
	}
	return strings.Contains(plain[start:end], digit)
}

// stripANSIForTabKey removes ANSI escape sequences so the digit + anchor checks
// don't depend on style. A tiny regex is fine — these tests render
// short strings, not megabytes.
var ansiReForTabKey = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func stripANSIForTabKey(s string) string { return ansiReForTabKey.ReplaceAllString(s, "") }
