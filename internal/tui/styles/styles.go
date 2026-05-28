// Package styles is ccmux's TUI design-system source of truth.
//
// Layout:
//
//   - palette.go — Palette type + the project's DefaultPalette.
//   - tokens.go  — non-color tokens (Spacing, Radius) plus
//     palette-derived TypographyRoles and SemanticColors, plus the
//     theme-invariant Matrix overlay decoration styles.
//   - styles.go  — the Styles aggregate every screen consumes,
//     plus FromPalette / Default / HostColor.
//
// The contract: every TUI screen MUST source every color, spacing
// value, border, and styled run from a Styles value (or the matrix
// overlay styles exposed alongside). Files outside
// internal/tui/styles/ and internal/tui/components/ MUST NOT
// reference lipgloss.Color("#...") or hand-rolled padding / margin
// integers; see TestNoInlineStyleLiteralsInScreens for the lint
// enforcement.
package styles

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/agent"
)

// Styles is the full ccmux style set, derived from a Palette. Every
// screen reads from a Styles value passed in via its model, or from
// Default() at program start.
type Styles struct {
	// P exposes the raw palette for the few callers that need a
	// palette-only lookup (HostColor's hash ramp, gradient ramps).
	// Screens MUST NOT pass P colors through lipgloss.Color literals;
	// pulling a value off P is consuming a token, which is fine.
	P Palette

	// Design-system tokens (see tokens.go).
	Spacing  SpacingScale
	Radius   RadiusSet
	Type     TypographyRoles
	Semantic SemanticColors

	// Layout primitives.
	App         lipgloss.Style
	Pane        lipgloss.Style
	PaneFocused lipgloss.Style
	Title       lipgloss.Style
	Subtitle    lipgloss.Style

	// Status chips and banners.
	StatusBar     lipgloss.Style
	StatusGood    lipgloss.Style
	StatusWarning lipgloss.Style
	StatusError   lipgloss.Style
	StatusDanger  lipgloss.Style // for Mode 2 / Mode 3 banners

	// Lists.
	ListItem         lipgloss.Style
	ListItemSelected lipgloss.Style
	ListItemFaded    lipgloss.Style

	// Session-state glyphs.
	StateActive     lipgloss.Style
	StateIdle       lipgloss.Style
	StateNeedsInput lipgloss.Style
	StateError      lipgloss.Style
	StateUnknown    lipgloss.Style

	// Host-origin badge color for "local" rows. Remote hosts hash
	// through HostColor.
	HostLocal lipgloss.Style

	// Misc.
	Key      lipgloss.Style
	Toast    lipgloss.Style
	Muted    lipgloss.Style
	Emphasis lipgloss.Style

	// Tab strip (top-of-screen navigation). TabActive is the
	// currently-focused screen tab; TabInactive is every other
	// screen. Active uses bold + underlined FG (no purple
	// background) so the visual emphasis comes from weight and
	// underline rather than a colored block — the previous
	// lavender-bold treatment read as a "purple background" to
	// some users and the accent color clashed with the bold
	// rendering of certain terminals.
	TabActive   lipgloss.Style
	TabInactive lipgloss.Style
}

// FromPalette builds a Styles aggregate from a Palette.
func FromPalette(p Palette) Styles {
	s := Styles{
		P:        p,
		Spacing:  DefaultSpacing(),
		Radius:   DefaultRadius(),
		Type:     typographyForPalette(p),
		Semantic: semanticForPalette(p),
	}

	s.App = lipgloss.NewStyle().Background(p.BG).Foreground(p.FG)
	s.Pane = lipgloss.NewStyle().
		Border(s.Radius.Soft).
		BorderForeground(p.Border).
		Padding(s.Spacing.XS, s.Spacing.SM)
	// Focused pane uses Sapphire (soft blue) instead of the vivid
	// Mauve previously here. The mauve border read as a heavy
	// "purple block" once two focused panes were on screen and
	// competed with the rest of the chrome. Sapphire is the
	// established "focus" color across most terminal UIs — calmer
	// while still unambiguous.
	s.PaneFocused = s.Pane.BorderForeground(p.Sapphire)
	s.Title = s.Type.Title
	s.Subtitle = s.Type.Subtitle

	s.StatusBar = lipgloss.NewStyle().
		Background(p.BGAlt).
		Foreground(p.FG).
		Padding(s.Spacing.XS, s.Spacing.SM)
	s.StatusGood = lipgloss.NewStyle().Foreground(s.Semantic.Success)
	s.StatusWarning = lipgloss.NewStyle().Foreground(s.Semantic.Warning)
	s.StatusError = lipgloss.NewStyle().Foreground(s.Semantic.Danger)
	s.StatusDanger = lipgloss.NewStyle().
		Background(s.Semantic.Danger).
		Foreground(p.BG).
		Bold(true).
		Padding(s.Spacing.XS, s.Spacing.SM)

	s.ListItem = lipgloss.NewStyle().Padding(s.Spacing.XS, s.Spacing.SM)
	s.ListItemSelected = lipgloss.NewStyle().
		Background(p.Selected).
		Foreground(s.Semantic.Accent).
		Bold(true).
		Padding(s.Spacing.XS, s.Spacing.SM)
	s.ListItemFaded = s.ListItem.Foreground(p.FGMuted)

	s.StateActive = lipgloss.NewStyle().Foreground(s.Semantic.Success).Bold(true)
	s.StateIdle = lipgloss.NewStyle().Foreground(s.Semantic.Info)
	s.StateNeedsInput = lipgloss.NewStyle().Foreground(s.Semantic.Warning).Bold(true)
	s.StateError = lipgloss.NewStyle().Foreground(s.Semantic.Danger).Bold(true)
	s.StateUnknown = lipgloss.NewStyle().Foreground(p.FGMuted)

	s.HostLocal = lipgloss.NewStyle().Foreground(p.Teal)

	s.Key = lipgloss.NewStyle().Foreground(p.Peach).Bold(true)
	s.Toast = lipgloss.NewStyle().
		Background(p.Mauve).
		Foreground(p.BG).
		Padding(s.Spacing.XS, s.Spacing.SM)
	s.Muted = lipgloss.NewStyle().Foreground(s.Semantic.Muted)
	s.Emphasis = lipgloss.NewStyle().Foreground(s.Semantic.Accent).Bold(true)

	// Tab strip. Active tab: bright FG + bold. Inactive: muted
	// gray. Emphasis comes from weight + brightness contrast
	// against the muted siblings — no underline (visual noise
	// that doubled up with the existing tab label brackets) and
	// no colored block.
	s.TabActive = lipgloss.NewStyle().Foreground(p.FG).Bold(true)
	s.TabInactive = lipgloss.NewStyle().Foreground(p.FGMuted)

	return s
}

// Default returns the default Styles built from DefaultPalette.
func Default() Styles {
	return FromPalette(DefaultPalette)
}

// AgentAccent returns the per-agent accent style. The mapping is the
// design system's single source of truth for agent colour coding: every
// surface that surfaces multiple agents (the Dashboard's Usage panel,
// the Conversations section nav, the Conversations row's agent label
// column, the Agents sub-tab row) reads from this method.
//
// Mapping: Claude=Mauve, Codex=Sky, Antigravity=Peach, Cursor=Teal.
// Unknown IDs fall back to the muted style so a future agent without
// a colour assignment still renders.
//
// The returned style sets the foreground only; callers add Bold (or
// other modifiers) themselves so the same accent can be reused for
// both loud headings and quieter row labels.
func (s Styles) AgentAccent(id agent.ID) lipgloss.Style {
	var color lipgloss.Color
	switch id {
	case agent.IDClaude:
		color = s.P.Mauve
	case agent.IDCodex:
		color = s.P.Sky
	case agent.IDAntigravity:
		color = s.P.Peach
	case agent.IDCursor:
		color = s.P.Teal
	default:
		return s.Muted
	}
	return lipgloss.NewStyle().Foreground(color)
}

// HostColor returns a deterministic color for a remote host name so
// the dashboard can color-code which sessions came from where.
// "local" is teal; others hash to one of the accent colors.
func (s Styles) HostColor(host string) lipgloss.Style {
	if host == "" || host == "local" {
		return s.HostLocal
	}
	accents := []lipgloss.Color{s.P.Mauve, s.P.Pink, s.P.Peach, s.P.Sapphire, s.P.Lavender, s.P.Yellow}
	h := 0
	for i := 0; i < len(host); i++ {
		h = (h*31 + int(host[i])) & 0x7fffffff
	}
	return lipgloss.NewStyle().Foreground(accents[h%len(accents)])
}
