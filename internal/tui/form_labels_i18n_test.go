package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/skzv/ccmux/internal/i18n"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// assertRowsOnConsecutiveLines checks that each form row (found by its
// translated label, or the start of it when a capped label column
// shortened it) is one line: the rows' labels sit on consecutive lines
// of the rendered form. A row that wraps pushes the next label down.
func assertRowsOnConsecutiveLines(t *testing.T, form string, labels ...string) {
	t.Helper()
	lines := strings.Split(ansi.Strip(form), "\n")
	prev := -1
	for _, label := range labels {
		prefix := label
		if r := []rune(label); len(r) > 3 {
			prefix = string(r[:3])
		}
		at := -1
		for i := prev + 1; i < len(lines); i++ {
			if strings.HasPrefix(strings.TrimLeft(lines[i], "│ "), prefix) {
				at = i
				break
			}
		}
		if at < 0 {
			t.Fatalf("row %q not found in form:\n%s", label, ansi.Strip(form))
		}
		if prev >= 0 && at != prev+1 {
			t.Fatalf("the row before %q takes %d lines:\n%s", label, at-prev, ansi.Strip(form))
		}
		prev = at
	}
}

// TestNewSessionForm_TranslatedLabelsKeepRowsOnOneLine — the label
// column was a fixed 12 cells, sized for English. "Arbeitsverzeichnis"
// or "directorio de trabajo" ran into the field and the working-dir row
// wrapped ("~ (daemon's $HOM…" on a line of its own) at 40 columns.
// The placeholder itself was never translated.
func TestNewSessionForm_TranslatedLabelsKeepRowsOnOneLine(t *testing.T) {
	withLang(t, "en")
	for _, code := range i18n.Codes() {
		for _, width := range []int{40, 60, 80, 120} {
			t.Run(fmt.Sprintf("%s/%d", code, width), func(t *testing.T) {
				i18n.SetLanguage(code)
				defer i18n.SetLanguage("en")
				f := newNewSessionForm(styles.Default(), []hostStatus{{Name: "local", Local: true, OK: true}}, "", "")
				out := f.View(minInt(80, width-4))
				assertNoOverflow(t, out, width-4)
				assertRowsOnConsecutiveLines(t, out, tr("name"), tr("working dir"), tr("device"), tr("agent"))
				if width >= 120 && !strings.Contains(ansi.Strip(out), tr("~ (daemon's $HOME if blank)")) {
					t.Errorf("working-dir placeholder not shown translated:\n%s", ansi.Strip(out))
				}
			})
		}
	}
}

// TestNewSessionForm_PlaceholderTranslated — the working-dir
// placeholder was English in every language.
func TestNewSessionForm_PlaceholderTranslated(t *testing.T) {
	withLang(t, "de")
	for _, dir := range []string{"", "~/work"} {
		got := defaultDirPlaceholder(dir)
		if strings.Contains(got, "if blank") || strings.Contains(got, "edit to override") {
			t.Errorf("placeholder for %q is still English: %q", dir, got)
		}
		if dir != "" && !strings.HasPrefix(got, dir) {
			t.Errorf("placeholder %q lost the configured dir %q", got, dir)
		}
	}
}

// TestNewNoteForm_TranslatedLabelsKeepRowsOnOneLine — the new-note
// inputs were a fixed 60 cells after a 10-cell label, so the German
// form wrapped its filename value; the rows now fit the pane.
func TestNewNoteForm_TranslatedLabelsKeepRowsOnOneLine(t *testing.T) {
	withLang(t, "en")
	now := time.Date(2026, 9, 27, 14, 5, 0, 0, time.UTC)
	for _, code := range i18n.Codes() {
		for _, width := range []int{40, 60, 80, 120} {
			t.Run(fmt.Sprintf("%s/%d", code, width), func(t *testing.T) {
				i18n.SetLanguage(code)
				defer i18n.SetLanguage("en")
				f := newNewNoteForm(styles.Default(), now)
				out := f.View(minInt(80, width-4))
				assertNoOverflow(t, out, width-4)
				assertRowsOnConsecutiveLines(t, out, tr("filename"), tr("title"))
			})
		}
	}
}

// TestNewProjectForm_TranslatedLabelsKeepRowsOnOneLine — same fixed
// label column in the new-project form.
func TestNewProjectForm_TranslatedLabelsKeepRowsOnOneLine(t *testing.T) {
	withLang(t, "en")
	for _, code := range i18n.Codes() {
		for _, width := range []int{40, 80} {
			t.Run(fmt.Sprintf("%s/%d", code, width), func(t *testing.T) {
				i18n.SetLanguage(code)
				defer i18n.SetLanguage("en")
				f := newNewProjectForm(styles.Default(), []hostStatus{{Name: "local", Local: true, OK: true}}, "")
				out := f.View(minInt(80, width-4))
				assertNoOverflow(t, out, width-4)
				assertRowsOnConsecutiveLines(t, out, tr("name"), tr("device"), tr("agent"))
			})
		}
	}
}
