package tui

import (
	"fmt"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/i18n"
	"github.com/skzv/ccmux/internal/tui/components"
)

// allFooterHints renders every screen's footer (the help bar) in the
// current language, including the per-agent Agents footers and both
// Conversations headless states. Keyed by a stable variant name.
func allFooterHints(t *testing.T) map[string][]components.KeyHint {
	t.Helper()
	a := newAppForTest(t)
	a.width = 400
	a.network = newNetwork(a.styles, a.keys)
	a.settings = newSettings(a.styles, a.keys, a.cfg, "test")
	out := map[string][]components.KeyHint{}
	for _, s := range []Screen{ScreenSessions, ScreenProjects, ScreenConversations, ScreenNotes, ScreenSettings, ScreenNetwork, Screen(-1)} {
		a.screen = s
		out[fmt.Sprintf("screen %d", int(s))] = a.helpBarProps().Hints
	}
	a.conversationsM.showHeadless = true
	out["conversations headless shown"] = a.conversationsM.HelpBarProps(a.width).Hints
	for _, ag := range agent.All() {
		a.agentsM.active = ag.ID()
		out["agents "+string(ag.ID())] = a.agentsM.HelpBarProps(a.width).Hints
	}
	return out
}

// TestFooterLabels_DistinctInEveryLocale — German translated both
// "kill" and "quit" as "beenden", so the Sessions footer read
// "q beenden · … · x beenden": two keys, one word, and no way to tell
// which one closes ccmux. Across every footer, two different English
// action labels must never share a translation.
func TestFooterLabels_DistinctInEveryLocale(t *testing.T) {
	withLang(t, "en")
	english := allFooterHints(t)
	for _, code := range i18n.Codes() {
		t.Run(code, func(t *testing.T) {
			i18n.SetLanguage(code)
			defer i18n.SetLanguage("en")
			owner := map[string]string{} // translated label → English label
			for variant, hints := range allFooterHints(t) {
				for i, h := range hints {
					en := english[variant][i].Label
					if prev, ok := owner[h.Label]; ok && prev != en {
						t.Errorf("%q and %q are both %q", prev, en, h.Label)
					}
					owner[h.Label] = en
				}
			}
		})
	}
}
