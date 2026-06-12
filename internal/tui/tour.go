// Tour is the first-run interactive walkthrough. Five overlay steps,
// skippable, persisted via config so it doesn't re-fire. Re-openable
// any time with `T` from any screen.
package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/tui/styles"
)

// tourStep is one slide. Body lines render as-is; bullets get a styled
// bullet glyph automatically.
type tourStep struct {
	Title   string
	Body    []string
	Bullets []string
	KeyHint string // a one-line "press X to do Y" footer; empty allowed
}

// defaultTourSteps is the script the first-run tour runs through. Five
// slides, each anchored to one of ccmux's core ideas. Keep each body
// terse — readers are in a TUI, not reading a book.
func defaultTourSteps() []tourStep {
	return []tourStep{
		{
			Title: "Welcome to ccmux",
			Body: []string{
				"ccmux is a terminal UI for managing long-lived Claude Code",
				"sessions on top of tmux, Mosh, and Tailscale.",
				"",
				"This 5-step tour shows you the essentials. It runs once on",
				"first launch and re-opens any time with `T`.",
			},
			KeyHint: "→ / space / enter: next  ·  esc: skip",
		},
		{
			Title: "Sessions (" + screenKey(ScreenSessions) + ") — Sessions + Stats",
			Body: []string{
				"Sessions is your command centre. Left pane: live sessions. Right pane: usage stats.",
			},
			Bullets: []string{
				"↑↓/jk — navigate the session list · Enter — attach to the highlighted session",
				"n — new session · x — kill · R — rename",
				"Right pane: 5h quota, ccusage billing block, burn rate, token totals",
				"Daemon + remote-host health also lives on the right",
			},
			KeyHint: "Press " + screenKey(ScreenSessions) + " / F1 anywhere to come back here",
		},
		{
			Title: "Projects, Conversations, Notes, Agents (" + screenKey(ScreenProjects) + "-" + screenKey(ScreenSettings) + ")",
			Body: []string{
				"The remaining screens cover the full workflow loop:",
			},
			Bullets: []string{
				screenKey(ScreenProjects) + " — Projects: every dir under ~/Projects with a CLAUDE.md or .git",
				screenKey(ScreenConversations) + " — Conversations: every past agent dialogue (Claude/Codex/Antigravity) — resume any",
				screenKey(ScreenNotes) + " — Notes: per-project docs/ vault — Specs, ADRs, Agent Logs",
				screenKey(ScreenAgents) + " — Agents: edit ~/.claude / ~/.codex / ~/.gemini/antigravity-cli config",
				screenKey(ScreenSettings) + " — Settings: ccmux's own config (paths, daemon, theme)",
			},
			KeyHint: "Number keys jump between screens · `?` opens contextual help · q quits",
		},
		{
			Title: "Mobile, remote, the daemon",
			Body: []string{
				"Two pieces you'll want eventually:",
				"",
				"  ccmux moshi-setup   — iOS push notifications via Moshi",
				"  ccmux host add …    — supervise sessions on a remote ccmuxd",
				"",
				"And one piece that's already running in the background:",
				"",
				"  ccmuxd  — polls tmux, classifies state, triggers the bell on",
				"             needs_input, holds caffeinate while sessions are active",
			},
			KeyHint: "Press enter to finish the tour, esc to skip — you can re-open with T",
		},
	}
}

// tourModel manages the active tour. Zero value is "tour not active".
type tourModel struct {
	active bool
	step   int
	steps  []tourStep
	st     styles.Styles
}

func newTour(st styles.Styles) tourModel {
	return tourModel{st: st, steps: defaultTourSteps()}
}

// Open begins the tour from step 0.
func (m *tourModel) Open() {
	m.active = true
	m.step = 0
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
	return true
}

// Prev steps back one slide. No-op at step 0.
func (m *tourModel) Prev() {
	if m.step > 0 {
		m.step--
	}
}

// View renders the tour as a centered overlay inside `w` × `h` chars.
// Designed to drop in place of the regular frame when active.
func (m tourModel) View(w, h int) string {
	if !m.active || len(m.steps) == 0 {
		return ""
	}
	if m.step >= len(m.steps) {
		m.step = len(m.steps) - 1
	}
	step := m.steps[m.step]

	// Card width = clamp between 50 and 80 cols so the layout reads
	// well on phones (narrow) and big monitors (don't get a wall).
	cardW := w - 6
	if cardW > 80 {
		cardW = 80
	}
	if cardW < 50 {
		cardW = 50
	}

	var lines []string
	// Title.
	titleStyle := m.st.Title.Foreground(m.st.P.Mauve).Bold(true)
	lines = append(lines, titleStyle.Render(step.Title))
	lines = append(lines, "")

	// Body.
	for _, b := range step.Body {
		lines = append(lines, b)
	}
	if len(step.Bullets) > 0 && len(step.Body) > 0 {
		lines = append(lines, "")
	}
	for _, b := range step.Bullets {
		lines = append(lines, "  "+m.st.Key.Render("•")+" "+b)
	}

	// Progress dots.
	lines = append(lines, "")
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
	lines = append(lines, dots.String())

	// Key hint footer.
	if step.KeyHint != "" {
		lines = append(lines, "")
		lines = append(lines, m.st.Muted.Render(step.KeyHint))
	}

	body := strings.Join(lines, "\n")
	card := lipgloss.NewStyle().
		Padding(1, 3).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.st.P.Mauve).
		Width(cardW).
		Render(body)

	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, card)
}
