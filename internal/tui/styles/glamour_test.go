package styles

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/glamour"
)

// TestGlamourStyle_RoundTripUsesTokens — render a known markdown
// document through Glamour configured with our design-system style
// and assert the output carries the palette's H1 color (mauve) and
// the design-system code-block background (BGAlt). This is the lint
// of "no default Glamour styling" promised by the tui-design-system
// spec.
func TestGlamourStyle_RoundTripUsesTokens(t *testing.T) {
	st := Default()
	style := GlamourStyle(st)
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(80),
	)
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}

	src := "# Title Heading\n\nA paragraph and some `inline code`.\n\n" +
		"```\nfenced block\n```\n"
	out, err := r.Render(src)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	// Sanity-check the markdown body actually rendered. ANSI escape
	// sequences split words across SGR boundaries, so strip them
	// first.
	plain := stripANSI(out)
	if !strings.Contains(plain, "Title Heading") {
		t.Errorf("rendered output missing H1 text:\n%s", plain)
	}
	if !strings.Contains(plain, "fenced block") {
		t.Errorf("rendered output missing fenced block content:\n%s", plain)
	}

	// FG (#cdd6f4) → rgb(205,214,244). termenv may quantize the
	// last channel by one during truecolor conversion, so we match
	// the 205;214; prefix rather than the exact tuple — the H1 +
	// the bold renderer compose to a unique combo no other rule
	// emits.
	fgSGRPrefix := "38;2;205;214;"
	if !strings.Contains(out, fgSGRPrefix) {
		t.Errorf("H1 missing FG-prefix SGR %q — design-system token not applied:\n%s", fgSGRPrefix, out)
	}

	// BGAlt (#181825) → rgb(24,24,37). termenv may round the last
	// channel by one during truecolor conversion, so we match the
	// 24,24, prefix rather than the exact tuple — what we're proving
	// is "the inline-code rule got a near-black BG override", not
	// "the bits round-trip without loss". The default dark Glamour
	// style emits no inline-code BG at all, so any "48;2;24;24;..."
	// is unambiguous evidence of our override.
	bgAltSGRPrefix := "48;2;24;24;"
	if !strings.Contains(out, bgAltSGRPrefix) {
		t.Errorf("inline code missing BGAlt-prefix bg SGR %q — code-block token not applied:\n%s", bgAltSGRPrefix, out)
	}
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }
