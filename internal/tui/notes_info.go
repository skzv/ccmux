package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// noteInfoOverlay is the `i`-key info modal. Closed by default; the
// app routes `i` (when no text field has focus) into a noteInfoOpenMsg
// to populate it from disk. Renders the selected note's metadata:
// absolute path, frontmatter (if any), H1 (if any), line/word counts,
// and modified time.
type noteInfoOverlay struct {
	open        bool
	absPath     string
	relPath     string
	h1          string
	frontmatter string
	lineCount   int
	wordCount   int
	modified    time.Time
	readErr     string
}

// buildNoteInfoOverlay reads `absPath` (capped at 1 MiB so an enormous
// markdown file can't hang the overlay) and pulls out the rendered
// fields. The returned overlay has open=true even when the read
// fails — the modal shows the error so the user knows what went
// wrong rather than silently no-op'ing.
func buildNoteInfoOverlay(absPath, projectRoot string) noteInfoOverlay {
	o := noteInfoOverlay{open: true, absPath: absPath}
	if projectRoot != "" {
		if rel, err := filepath.Rel(projectRoot, absPath); err == nil {
			o.relPath = filepath.ToSlash(rel)
		}
	}
	info, statErr := os.Stat(absPath)
	if statErr == nil {
		o.modified = info.ModTime()
	}
	const readCap = 1 << 20
	data, err := os.ReadFile(absPath)
	if err != nil {
		o.readErr = err.Error()
		return o
	}
	if len(data) > readCap {
		data = data[:readCap]
	}
	body := string(data)
	o.lineCount = strings.Count(body, "\n")
	if len(body) > 0 && !strings.HasSuffix(body, "\n") {
		o.lineCount++
	}
	o.wordCount = len(strings.Fields(body))
	o.frontmatter = extractFrontmatter(body)
	o.h1 = cachedH1ForOverlay(absPath, o.modified)
	return o
}

// cachedH1ForOverlay returns the leading H1 text for the file at
// `absPath`, or "" when none is present in the first 4 KiB. Mirrors
// notes.extractH1 so the overlay sees the same heading the row label
// does, including the YAML/TOML frontmatter-skip behaviour.
func cachedH1ForOverlay(absPath string, mod time.Time) string {
	f, err := os.Open(absPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, 4096)
	n, _ := f.Read(buf)
	src := string(buf[:n])
	lines := strings.Split(src, "\n")

	inFrontmatter := false
	frontDelim := ""
	firstLine := true
	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		if firstLine {
			firstLine = false
			if line == "---" {
				inFrontmatter = true
				frontDelim = "---"
				continue
			}
			if line == "+++" {
				inFrontmatter = true
				frontDelim = "+++"
				continue
			}
		}
		if inFrontmatter {
			if line == frontDelim {
				inFrontmatter = false
			}
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "# ") && !strings.HasPrefix(trimmed, "## ") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
		}
		// First non-empty, non-frontmatter line that isn't an H1 → stop looking.
		break
	}
	return ""
}

// extractFrontmatter returns the YAML/TOML frontmatter block (without
// the delimiters) when the file starts with one, otherwise "". The
// scan is bounded to the first 64 lines so a "doc that looks like
// frontmatter forever" can't blow the buffer.
func extractFrontmatter(body string) string {
	lines := strings.Split(body, "\n")
	if len(lines) < 2 {
		return ""
	}
	delim := ""
	switch lines[0] {
	case "---":
		delim = "---"
	case "+++":
		delim = "+++"
	default:
		return ""
	}
	const scanLines = 64
	end := scanLines
	if end > len(lines) {
		end = len(lines)
	}
	for i := 1; i < end; i++ {
		if lines[i] == delim {
			return strings.Join(lines[1:i], "\n")
		}
	}
	return ""
}

// renderNoteInfoOverlay draws the centered note-info modal.
func (m notesModel) renderNoteInfoOverlay(width, height int) string {
	st := m.st
	o := m.noteInfo
	lines := []string{
		st.Emphasis.Render(tr("Note info")),
		st.Subtitle.Render(tr("Press i or esc to close.")),
		"",
	}
	if o.readErr != "" {
		lines = append(lines, st.StatusError.Render("⚠ "+o.readErr))
	}
	row := func(k, v string) string {
		return st.Muted.Render(k+": ") + v
	}
	if o.relPath != "" {
		lines = append(lines, row("relative", o.relPath))
	}
	lines = append(lines, row("path", o.absPath))
	if o.h1 != "" {
		lines = append(lines, row("title (H1)", st.Emphasis.Render(o.h1)))
	}
	lines = append(lines,
		row("lines", fmt.Sprintf("%d", o.lineCount)),
		row("words", fmt.Sprintf("%d", o.wordCount)),
	)
	if !o.modified.IsZero() {
		lines = append(lines, row("modified", o.modified.Format("2006-01-02 15:04:05")))
	}
	if o.frontmatter != "" {
		lines = append(lines, "", st.Subtitle.Render("frontmatter"))
		for _, l := range strings.Split(o.frontmatter, "\n") {
			lines = append(lines, "  "+l)
		}
	}
	modalW := max(20, minInt(96, width-4))
	body := strings.Join(lines, "\n")
	if height > 6 {
		body = clampLines(body, height-6)
	}
	modal := st.PaneFocused.Width(modalW).Render(body)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
}
