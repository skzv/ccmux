package styles

import "github.com/charmbracelet/lipgloss"

// SpacingScale is the named cell-count token set used for padding,
// margin, and gap calculations across the TUI. Values are terminal
// cells; XS is no space, SM the unit cell, MD the component-internal
// default, and so on. Screens MUST select from these named tokens
// rather than passing bare integer literals to lipgloss.
type SpacingScale struct {
	XS int
	SM int
	MD int
	LG int
	XL int
}

// DefaultSpacing returns the project's canonical spacing scale.
func DefaultSpacing() SpacingScale {
	return SpacingScale{XS: 0, SM: 1, MD: 2, LG: 3, XL: 4}
}

// RadiusSet exposes named border-roundedness tokens. Soft is the
// default rounded look used by every pane, chip, and modal.
type RadiusSet struct {
	Soft lipgloss.Border
}

// DefaultRadius returns the project's canonical radius set.
func DefaultRadius() RadiusSet {
	return RadiusSet{Soft: lipgloss.RoundedBorder()}
}

// TypographyRoles is the named typography scale, derived from a
// Palette. Roles encode the *intent* (Display, Title, Subtitle,
// Body, Caption, Code) rather than the visual treatment, so a
// future theme can swap emphasis or color without touching screens.
type TypographyRoles struct {
	Display  lipgloss.Style
	Title    lipgloss.Style
	Subtitle lipgloss.Style
	Body     lipgloss.Style
	Caption  lipgloss.Style
	Code     lipgloss.Style
}

func typographyForPalette(p Palette) TypographyRoles {
	return TypographyRoles{
		Display:  lipgloss.NewStyle().Foreground(p.Mauve).Bold(true),
		Title:    lipgloss.NewStyle().Foreground(p.Mauve).Bold(true),
		Subtitle: lipgloss.NewStyle().Foreground(p.FGMuted),
		Body:     lipgloss.NewStyle().Foreground(p.FG),
		Caption:  lipgloss.NewStyle().Foreground(p.FGMuted).Faint(true),
		Code:     lipgloss.NewStyle().Foreground(p.Lavender).Background(p.BGAlt),
	}
}

// SemanticColors maps intent to palette colors. Screens reach for a
// semantic color when the *intent* (success, warning, danger, ...)
// is what matters, not the specific hue.
type SemanticColors struct {
	Primary lipgloss.Color
	Success lipgloss.Color
	Warning lipgloss.Color
	Danger  lipgloss.Color
	Info    lipgloss.Color
	Muted   lipgloss.Color
	Accent  lipgloss.Color
}

func semanticForPalette(p Palette) SemanticColors {
	return SemanticColors{
		Primary: p.Mauve,
		Success: p.Green,
		Warning: p.Yellow,
		Danger:  p.Red,
		Info:    p.Sky,
		Muted:   p.FGMuted,
		Accent:  p.Lavender,
	}
}

// Matrix overlay decoration colors. Theme-independent on purpose —
// the easter egg is "The Matrix," not "the current ccmux palette."
// Lives in styles/ so the no-inline-hex lint rule still holds even
// for the overlay screen.
var (
	MatrixGreenStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#00ff66"))
	MatrixGreenDimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#008833"))
	MatrixGreenFaintStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#003311"))
	MatrixWhiteStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#e8ffe8")).Bold(true)
)
