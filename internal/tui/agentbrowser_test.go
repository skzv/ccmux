package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/tui/styles"
)

// longMarkdownSections builds one browser section whose single item
// carries a markdown preview tall enough to overflow any small pane.
func longMarkdownSections(lines int) []agentBrowserSection {
	var b strings.Builder
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&b, "line %03d of the preview body\n\n", i)
	}
	b.WriteString("LAST-PREVIEW-LINE\n")
	return []agentBrowserSection{{
		Title: "Skills",
		Items: []agentBrowserItem{{Label: "big.md", Preview: b.String(), Markdown: true}},
	}}
}

// TestAgentBrowser_SetSizePersistsViewportForScrolling — the shipped
// bug: agentBrowser.View has a value receiver, so the pane size it
// computed each frame died with the frame copy and the persistent
// viewport kept its constructed 80×20 size. Scrolling then clamped
// YOffset against the stale Height: on panes shorter than ~20 content
// rows the last lines could never scroll into view. SetSize (called
// from the host's Update-side resize chain) must persist the real
// geometry so GotoBottom actually reaches the end.
func TestAgentBrowser_SetSizePersistsViewportForScrolling(t *testing.T) {
	b := newAgentBrowser(styles.Default())
	b.SetSections("test", longMarkdownSections(60))

	const paneW, paneH = 80, 14 // pane much shorter than the constructed 20
	b.SetSize(paneW, paneH)

	g := agentBrowserGeometry(paneW, paneH)
	if b.preview.Height != g.previewViewH {
		t.Fatalf("preview.Height = %d after SetSize, want %d (geometry)", b.preview.Height, g.previewViewH)
	}
	if b.preview.Width != g.previewContentW {
		t.Fatalf("preview.Width = %d after SetSize, want %d (geometry)", b.preview.Width, g.previewContentW)
	}
	if g.previewViewH >= 20 {
		t.Fatalf("test setup broken: pane viewport %d not shorter than the stale 20", g.previewViewH)
	}

	// Focus the preview and jump to the bottom — the Update-side
	// scroll path that used to clamp against the stale Height.
	b, _, _ = b.Update(tea.KeyMsg{Type: tea.KeyEnter})
	b, _, _ = b.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})

	total := b.preview.TotalLineCount()
	if total <= b.preview.Height {
		t.Fatalf("test setup broken: content (%d lines) fits the viewport (%d)", total, b.preview.Height)
	}
	if !b.preview.AtBottom() {
		t.Errorf("GotoBottom did not reach the bottom: YOffset=%d total=%d height=%d",
			b.preview.YOffset, total, b.preview.Height)
	}
	// The last content line must be inside the visible window — this
	// is exactly what failed with the stale Height (YOffset stopped
	// total-20 lines up, leaving the tail unreachable).
	if b.preview.YOffset+b.preview.Height < total {
		t.Errorf("last line unreachable: YOffset(%d) + Height(%d) < total(%d)",
			b.preview.YOffset, b.preview.Height, total)
	}
}

// TestAgentBrowser_SetSizeRewrapsGlamourAtRealWidth — updatePreview
// wraps markdown at preview.Width-4; with the stale constructed width
// (80) a narrow pane got content wrapped for a pane 80 wide. After
// SetSize the rendered preview must fit the real content width.
func TestAgentBrowser_SetSizeRewrapsGlamourAtRealWidth(t *testing.T) {
	b := newAgentBrowser(styles.Default())
	b.SetSections("test", []agentBrowserSection{{
		Title: "Skills",
		Items: []agentBrowserItem{{
			Label:    "wide.md",
			Markdown: true,
			Preview: "This is one very long markdown paragraph that will need wrapping " +
				"at whatever width the renderer is handed because it just keeps going " +
				"and going well past any narrow pane's content width.",
		}},
	}})

	const paneW, paneH = 64, 20 // narrow: preview content width well under 80
	b.SetSize(paneW, paneH)
	g := agentBrowserGeometry(paneW, paneH)

	maxW := 0
	for _, line := range strings.Split(b.rendered, "\n") {
		if w := lipgloss.Width(line); w > maxW {
			maxW = w
		}
	}
	if maxW > g.previewContentW {
		t.Errorf("rendered preview wraps at %d cells, pane content width is %d — Glamour used a stale width",
			maxW, g.previewContentW)
	}
}

// TestApp_WindowSizeReachesAgentsBrowsers — the wiring half: a
// tea.WindowSizeMsg through the real App.Update must land on every
// Agents sub-tab's embedded browser. Before the fix nothing called
// into the browsers on resize, so the viewports sat at the
// constructed 80×20 forever.
func TestApp_WindowSizeReachesAgentsBrowsers(t *testing.T) {
	a := newAppForTest(t)
	m, _ := a.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	a2 := m.(App)

	// Mirror the production math with the production helpers so the
	// expectation can't drift from the layout chain.
	header := a2.agentsM.renderSubtabs(isNarrow(160))
	innerW := 160 - 4
	innerH := a2.screenBodyHeight() - 2 - lipgloss.Height(header) - 1
	if innerH < 6 {
		innerH = 6
	}
	wantClaude := agentBrowserGeometry(innerW, a2.agentsM.claude.browserHeight(innerH))
	got := a2.agentsM.claude.browser.preview
	if got.Height == 20 && wantClaude.previewViewH != 20 {
		t.Fatal("Claude browser viewport still at the constructed 80×20 after WindowSizeMsg — resize never reached it")
	}
	if got.Height != wantClaude.previewViewH || got.Width != wantClaude.previewContentW {
		t.Errorf("claude browser viewport = %dx%d, want %dx%d",
			got.Width, got.Height, wantClaude.previewContentW, wantClaude.previewViewH)
	}

	// Every sub-tab gets the size, not just the active one.
	for name, vp := range map[string]struct{ w, h int }{
		"codex":       {a2.agentsM.codex.browser.preview.Width, a2.agentsM.codex.browser.preview.Height},
		"antigravity": {a2.agentsM.antigravity.browser.preview.Width, a2.agentsM.antigravity.browser.preview.Height},
		"cursor":      {a2.agentsM.cursor.browser.preview.Width, a2.agentsM.cursor.browser.preview.Height},
	} {
		if vp.w == 80 && vp.h == 20 {
			t.Errorf("%s browser viewport still at the constructed 80×20 after WindowSizeMsg", name)
		}
	}
}
