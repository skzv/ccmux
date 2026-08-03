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
		if loc := spacingLiteralRE.FindIndex(data); loc != nil {
			line := lineOf(src, loc[0])
			t.Errorf("%s:%d: inline numeric literal in Padding/Margin call — use s.Spacing.* tokens instead", path, line)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk: %v", walkErr)
	}
}

// spacingLiteralRE is the spacing-literal rule: the (top, right,
// bottom, left) and (vertical, horizontal) forms of Padding / Margin
// / Padding<Side> / Margin<Side> when the first argument is a numeric
// literal. Matches `.Padding(0`, `.PaddingLeft(2`, `.Margin(0,`, etc.
// The `\s*` after the dot makes the rule line-break tolerant: chained
// builder calls split across lines (`lipgloss.NewStyle().` newline
// `Padding(1, 3)`) used to evade the old `\.Padding` form and four
// screen files accumulated literals that way.
var spacingLiteralRE = regexp.MustCompile(`\.\s*(Padding|Margin)(Left|Right|Top|Bottom)?\(\s*[0-9]`)

// TestSpacingLintRegexCatchesLineBrokenChains pins the strengthened
// regex against the exact literal shapes that previously slipped by:
// a chained call with the method on its own line. Each "match" snippet
// reproduces one of the historical offenders (tour.go, confirmation.go,
// attach_loading.go, sshsetup_wizard.go); the "no match" snippets
// guard against false positives on token-based calls and prose
// comments.
func TestSpacingLintRegexCatchesLineBrokenChains(t *testing.T) {
	match := []string{
		`lipgloss.NewStyle().Padding(1, 3).Border(lipgloss.RoundedBorder())`,
		"lipgloss.NewStyle().\n\tPadding(1, 3).\n\tBorder(lipgloss.RoundedBorder())",
		"lipgloss.NewStyle().\n\t\tBorder(lipgloss.RoundedBorder()).\n\t\tPadding(1, 2).\n\t\tWidth(w)",
		"style.\n    MarginLeft(2)",
		`s.PaddingTop( 1)`,
	}
	for _, src := range match {
		if !spacingLiteralRE.MatchString(src) {
			t.Errorf("spacing lint failed to flag literal:\n%s", src)
		}
	}
	noMatch := []string{
		`lipgloss.NewStyle().Padding(s.Spacing.XS, s.Spacing.SM)`,
		"lipgloss.NewStyle().\n\tPadding(st.Spacing.SM, st.Spacing.LG)",
		`// border + 2 cells of Padding(0,1) horizontally`,
		`Padding(0, 1)`, // bare mention with no receiver dot (prose/comment)
	}
	for _, src := range noMatch {
		if spacingLiteralRE.MatchString(src) {
			t.Errorf("spacing lint false-positive on:\n%s", src)
		}
	}
}

func lineOf(src string, offset int) int {
	if offset > len(src) {
		offset = len(src)
	}
	return 1 + strings.Count(src[:offset], "\n")
}
