package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tui/styles"
)

const frontmatterDoc = "---\ntitle: Overview\ntags: [a, b]\n---\n# Overview\n\nBody text\n"

// TestNotesPreview_FrontmatterRendersAsFrontmatter — Glamour read YAML
// frontmatter as a horizontal rule plus a setext heading, so a note
// opened with "──────" and "## title: Overview". It must show the keys as
// they are, not as a heading, and still render the note body.
func TestNotesPreview_FrontmatterRendersAsFrontmatter(t *testing.T) {
	m := newNotes(styles.Default(), DefaultKeymap())
	m.SetSize(120, 40)
	m.project = &project.Project{Name: "p", Path: t.TempDir()}
	m.previewSrc = frontmatterDoc
	out := ansi.Strip(m.renderPreviewContent(80))
	if strings.Contains(out, "## title") {
		t.Errorf("frontmatter rendered as a heading:\n%s", out)
	}
	for _, want := range []string{"title: Overview", "tags: [a, b]", "Body text"} {
		if !strings.Contains(out, want) {
			t.Errorf("preview lacks %q:\n%s", want, out)
		}
	}
}

// TestAgentBrowserPreview_StripsEscapesAndFrontmatter — a command or
// skill file's preview kept "[31mRED[0m" (only the ESC byte was dropped)
// and showed its frontmatter as a rule + heading.
func TestAgentBrowserPreview_StripsEscapesAndFrontmatter(t *testing.T) {
	b := newAgentBrowser(styles.Default())
	b.SetSize(120, 30)
	b.SetSections("t", []agentBrowserSection{{Title: "Commands", Items: []agentBrowserItem{
		{Label: "/review", Markdown: true, Preview: "---\ndescription: Review a PR\n---\nSay \x1b[31mRED\x1b[0m loudly\n"},
		{Label: "hook", Preview: "command: echo \x1b]52;c;ZXZpbA==\x07hi"},
	}}})
	out := ansi.Strip(b.rendered)
	if strings.Contains(out, "[31m") || strings.Contains(out, "[0m") {
		t.Errorf("escape sequence remnants in the preview:\n%s", out)
	}
	if !strings.Contains(out, "RED") || !strings.Contains(out, "description: Review a PR") || strings.Contains(out, "## description") {
		t.Errorf("preview should show the text and the frontmatter as-is:\n%s", out)
	}
	b.moveCursor(+1)
	if strings.Contains(b.rendered, "\x1b]52") {
		t.Errorf("an OSC 52 sequence in a structured preview reached the terminal: %q", b.rendered)
	}
}

func TestSplitFrontmatter(t *testing.T) {
	cases := []struct {
		in, lang, fm, body string
		ok                 bool
	}{
		{frontmatterDoc, "yaml", "title: Overview\ntags: [a, b]", "# Overview\n\nBody text\n", true},
		{"+++\ntitle = \"x\"\n+++\nbody", "toml", "title = \"x\"", "body", true},
		{"---\nkey: v\n...\nbody", "yaml", "key: v", "body", true},
		{"---\nnever closed\n", "", "", "", false},
		{"# no frontmatter\n---\n", "", "", "", false},
	}
	for _, tc := range cases {
		lang, fm, body, ok := splitFrontmatter(tc.in)
		if ok != tc.ok || lang != tc.lang || fm != tc.fm || body != tc.body {
			t.Errorf("splitFrontmatter(%q) = %q, %q, %q, %v", tc.in, lang, fm, body, ok)
		}
	}
}
