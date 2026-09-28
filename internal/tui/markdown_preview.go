package tui

import (
	"strings"

	"github.com/skzv/ccmux/internal/termsafe"
)

// markdownForPreview prepares markdown from disk (a note, a slash
// command, a skill) for Glamour:
//
//   - terminal control sequences are removed whole (termsafe). Glamour
//     dropped only the ESC byte of `\x1b[31m`, leaving "[31mRED[0m" on
//     screen.
//   - a leading YAML (---) or TOML (+++) frontmatter block is shown as a
//     fenced code block. As plain markdown Glamour read it as a rule and
//     a setext heading: "──────" over "## title: Overview".
func markdownForPreview(src string) string {
	src = termsafe.String(src)
	lang, fm, body, ok := splitFrontmatter(src)
	if !ok {
		return src
	}
	return "```" + lang + "\n" + fm + "\n```\n\n" + body
}

// splitFrontmatter separates a frontmatter block delimited by "---"
// (YAML, which may also close with "...") or "+++" (TOML) lines at the
// very top of src. ok is false when src doesn't open with one or the
// block is never closed.
func splitFrontmatter(src string) (lang, fm, body string, ok bool) {
	src = strings.TrimPrefix(src, "\ufeff")
	lines := strings.Split(src, "\n")
	if len(lines) < 2 {
		return "", "", "", false
	}
	switch strings.TrimSpace(lines[0]) {
	case "---":
		lang = "yaml"
	case "+++":
		lang = "toml"
	default:
		return "", "", "", false
	}
	open := strings.TrimSpace(lines[0])
	for i := 1; i < len(lines); i++ {
		l := strings.TrimSpace(lines[i])
		if l == open || (lang == "yaml" && l == "...") {
			return lang, strings.Join(lines[1:i], "\n"), strings.Join(lines[i+1:], "\n"), true
		}
	}
	return "", "", "", false
}
