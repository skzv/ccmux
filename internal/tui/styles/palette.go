package styles

import "github.com/charmbracelet/lipgloss"

// Palette is the small set of named colors that every other style in
// the design system derives from. A theme swap is a different Palette
// value passed to FromPalette.
type Palette struct {
	Name string

	BG       lipgloss.Color
	BGAlt    lipgloss.Color
	FG       lipgloss.Color
	FGMuted  lipgloss.Color
	Border   lipgloss.Color
	Selected lipgloss.Color

	Pink     lipgloss.Color
	Mauve    lipgloss.Color
	Red      lipgloss.Color
	Peach    lipgloss.Color
	Yellow   lipgloss.Color
	Green    lipgloss.Color
	Teal     lipgloss.Color
	Sky      lipgloss.Color
	Sapphire lipgloss.Color
	Blue     lipgloss.Color
	Lavender lipgloss.Color
}

// DefaultPalette is ccmux's single ship-default theme. Color values
// come from Catppuccin Mocha — a well-tuned dark palette suitable as
// the design system's anchor while we add theme-swapping later.
//
// The exported name is theme-agnostic on purpose so a future palette
// can be dropped in without renaming call sites.
var DefaultPalette = Palette{
	Name:     "default",
	BG:       "#1e1e2e",
	BGAlt:    "#181825",
	FG:       "#cdd6f4",
	FGMuted:  "#7f849c",
	Border:   "#45475a",
	Selected: "#313244",
	Pink:     "#f5c2e7",
	Mauve:    "#cba6f7",
	Red:      "#f38ba8",
	Peach:    "#fab387",
	Yellow:   "#f9e2af",
	Green:    "#a6e3a1",
	Teal:     "#94e2d5",
	Sky:      "#89dceb",
	Sapphire: "#74c7ec",
	Blue:     "#89b4fa",
	Lavender: "#b4befe",
}
