// Tour is the first-run interactive walkthrough: a short sequence of
// overlay steps, skippable, persisted via config so it doesn't
// re-fire. Re-openable any time with `T` from any screen.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/tui/styles"
)

// tourStep is one slide: paragraphs (and command rows), then bullets,
// which get a styled bullet glyph automatically.
type tourStep struct {
	Title   string
	Body    []tourPara
	Bullets []string
	KeyHint string // a one-line "press X to do Y" footer; empty allowed
}

// tourPara is one block of a slide's body. Text is a whole paragraph:
// the layout wraps it at the card's width, so the copy must not be
// broken into lines by hand (hard breaks wrapped a second time made
// ragged "…Mosh, and" / "Tailscale." lines). "" is a blank line.
//
// When Cmd is set the block is a command row instead: Cmd is printed
// verbatim, since it is what the user types, and Text describes it.
// Only Text is translated — the tour once told German users to run
// "ccmux-Host hinzufügen".
type tourPara struct {
	Text string
	Cmd  string
}

// para is a plain paragraph block.
func para(text string) tourPara { return tourPara{Text: text} }

// cmdRow is a command block: cmd verbatim, desc its (translated) gloss.
func cmdRow(cmd, desc string) tourPara { return tourPara{Cmd: cmd, Text: desc} }

// defaultTourSteps is the script the first-run tour runs through. Each
// slide is anchored to one of ccmux's core ideas. Keep each body
// terse — readers are in a TUI, not reading a book.
//
// The welcome slide's "N-step" count is stamped in from len(steps)
// after the slice is built, so the copy can never drift from the
// actual slide count again (it said "5-step" while the tour had 4).
func defaultTourSteps() []tourStep {
	steps := []tourStep{
		{
			Title: tr("Welcome to ccmux"),
			Body: []tourPara{
				para(tr("ccmux is a terminal UI for long-lived coding-agent sessions (Claude Code, Codex, Cursor, and more) on top of tmux, Mosh, and Tailscale.")),
				para(""),
				para(tr("This %d-step tour shows you the essentials. It runs once on first launch and re-opens any time with `T`.")),
			},
			KeyHint: tr("→ / space / enter: next  ·  esc: skip"),
		},
		{
			Title: tr("Sessions (") + screenKey(ScreenSessions) + ") — Sessions + Stats",
			Body: []tourPara{
				para(tr("Sessions is your command centre. Left pane: live sessions. Right pane: usage stats.")),
			},
			Bullets: []string{
				tr("↑↓/jk — navigate the session list · Enter — attach to the highlighted session"),
				tr("n — new session · x — kill · R — rename"),
				tr("Right pane: 5h quota, ccusage billing block, burn rate, token totals"),
				tr("Daemon + remote-host health also lives on the right"),
			},
			KeyHint: fmt.Sprintf(tr("Press %s / F1 anywhere to come back here"), screenKey(ScreenSessions)),
		},
		{
			Title: fmt.Sprintf(tr("The other screens (%s-%s)"), screenKey(ScreenProjects), screenKey(ScreenNetwork)),
			Body: []tourPara{
				para(tr("The remaining screens cover the full workflow loop:")),
			},
			Bullets: []string{
				fmt.Sprintf(tr("%s — Projects: every folder under your projects root (~/Projects by default)"), screenKey(ScreenProjects)),
				fmt.Sprintf(tr("%s — Conversations: every past agent conversation — resume any"), screenKey(ScreenConversations)),
				fmt.Sprintf(tr("%s — Notes: every markdown file in the project, rendered"), screenKey(ScreenNotes)),
				fmt.Sprintf(tr("%s — Agents: each agent's own config — model, hooks, commands, skills"), screenKey(ScreenAgents)),
				fmt.Sprintf(tr("%s — Settings: ccmux's own config (paths, daemon, theme)"), screenKey(ScreenSettings)),
				fmt.Sprintf(tr("%s — Network: your devices on the tailnet — ssh in, set up keys"), screenKey(ScreenNetwork)),
			},
			KeyHint: tr("Number keys jump between screens · `?` opens contextual help · q quits"),
		},
		{
			Title: tr("Mobile, remote, the daemon"),
			Body: []tourPara{
				para(tr("Two pieces you'll want eventually:")),
				para(""),
				cmdRow("ccmux moshi-setup", tr("iOS push notifications via Moshi")),
				cmdRow("ccmux host add …", tr("supervise sessions on a remote ccmuxd")),
				para(""),
				para(tr("And one piece that's already running in the background:")),
				para(""),
				cmdRow("ccmuxd", tr("polls tmux, classifies each session, rings the bell when an agent needs input, and keeps the machine awake while sessions are active")),
			},
			KeyHint: tr("Press enter to finish the tour, esc to skip — you can re-open with T"),
		},
	}
	// Stamp the derived step count into any body paragraph carrying a
	// %d placeholder (today just the welcome slide). Done by scan rather
	// than a hard-coded index so reflowing the copy can't silently point
	// the Sprintf at the wrong paragraph.
	for i, p := range steps[0].Body {
		if strings.Contains(p.Text, "%d") {
			steps[0].Body[i].Text = fmt.Sprintf(p.Text, len(steps))
		}
	}
	return steps
}

// tourModel manages the active tour. Zero value is "tour not active".
type tourModel struct {
	active bool
	step   int
	steps  []tourStep
	st     styles.Styles
	// scroll is the first body row shown when a slide is taller than
	// the terminal; reset on every slide change.
	scroll int
}

func newTour(st styles.Styles) tourModel {
	return tourModel{st: st, steps: defaultTourSteps()}
}

// Open begins the tour from step 0.
func (m *tourModel) Open() {
	// A language switch may have happened since construction or the last
	// tour. Rebuild translated copy before displaying the first slide.
	m.steps = defaultTourSteps()
	m.active = true
	m.step = 0
	m.scroll = 0
}

// Close hides the tour without advancing.
func (m *tourModel) Close() { m.active = false }

// Active reports whether the tour is being shown right now.
func (m tourModel) Active() bool { return m.active }

// Step returns the index of the slide currently visible.
func (m tourModel) Step() int { return m.step }

// Next advances to the next slide and returns true if a slide change
// happened; returns false on the final slide (so the caller can mark
// the tour complete and close it).
func (m *tourModel) Next() bool {
	if m.step >= len(m.steps)-1 {
		return false
	}
	m.step++
	m.scroll = 0
	return true
}

// Prev steps back one slide. No-op at step 0.
func (m *tourModel) Prev() {
	if m.step > 0 {
		m.step--
		m.scroll = 0
	}
}

// View renders the tour as a centered overlay inside `w` × `h` chars.
// Designed to drop in place of the regular frame when active.
func (m tourModel) View(w, h int) string {
	if !m.active || len(m.steps) == 0 {
		return ""
	}
	l := m.layout(w, h)
	body := l.body
	if len(body) > l.room {
		// Taller than the terminal: show a window of the slide and say
		// how to scroll it. Clipping it (the old behaviour) lost the
		// bottom of the slide on a phone.
		maxOff := len(body) - l.room
		off := maxInt(0, minInt(m.scroll, maxOff))
		body = append(append([]string{}, body[off:off+l.room]...),
			m.st.Muted.Render(fmt.Sprintf(tr("↑↓ scroll %d/%d"), off+1, maxOff+1)))
	}
	card := lipgloss.NewStyle().
		Padding(l.padY, l.padX).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.st.P.Mauve).
		Width(l.cardW).
		Render(strings.Join(append(body, l.footer...), "\n"))

	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, card)
}

// tourLayout is the current slide laid out for a w×h terminal: the
// scrollable part (title, body, bullets), the footer that always shows
// (progress dots, key hint), and how many body rows fit.
type tourLayout struct {
	cardW, padX, padY int
	body, footer      []string
	room              int
}

func (m tourModel) layout(w, h int) tourLayout {
	step := m.steps[minInt(m.step, len(m.steps)-1)]

	// Card width = clamp between 50 and 80 cols so the layout reads
	// well on phones (narrow) and big monitors (don't get a wall).
	cardW := w - 6
	if cardW > 80 {
		cardW = 80
	}
	if cardW < 50 {
		cardW = 50
	}
	// ...but never wider than the terminal: the 50-col floor made the
	// card (plus its 2-col border) overflow a phone-width screen, and
	// Bubble Tea hard-truncated the right edge off. Width excludes the
	// border, so the card fits in w-2.
	if cardW > w-2 {
		cardW = maxInt(1, w-2)
	}
	l := tourLayout{cardW: cardW, padX: m.st.Spacing.LG, padY: m.st.Spacing.SM}
	if w < 60 {
		l.padX = m.st.Spacing.SM // phone: the text needs the columns more
	}
	textW := maxInt(8, cardW-2*l.padX)
	wrap := func(s string) []string {
		return strings.Split(lipgloss.NewStyle().Width(textW).Render(s), "\n")
	}

	titleStyle := m.st.Title.Foreground(m.st.P.Mauve).Bold(true)
	l.body = append(wrap(titleStyle.Render(step.Title)), "")
	// Command rows share one column for their commands (the widest on
	// the slide), with each description wrapping under its own start.
	// On a card too narrow for that, the description goes under the
	// command instead.
	cmdW := 0
	for _, p := range step.Body {
		if p.Cmd != "" {
			cmdW = maxInt(cmdW, lipgloss.Width(p.Cmd))
		}
	}
	const cmdIndent = 2
	descX := cmdIndent + cmdW + 2
	stacked := textW-descX < 20
	for _, p := range step.Body {
		if p.Cmd == "" {
			l.body = append(l.body, wrap(p.Text)...)
			continue
		}
		cmd := strings.Repeat(" ", cmdIndent) + m.st.Key.Render(p.Cmd)
		if stacked {
			l.body = append(l.body, cmd)
			for _, line := range strings.Split(lipgloss.NewStyle().Width(maxInt(4, textW-2*cmdIndent)).Render(p.Text), "\n") {
				l.body = append(l.body, strings.Repeat(" ", 2*cmdIndent)+line)
			}
			continue
		}
		desc := strings.Split(lipgloss.NewStyle().Width(textW-descX).Render(p.Text), "\n")
		for i, line := range desc {
			if i == 0 {
				l.body = append(l.body, cmd+strings.Repeat(" ", descX-cmdIndent-lipgloss.Width(p.Cmd))+line)
			} else {
				l.body = append(l.body, strings.Repeat(" ", descX)+line)
			}
		}
	}
	if len(step.Bullets) > 0 && len(step.Body) > 0 {
		l.body = append(l.body, "")
	}
	// Bullets wrap under their own text (a hanging indent), not back
	// under the bullet glyph.
	const bulletIndent = 4 // "  • "
	for _, b := range step.Bullets {
		wrapped := strings.Split(lipgloss.NewStyle().Width(maxInt(4, textW-bulletIndent)).Render(b), "\n")
		for i, line := range wrapped {
			if i == 0 {
				l.body = append(l.body, "  "+m.st.Key.Render("•")+" "+line)
			} else {
				l.body = append(l.body, strings.Repeat(" ", bulletIndent)+line)
			}
		}
	}

	// Progress dots.
	dots := strings.Builder{}
	for i := range m.steps {
		if i == m.step {
			dots.WriteString(m.st.Key.Render("●"))
		} else {
			dots.WriteString(m.st.Muted.Render("○"))
		}
		if i < len(m.steps)-1 {
			dots.WriteString(" ")
		}
	}
	l.footer = []string{"", dots.String()}
	if step.KeyHint != "" {
		l.footer = append(l.footer, "")
		l.footer = append(l.footer, wrap(m.st.Muted.Render(step.KeyHint))...)
	}

	// Rows the body may use: the terminal less the border, the vertical
	// padding and the footer. Short terminals drop the vertical padding
	// first; the body scrolls when it still doesn't fit (one row then
	// goes to the scroll hint).
	fits := func() int { return h - 2 - 2*l.padY - len(l.footer) }
	if len(l.body) > fits() {
		l.padY = m.st.Spacing.XS
	}
	l.room = fits()
	if len(l.body) > l.room {
		l.room--
	}
	l.room = maxInt(1, l.room)
	return l
}

// ScrollBy moves the slide's scroll offset by delta rows, clamped to
// what a w×h terminal leaves hidden.
func (m *tourModel) ScrollBy(delta, w, h int) {
	if !m.active || len(m.steps) == 0 {
		return
	}
	l := m.layout(w, h)
	m.scroll = maxInt(0, minInt(m.scroll+delta, len(l.body)-l.room))
}
