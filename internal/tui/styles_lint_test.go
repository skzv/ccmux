package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoInlineStyleLiteralsInScreens enforces the redesign-tui-charm
// contract: screen files outside internal/tui/styles/ and
// internal/tui/components/ MUST NOT introduce literal palette colors
// or hand-rolled padding / margin integers. Every styled value must
// come from styles.Styles (tokens, semantic colors, or palette) or
// from the matrix-overlay decoration styles exposed alongside.
//
// Detection is regex-based on the raw source. The rule is intentionally
// coarse — it would rather false-positive on a clever construction than
// silently let a hex literal slip into a screen. If a new screen
// legitimately needs a token, add the token to internal/tui/styles/
// rather than expanding the allowlist.
func TestNoInlineStyleLiteralsInScreens(t *testing.T) {
	root := "."

	// allowedFiles is the small set of files that still carry inline
	// literals. Empty after Phase 4 cleanup; kept as a map (not a
	// nil) so a future regression is one line away from being
	// quietly allowed.
	allowedFiles := map[string]string{}

	colorRE := regexp.MustCompile(`lipgloss\.Color\("#`)
	// Spacing-literal rule: the (top, right, bottom, left) and
	// (vertical, horizontal) forms of Padding / Margin / Padding<Side>
	// / Margin<Side> when the first argument is a numeric literal.
	// Matches `.Padding(0`, `.PaddingLeft(2`, `.Margin(0,`, etc.
	spacingRE := regexp.MustCompile(`\.(Padding|Margin)(Left|Right|Top|Bottom)?\(\s*[0-9]`)

	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip the styles + components packages; that's where
			// literals are allowed by design.
			base := filepath.Base(path)
			if base == "styles" || base == "components" {
				return filepath.SkipDir
			}
			// Skip golden-test fixtures.
			if base == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Skip test files — they often build synthetic styles for
		// assertion purposes (golden seeds, fake palettes). The
		// production rule applies to the rendered TUI.
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// Skip this lint file's own regex strings.
		if filepath.Base(path) == "styles_lint_test.go" {
			return nil
		}

		base := filepath.Base(path)
		if _, ok := allowedFiles[base]; ok {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(data)

		if loc := colorRE.FindIndex(data); loc != nil {
			line := lineOf(src, loc[0])
			t.Errorf("%s:%d: inline lipgloss.Color(\"#...\") in screen file — use styles.Styles tokens (s.Semantic.* or s.P.*) instead", path, line)
		}
		if loc := spacingRE.FindIndex(data); loc != nil {
			line := lineOf(src, loc[0])
			t.Errorf("%s:%d: inline numeric literal in Padding/Margin call — use s.Spacing.* tokens instead", path, line)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk: %v", walkErr)
	}
}

func lineOf(src string, offset int) int {
	if offset > len(src) {
		offset = len(src)
	}
	return 1 + strings.Count(src[:offset], "\n")
}
