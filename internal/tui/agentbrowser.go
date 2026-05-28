package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/tui/components"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// agentBrowserItem is one row in the Browser's left list. Label /
// Trailing render in the list; Preview is the text shown in the
// right pane when this row is selected. When Markdown is true the
// preview is run through Glamour before being placed in the viewport,
// matching how the Notes screen renders .md files.
type agentBrowserItem struct {
	Label    string
	Trailing string
	Preview  string
	Markdown bool
}

// agentBrowserSection groups items under a heading inside the
// Browser's left list. Sections render in slice order with their
// titles as muted subheaders; items are flattened into one cursor-
// navigable list across sections. Color, when non-empty, is the
// foreground used for the leading "•" dot in front of the section
// title and each item row — a per-type visual key (hooks vs MCP vs
// commands vs skills) so categories pop in the list pane.
type agentBrowserSection struct {
	Title string
	Color lipgloss.Color
	Items []agentBrowserItem
}

// agentBrowserFocus tracks which pane handles j/k. When focused on
// the list, navigation moves the row cursor (and the right pane re-
// renders the new selection's preview); when focused on the preview,
// navigation scrolls the viewport. Tab and ←/→ toggle. Mirrors the
// Notes screen's two-pane focus convention so muscle memory carries.
type agentBrowserFocus int

const (
	agentBrowserFocusList agentBrowserFocus = iota
	agentBrowserFocusPreview
)

// agentBrowser is the inline list+preview component every Agents
// sub-tab embeds permanently. The host (claudeModel, codexConfigModel,
// …) calls SetSections each reload and View on every render. The
// browser does NOT render a centered overlay; it occupies the
// rectangle the host gives it.
type agentBrowser struct {
	st styles.Styles

	title    string
	sections []agentBrowserSection
	// flat is sections expanded into one linear cursor-addressable
	// slice with section-boundary markers (item==nil means section
	// title).
	flat   []agentBrowserRow
	cursor int

	focus    agentBrowserFocus
	preview  viewport.Model
	rendered string
}

type agentBrowserRow struct {
	section string // non-empty for section header rows
	color   lipgloss.Color
	item    *agentBrowserItem
}

func newAgentBrowser(st styles.Styles) agentBrowser {
	vp := viewport.New(80, 20)
	return agentBrowser{st: st, preview: vp, cursor: -1}
}

// SetSections refreshes the browser's sections from the host. The
// cursor is preserved when the previously-selected item is still
// present; otherwise it lands on the first selectable item.
func (b *agentBrowser) SetSections(title string, sections []agentBrowserSection) {
	b.title = title
	prevLabel := ""
	if b.cursor >= 0 && b.cursor < len(b.flat) {
		if row := b.flat[b.cursor]; row.item != nil {
			prevLabel = row.item.Label
		}
	}
	b.sections = sections
	b.flat = flattenBrowser(sections)
	if prevLabel != "" {
		for i, r := range b.flat {
			if r.item != nil && r.item.Label == prevLabel {
				b.cursor = i
				b.updatePreview()
				return
			}
		}
	}
	b.cursor = firstItemIndex(b.flat)
	b.updatePreview()
}

// flattenBrowser walks the sections in order and produces one slice
// of section-header rows + item rows so cursor arithmetic is trivial.
func flattenBrowser(sections []agentBrowserSection) []agentBrowserRow {
	out := []agentBrowserRow{}
	for _, s := range sections {
		out = append(out, agentBrowserRow{section: s.Title, color: s.Color})
		for i := range s.Items {
			out = append(out, agentBrowserRow{color: s.Color, item: &s.Items[i]})
		}
	}
	return out
}

// firstItemIndex returns the index of the first item row in flat, or
// -1 when there are no items.
func firstItemIndex(flat []agentBrowserRow) int {
	for i, r := range flat {
		if r.item != nil {
			return i
		}
	}
	return -1
}

// Update handles browser-level keys and mouse-wheel events. Returns
// the (possibly updated) browser and a tea.Cmd. When the browser
// handles an event it returns true so the host knows to swallow it.
// Letter keys the browser doesn't bind (e.g. m/e/a/y/c/j picker
// shortcuts on Claude) fall through so the host's existing handlers
// still respond.
func (b agentBrowser) Update(msg tea.Msg) (agentBrowser, tea.Cmd, bool) {
	// Mouse wheel scrolls the preview pane regardless of focus —
	// scrolling is a hands-on intent and the user can plausibly be
	// pointing at the right pane without first having tabbed into
	// it, so we don't gate on focus.
	if mm, ok := msg.(tea.MouseMsg); ok {
		switch mm.Type {
		case tea.MouseWheelUp:
			b.preview.LineUp(3)
			return b, nil, true
		case tea.MouseWheelDown:
			b.preview.LineDown(3)
			return b, nil, true
		}
		return b, nil, false
	}
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return b, nil, false
	}
	switch km.String() {
	case "left":
		b.focus = agentBrowserFocusList
		return b, nil, true
	case "right":
		b.focus = agentBrowserFocusPreview
		return b, nil, true
	case "down", "j":
		if b.focus == agentBrowserFocusPreview {
			b.preview.LineDown(1)
			return b, nil, true
		}
		b.moveCursor(+1)
		return b, nil, true
	case "up", "k":
		if b.focus == agentBrowserFocusPreview {
			b.preview.LineUp(1)
			return b, nil, true
		}
		b.moveCursor(-1)
		return b, nil, true
	case "pgdown", "ctrl+f":
		if b.focus == agentBrowserFocusPreview {
			b.preview.HalfViewDown()
			return b, nil, true
		}
		for i := 0; i < 5; i++ {
			b.moveCursor(+1)
		}
		return b, nil, true
	case "pgup", "ctrl+b":
		if b.focus == agentBrowserFocusPreview {
			b.preview.HalfViewUp()
			return b, nil, true
		}
		for i := 0; i < 5; i++ {
			b.moveCursor(-1)
		}
		return b, nil, true
	case "g":
		if b.focus == agentBrowserFocusPreview {
			b.preview.GotoTop()
			return b, nil, true
		}
		return b, nil, false
	case "G":
		if b.focus == agentBrowserFocusPreview {
			b.preview.GotoBottom()
			return b, nil, true
		}
		return b, nil, false
	case "enter":
		// Enter shifts focus into the preview so j/k start scrolling.
		// Esc / tab / ← move focus back.
		b.focus = agentBrowserFocusPreview
		return b, nil, true
	}
	return b, nil, false
}

// moveCursor steps the cursor by dir, skipping over section-header
// rows so item navigation feels uninterrupted.
func (b *agentBrowser) moveCursor(dir int) {
	if len(b.flat) == 0 {
		return
	}
	i := b.cursor
	for {
		i += dir
		if i < 0 || i >= len(b.flat) {
			return
		}
		if b.flat[i].item != nil {
			b.cursor = i
			b.updatePreview()
			return
		}
	}
}

// updatePreview re-renders the selected item's Preview into the
// right-pane viewport. Markdown items go through Glamour (matching
// the Notes preview); structured-text items (hooks, MCP servers)
// render verbatim. The viewport is rewound to the top on each new
// selection so the user always sees the head of the preview.
func (b *agentBrowser) updatePreview() {
	if b.cursor < 0 || b.cursor >= len(b.flat) {
		b.rendered = ""
		b.preview.SetContent("")
		return
	}
	row := b.flat[b.cursor]
	if row.item == nil {
		b.rendered = ""
		b.preview.SetContent("")
		return
	}
	content := row.item.Preview
	if content == "" {
		content = b.st.Muted.Render("(no preview)")
	} else if row.item.Markdown {
		width := b.preview.Width - 4
		if width < 20 {
			width = 20
		}
		r, err := glamour.NewTermRenderer(
			glamour.WithStandardStyle("dark"),
			glamour.WithWordWrap(width),
		)
		if err == nil {
			if out, rerr := r.Render(content); rerr == nil {
				content = out
			}
		}
	}
	b.rendered = content
	b.preview.SetContent(content)
	b.preview.GotoTop()
}

// View renders the browser inline at (width, height). The layout is
// a 40/60 split between two bordered panes; the focused pane wears
// the accent (PaneFocused) border, the unfocused pane wears the
// muted (Pane) border, so the user can see at a glance which side
// j/k drive.
func (b agentBrowser) View(width, height int) string {
	st := b.st
	if width < 60 {
		width = 60
	}
	if height < 10 {
		height = 10
	}

	hintH := 1
	bodyH := height - hintH - 1 // -1 for blank line above hint
	if bodyH < 6 {
		bodyH = 6
	}

	listW := width * 4 / 10
	if listW < 24 {
		listW = 24
	}
	previewW := width - listW

	// Frame each pane. st.Pane carries Border() (1 cell each side) +
	// Padding(0, 1) (1 cell horizontal each side); so the outer pane
	// width is content + 2 padding + 2 border = content + 4. When we
	// hand the pane a .Width(N) the call sets inner-frame width
	// (content + padding), so the actual content area is N - 2.
	const paneChromeH = 4                            // border + padding horizontally
	const paneInnerPad = 2                           // padding horizontally (subtracted from .Width)
	listFrameW := listW - paneChromeH + paneInnerPad // content + padding to pass to .Width
	if listFrameW < 6 {
		listFrameW = 6
	}
	listContentW := listFrameW - paneInnerPad
	if listContentW < 4 {
		listContentW = 4
	}
	listFrameH := bodyH - 2
	if listFrameH < 3 {
		listFrameH = 3
	}
	previewFrameW := previewW - paneChromeH + paneInnerPad
	if previewFrameW < 6 {
		previewFrameW = 6
	}
	previewContentW := previewFrameW - paneInnerPad
	if previewContentW < 4 {
		previewContentW = 4
	}
	previewFrameH := bodyH - 2
	if previewFrameH < 3 {
		previewFrameH = 3
	}

	b.preview.Width = previewContentW
	b.preview.Height = previewFrameH - 2

	listContent := b.renderList(listContentW, listFrameH-2)
	previewContent := b.preview.View()

	listStyle, previewStyle := st.Pane, st.Pane
	if b.focus == agentBrowserFocusList {
		listStyle = st.PaneFocused
	} else {
		previewStyle = st.PaneFocused
	}
	listPane := listStyle.Width(listFrameW).Height(listFrameH).Render(listContent)
	previewPane := previewStyle.Width(previewFrameW).Height(previewFrameH).Render(previewContent)

	body := lipgloss.JoinHorizontal(lipgloss.Top, listPane, previewPane)

	hint := b.renderHint()
	return lipgloss.JoinVertical(lipgloss.Left, body, "", hint)
}

// renderHint produces the muted hint line under the panes. The hint
// shifts based on focus so the keystrokes that actually do something
// are always present.
func (b agentBrowser) renderHint() string {
	st := b.st
	if b.focus == agentBrowserFocusPreview {
		return st.Muted.Render("↑↓ scroll · g/G top/bottom · ← focus list")
	}
	return st.Muted.Render("↑↓ navigate · enter/→ focus preview")
}

// renderList renders the left list pane (sections + items). Items
// use components.RenderListRow for selection treatment so the visual
// matches the rest of the TUI. The selection bar only shows on the
// row when the list pane has focus — when the preview is focused,
// the cursor row keeps a subtle marker but loses the accent bar so
// the focused pane is unambiguous.
func (b agentBrowser) renderList(width, height int) string {
	st := b.st
	if len(b.flat) == 0 {
		return st.Muted.Render("(no items)")
	}
	// Layout columns:
	//   "▌ " or "  " (selection bar, 2 cells)
	//   "  " (item indent, 2 cells; section headers skip this)
	//   "● " (colored type dot, 2 cells)
	//   <label> ... <trailing>
	// `width` is the inner pane width (between the column borders).
	// The selection bar consumes 2 cells; the rest is content.
	const selBarW = 2
	const itemIndentW = 2
	const dotColW = 2
	lines := []string{}
	for i, r := range b.flat {
		dot := strings.Repeat(" ", dotColW)
		if r.color != "" {
			dot = lipgloss.NewStyle().Foreground(r.color).Render("•") + " "
		}
		if r.section != "" {
			// Section headers anchor at column 0 (no item indent) so
			// the colored dot reads as a category marker, not a row.
			lines = append(lines, dot+st.Subtitle.Render(r.section))
			continue
		}
		it := r.item
		// Available cells for label + pad + trailing.
		contentW := width - selBarW - itemIndentW - dotColW
		if contentW < 1 {
			contentW = 1
		}
		content := it.Label
		if it.Trailing != "" {
			pad := contentW - lipgloss.Width(content) - lipgloss.Width(it.Trailing)
			if pad < 1 {
				pad = 1
			}
			content += strings.Repeat(" ", pad) + st.Muted.Render(it.Trailing)
		}
		row := strings.Repeat(" ", itemIndentW) + dot + content
		selected := i == b.cursor
		if selected && b.focus == agentBrowserFocusPreview {
			lines = append(lines, lipgloss.NewStyle().Foreground(st.P.FGMuted).Render(row))
		} else {
			lines = append(lines, components.RenderListRow(st, row, selected, width))
		}
	}
	if len(lines) > height {
		// Center the cursor in the viewport.
		start := b.cursor - height/2
		if start < 0 {
			start = 0
		}
		end := start + height
		if end > len(lines) {
			end = len(lines)
			start = end - height
			if start < 0 {
				start = 0
			}
		}
		lines = lines[start:end]
	}
	return strings.Join(lines, "\n")
}
