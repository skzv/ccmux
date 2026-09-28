package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
)

// helpKeyTokens splits a help/footer key label ("p / space", "→/←",
// "↑↓ / j k (list focused)") into the individual keys it names.
func helpKeyTokens(label string) []string {
	if i := strings.Index(label, "("); i >= 0 {
		label = label[:i]
	}
	var out []string
	for _, f := range strings.FieldsFunc(label, func(r rune) bool { return r == '/' || r == ' ' || r == '+' }) {
		switch f {
		case "↑↓", "←→", "→←":
			for _, r := range f {
				out = append(out, string(r))
			}
		default:
			out = append(out, f)
		}
	}
	return out
}

// TestHelp_CoversEveryFooterKey — the help overlay drifted from the
// bindings: Notes said "tab / h / l / ←→ toggle focus" although ←/→
// expand and collapse folders, and Conversations' help left out p, x
// and tab, which its own footer advertises. Every key a screen's footer
// shows must be explained in that screen's help (or the global part).
func TestHelp_CoversEveryFooterKey(t *testing.T) {
	withLang(t, "en")
	a := newAppForTest(t)
	a.width = 400
	a.network = newNetwork(a.styles, a.keys)
	a.settings = newSettings(a.styles, a.keys, a.cfg, "test")
	check := func(t *testing.T, screen Screen, footerKeys []string) {
		t.Helper()
		have := map[string]bool{}
		for _, it := range append(helpForScreen(screen, a.keys), globalHelp(a.keys)...) {
			for _, k := range helpKeyTokens(it.Key) {
				have[k] = true
			}
		}
		// The global help spells the screen keys as a range.
		have["1-7"] = true
		for _, fk := range footerKeys {
			for _, k := range helpKeyTokens(fk) {
				if !have[k] {
					t.Errorf("footer key %q (from %q) is missing from the help", k, fk)
				}
			}
		}
	}
	for _, s := range []Screen{ScreenSessions, ScreenProjects, ScreenConversations, ScreenNotes, ScreenSettings, ScreenNetwork} {
		a.screen = s
		var keys []string
		for _, h := range a.helpBarProps().Hints {
			keys = append(keys, h.Key)
		}
		t.Run(fmt.Sprint(int(s)), func(t *testing.T) { check(t, s, keys) })
	}
	for _, ag := range agent.All() {
		a.agentsM.active = ag.ID()
		var keys []string
		for _, h := range a.agentsM.HelpBarProps(a.width).Hints {
			keys = append(keys, h.Key)
		}
		t.Run("agents/"+string(ag.ID()), func(t *testing.T) { check(t, ScreenAgents, keys) })
	}
}

// TestHelp_NotesArrowsExpandFolders — ←/→ (and h/l) open and close
// folders in the Notes tree; the help said they toggled focus.
func TestHelp_NotesArrowsExpandFolders(t *testing.T) {
	withLang(t, "en")
	for _, it := range helpForScreen(ScreenNotes, DefaultKeymap()) {
		keys := helpKeyTokens(it.Key)
		for _, k := range keys {
			if (k == "←" || k == "→" || k == "h" || k == "l") && strings.Contains(it.Desc, "toggle focus") {
				t.Errorf("help says %q toggles focus: %q — %q", k, it.Key, it.Desc)
			}
		}
	}
}
