package tui

import (
	"math/rand"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// matrixModel is the easter-egg overlay triggered by typing "matrix"
// anywhere in the TUI. Two phases:
//
//  1. phaseNeo — typed-banner sequence ("Wake up, Neo...", etc.),
//     one character per tick, with a brief pause between lines.
//  2. phaseRain — falling green code-rain animation; columns of
//     half-width katakana / digit glyphs advance one row per tick,
//     newest character bright white, trail fading green to black.
//
// Phase 1 auto-advances into phase 2 once the script is exhausted.
// Esc dismisses the overlay from either phase; any other keystroke
// during phase 1 skips ahead to the rain.
//
// The model is intentionally small and deterministic-given-seed so
// the rain stays stable across re-renders inside a single session
// (tea calls View on every message, even non-tick ones, so the
// underlying character grid must not be regenerated from scratch
// each call).
type matrixModel struct {
	active bool
	phase  matrixPhase

	// phase-1 state
	script    []string
	lineIdx   int
	charIdx   int
	holdTicks int // remaining ticks to hold the last char of a line before advancing

	// phase-2 state — column heads. The viewport width can shift on
	// WindowSizeMsg; we resize lazily on the next tick rather than
	// trying to keep state across resizes.
	width, height int
	cols          []matrixColumn
	rng           *rand.Rand
}

type matrixPhase int

const (
	phaseNeo matrixPhase = iota
	phaseRain
)

// matrixColumn is one falling stream. `head` is the row index of the
// brightest cell (the latest character); `length` is how many rows
// of trail to draw below — er, above, since the rain falls down so
// the trail is behind the head. `speed` lets some columns advance
// faster than others; we tick every column on every tea-tick but
// only advance the head when its tickCount % speed == 0.
type matrixColumn struct {
	head      int
	length    int
	speed     int
	tickCount int
	// chars holds one rune per row of the viewport. Rotated as the
	// head advances so each cell can hold a stable glyph until the
	// stream resets, rather than re-randomizing per tick (which
	// looks like static rather than rain).
	chars []rune
}

// matrixTickMsg drives both phases. One message type for both keeps
// the route in App.Update small.
type matrixTickMsg struct{}

func newMatrix() matrixModel {
	return matrixModel{
		script: []string{
			"Wake up, Neo...",
			"The Matrix has you...",
			"Follow the white rabbit.",
			"Knock, knock, Neo.",
		},
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (m matrixModel) Active() bool { return m.active }

// Open switches the overlay on at phase 1, char 0. Idempotent —
// re-opening while active rewinds to the start of the Neo script.
func (m *matrixModel) Open() {
	m.active = true
	m.phase = phaseNeo
	m.lineIdx = 0
	m.charIdx = 0
	m.holdTicks = 0
}

func (m *matrixModel) Close() {
	m.active = false
	m.cols = nil
}

// SetSize is called by App on every WindowSizeMsg. We don't rebuild
// the column grid here — only on the next tick, when the rain phase
// is actually active. Resizing in the middle of phase 1 would clear
// state we still need.
func (m *matrixModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// matrixTick returns a command for the next animation frame. The
// 60ms cadence balances "feels alive" with "doesn't peg a CPU core
// to draw a joke screen" — at this rate, each rain column advances
// roughly 16 rows per second.
func matrixTick() tea.Cmd {
	return tea.Tick(60*time.Millisecond, func(time.Time) tea.Msg { return matrixTickMsg{} })
}

// Update advances the active phase by one frame. Returns the model
// + a follow-up tick. The caller (App.Update) is responsible for
// not calling Update when the overlay is inactive.
func (m matrixModel) Update(msg tea.Msg) (matrixModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "esc" {
			m.Close()
			return m, nil
		}
		// Any other key during the Neo phase skips to rain — keeps
		// the joke from feeling stuck on a slow terminal.
		if m.phase == phaseNeo {
			m.phase = phaseRain
			return m, matrixTick()
		}
		return m, nil
	case matrixTickMsg:
		switch m.phase {
		case phaseNeo:
			return m.advanceNeo(), matrixTick()
		case phaseRain:
			return m.advanceRain(), matrixTick()
		}
	}
	return m, nil
}

// advanceNeo types one more character of the current script line.
// When a line completes, we pause `neoLineHold` ticks before
// advancing to the next line — gives the reader time to actually
// read it. After the last line, we transition to phase 2.
const neoLineHold = 18

func (m matrixModel) advanceNeo() matrixModel {
	if m.lineIdx >= len(m.script) {
		m.phase = phaseRain
		return m
	}
	line := m.script[m.lineIdx]
	if m.charIdx < len(line) {
		m.charIdx++
		return m
	}
	// At end of line — hold, then advance.
	if m.holdTicks < neoLineHold {
		m.holdTicks++
		return m
	}
	m.lineIdx++
	m.charIdx = 0
	m.holdTicks = 0
	if m.lineIdx >= len(m.script) {
		m.phase = phaseRain
	}
	return m
}

// advanceRain advances every column by one tick. The grid is built
// lazily on first call after entering phase 2, and re-built when
// the viewport size changes between calls — cheaper than tracking
// resizes incrementally.
func (m matrixModel) advanceRain() matrixModel {
	if m.width <= 0 || m.height <= 0 {
		return m
	}
	if len(m.cols) != m.width {
		m.cols = make([]matrixColumn, m.width)
		for i := range m.cols {
			m.cols[i] = m.newColumn()
		}
	}
	for i := range m.cols {
		c := &m.cols[i]
		c.tickCount++
		if c.tickCount%c.speed != 0 {
			continue
		}
		c.head++
		// Rotate a fresh glyph into the head position.
		if c.head >= 0 && c.head < len(c.chars) {
			c.chars[c.head] = m.randGlyph()
		}
		// Reset when the trail has fully scrolled off the bottom.
		if c.head-c.length > m.height {
			*c = m.newColumn()
		}
	}
	return m
}

// matrixGlyphs is the character pool. Mixing half-width katakana
// with digits gives the right visual texture: katakana provides
// shape variety, digits anchor the rain in something legible.
var matrixGlyphs = []rune("ｱｲｳｴｵｶｷｸｹｺｻｼｽｾｿﾀﾁﾂﾃﾄﾅﾆﾇﾈﾉﾊﾋﾌﾍﾎﾏﾐﾑﾒﾓﾔﾕﾖﾗﾘﾙﾚﾛﾜﾝ0123456789")

func (m matrixModel) randGlyph() rune {
	return matrixGlyphs[m.rng.Intn(len(matrixGlyphs))]
}

// newColumn produces a fresh stream starting somewhere above the
// viewport (negative head) so columns don't all start at row 0 in
// lockstep. Lengths vary too, between 6 and m.height/2 — short
// columns look like blips, long ones look like sustained streams.
func (m matrixModel) newColumn() matrixColumn {
	c := matrixColumn{
		head:   -m.rng.Intn(m.height),
		length: 6 + m.rng.Intn(maxInt(7, m.height/2)),
		speed:  1 + m.rng.Intn(3),
		chars:  make([]rune, m.height+1),
	}
	for i := range c.chars {
		c.chars[i] = m.randGlyph()
	}
	return c
}

// View renders the overlay. Phase 1 centers the typed banner over
// a black background; phase 2 fills the viewport with the rain.
// Caller blits this on top of whatever the active screen rendered,
// so a fully opaque black fill is intentional — the underlying
// dashboard shouldn't bleed through.
func (m matrixModel) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	switch m.phase {
	case phaseNeo:
		return m.viewNeo(width, height)
	case phaseRain:
		return m.viewRain(width, height)
	}
	return ""
}

var matrixGreen = lipgloss.NewStyle().Foreground(lipgloss.Color("#00ff66"))
var matrixGreenDim = lipgloss.NewStyle().Foreground(lipgloss.Color("#008833"))
var matrixGreenFaint = lipgloss.NewStyle().Foreground(lipgloss.Color("#003311"))
var matrixWhite = lipgloss.NewStyle().Foreground(lipgloss.Color("#e8ffe8")).Bold(true)

func (m matrixModel) viewNeo(width, height int) string {
	// Show typed-so-far across previous lines + partial current line.
	var lines []string
	for i := 0; i <= m.lineIdx && i < len(m.script); i++ {
		s := m.script[i]
		if i == m.lineIdx {
			end := m.charIdx
			if end > len(s) {
				end = len(s)
			}
			// Add a blinking caret on the in-progress line.
			caret := "_"
			if (time.Now().UnixMilli()/400)%2 == 0 {
				caret = " "
			}
			lines = append(lines, matrixGreen.Render(s[:end])+matrixWhite.Render(caret))
		} else {
			lines = append(lines, matrixGreen.Render(s))
		}
	}
	body := strings.Join(lines, "\n\n")
	hint := matrixGreenFaint.Render("(esc to exit · any key to continue)")
	pane := lipgloss.JoinVertical(lipgloss.Left, body, "", hint)
	return lipgloss.Place(width, height,
		lipgloss.Center, lipgloss.Center,
		pane,
		lipgloss.WithWhitespaceChars(" "),
	)
}

// viewRain builds the falling-code grid row by row. We cap output
// to width-by-height runes; no inter-line ANSI prefixes other than
// the per-cell foreground color. Performance: ~width*height
// lipgloss.Render calls per frame, fine at typical terminal sizes
// (e.g. 200x60 = 12k cells, < 5ms on a modern machine).
func (m matrixModel) viewRain(width, height int) string {
	if width <= 0 || height <= 0 || len(m.cols) == 0 {
		return strings.Repeat("\n", height-1)
	}
	var b strings.Builder
	for row := 0; row < height; row++ {
		for col := 0; col < width && col < len(m.cols); col++ {
			c := m.cols[col]
			cell := ' '
			var style lipgloss.Style
			drawn := false
			if row >= 0 && row <= c.head && row >= c.head-c.length && row < len(c.chars) {
				cell = c.chars[row]
				switch {
				case row == c.head:
					style = matrixWhite
				case row > c.head-3:
					style = matrixGreen
				case row > c.head-c.length/2:
					style = matrixGreenDim
				default:
					style = matrixGreenFaint
				}
				drawn = true
			}
			if drawn {
				b.WriteString(style.Render(string(cell)))
			} else {
				b.WriteRune(' ')
			}
		}
		if row < height-1 {
			b.WriteRune('\n')
		}
	}
	return b.String()
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
