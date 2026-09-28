package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/skzv/ccmux/internal/usage"
)

// TestVersionLabel — make builds pass `git describe` output ("v0.6.1-12-
// g…") and the status bar prepended another "v": "vv0.6.1-…".
func TestVersionLabel(t *testing.T) {
	for in, want := range map[string]string{
		"v0.6.1-12-gabc1234": "v0.6.1-12-gabc1234",
		"0.6.1":              "v0.6.1",
		"dev":                "dev",
		"":                   "",
	} {
		if got := versionLabel(in); got != want {
			t.Errorf("versionLabel(%q) = %q, want %q", in, got, want)
		}
	}
	a := newHomeApp(t, 160, 40, 0)
	a.version = "v0.6.1-12-gabc1234"
	if out := ansi.Strip(a.renderStatusBar()); strings.Contains(out, "vv0.6.1") || !strings.Contains(out, "v0.6.1-12-gabc1234") {
		t.Errorf("status bar version chip: %q", out)
	}
}

// TestTourCopy_OpenEndedAndComplete — the tour called ccmux a manager
// of "Claude Code sessions", said projects need "a CLAUDE.md or .git"
// (plain folders show up now), and listed the screens as "(2-6)"
// without Network.
func TestTourCopy_OpenEndedAndComplete(t *testing.T) {
	var all strings.Builder
	for _, s := range defaultTourSteps() {
		all.WriteString(s.Title + "\n" + strings.Join(s.Body, "\n") + "\n" + strings.Join(s.Bullets, "\n") + "\n")
	}
	text := all.String()
	for _, stale := range []string{"Claude Code sessions", "managing long-lived Claude Code", "CLAUDE.md or .git", "(Claude/Codex/Antigravity)"} {
		if strings.Contains(text, stale) {
			t.Errorf("tour still says %q", stale)
		}
	}
	span := fmt.Sprintf("(%s-%s)", screenKey(ScreenProjects), screenKey(ScreenNetwork))
	if !strings.Contains(text, span) || !strings.Contains(text, "Network") {
		t.Errorf("tour's screen overview should cover %s including Network:\n%s", span, text)
	}
}

// TestHelp_SessionsListsPreview — `p` toggles the Sessions preview but
// the help overlay didn't mention it.
func TestHelp_SessionsListsPreview(t *testing.T) {
	for _, it := range helpForScreen(ScreenSessions, DefaultKeymap()) {
		if it.Key == "p" {
			return
		}
	}
	t.Error("Sessions help has no entry for p (preview)")
}

// TestSettings_NoStalePromise — Settings promised a "Theme picker UI
// coming in v0.2" long after v0.2.
func TestSettings_NoStalePromise(t *testing.T) {
	a := newHomeApp(t, 160, 40, 0)
	for _, f := range a.settings.fields() {
		if strings.Contains(f.hint, "coming in v0.2") {
			t.Errorf("%s hint: %q", f.label, f.hint)
		}
	}
}

// TestPluralLabel_OnePrompt — "1 prompts".
func TestPluralLabel_OnePrompt(t *testing.T) {
	d := newDashboard(newHomeApp(t, 120, 40, 0).styles, DefaultKeymap())
	out := ansi.Strip(strings.Join(d.renderOtherAgentSection(usage.AgentSummary{HasData: true, Prompts: 1}), "\n"))
	if strings.Contains(out, "1 prompts") || !strings.Contains(out, "1 prompt") {
		t.Errorf("one prompt rendered as %q", out)
	}
	out = ansi.Strip(strings.Join(d.renderOtherAgentSection(usage.AgentSummary{HasData: true, Prompts: 2}), "\n"))
	if !strings.Contains(out, "2 prompts") {
		t.Errorf("two prompts rendered as %q", out)
	}
}

// TestGermanLabels_NoStrayHyphensOrWrongVerbs — the German catalog had
// "✓ -Daemon", "-Bildschirme", "-Stufe", "-Modus", "-Schalter",
// "-Version" and past participles for key hints ("enter geöffnet",
// "q beendet").
func TestGermanLabels_NoStrayHyphensOrWrongVerbs(t *testing.T) {
	withLang(t, "de")
	for _, key := range []string{"daemon", "screens", "tier", "mode", "switch", "version", "quit", "open"} {
		if got := tr(key); strings.HasPrefix(got, "-") {
			t.Errorf("de %q = %q (stray hyphen)", key, got)
		}
	}
	if got := tr("quit"); got != "beenden" {
		t.Errorf(`de "quit" = %q, want the infinitive "beenden"`, got)
	}
	if got := tr("open"); got != "öffnen" {
		t.Errorf(`de "open" = %q, want the infinitive "öffnen"`, got)
	}
}

// TestModelPicker_RowsStayOnOneLine — at 80 columns the model picker's
// "%-40s desc" rows were wider than the modal and wrapped mid-line into
// the next option.
func TestModelPicker_RowsStayOnOneLine(t *testing.T) {
	m := newClaude(newHomeApp(t, 80, 30, 0).styles, DefaultKeymap())
	m = m.openModelPicker()
	out := m.View(80, 30)
	assertNoOverflow(t, out, 80)
	plain := strings.Split(ansi.Strip(out), "\n")
	choices := m.unifiedModelChoices()
	rowOf := func(label string) int {
		if len(label) > 20 {
			label = label[:20]
		}
		for i, line := range plain {
			if strings.Contains(line, label) {
				return i
			}
		}
		t.Fatalf("option %q not shown:\n%s", label, strings.Join(plain, "\n"))
		return -1
	}
	// n options on exactly n consecutive rows: nothing wrapped.
	first, last := rowOf(choices[0].Label), rowOf(choices[len(choices)-1].Label)
	if got := last - first + 1; got != len(choices) {
		t.Errorf("%d options take %d rows — rows wrap:\n%s", len(choices), got, strings.Join(plain, "\n"))
	}
	if h := strings.Count(out, "\n") + 1; h > 30 {
		t.Errorf("picker is %d rows tall on a 30-row terminal", h)
	}
}
