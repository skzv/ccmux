// Package styles holds every color and Lipgloss style ccmux renders with.
// No screen file should hard-code a color — pull from here so themes work.
package styles

import "github.com/charmbracelet/lipgloss"

// Palette is a small set of semantic colors used across the TUI.
// Built from Catppuccin Mocha; future themes implement the same Palette.
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

// CatppuccinMocha is the default theme.
// Color values are the canonical hex codes from
// https://github.com/catppuccin/catppuccin (the Mocha flavor).
var CatppuccinMocha = Palette{
	Name:     "catppuccin-mocha",
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

// Styles is the full Lipgloss style set, derived from a Palette.
type Styles struct {
	P Palette

	// Layout
	App         lipgloss.Style
	Pane        lipgloss.Style
	PaneFocused lipgloss.Style
	Title       lipgloss.Style
	Subtitle    lipgloss.Style

	// Status
	StatusBar     lipgloss.Style
	StatusGood    lipgloss.Style
	StatusWarning lipgloss.Style
	StatusError   lipgloss.Style
	StatusDanger  lipgloss.Style // for Mode 2 / Mode 3 banners

	// Lists
	ListItem         lipgloss.Style
	ListItemSelected lipgloss.Style
	ListItemFaded    lipgloss.Style

	// Session states
	StateActive     lipgloss.Style
	StateIdle       lipgloss.Style
	StateNeedsInput lipgloss.Style
	StateError      lipgloss.Style
	StateUnknown    lipgloss.Style

	// Host origin (color-codes "local" vs each remote)
	HostLocal lipgloss.Style

	// Misc
	Key      lipgloss.Style
	Toast    lipgloss.Style
	Muted    lipgloss.Style
	Emphasis lipgloss.Style
}

// FromPalette builds a Styles set from a Palette.
func FromPalette(p Palette) Styles {
	s := Styles{P: p}

	s.App = lipgloss.NewStyle().Background(p.BG).Foreground(p.FG)
	s.Pane = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(p.Border).
		Padding(0, 1)
	s.PaneFocused = s.Pane.BorderForeground(p.Mauve)
	s.Title = lipgloss.NewStyle().Foreground(p.Mauve).Bold(true)
	s.Subtitle = lipgloss.NewStyle().Foreground(p.FGMuted)

	s.StatusBar = lipgloss.NewStyle().
		Background(p.BGAlt).
		Foreground(p.FG).
		Padding(0, 1)
	s.StatusGood = lipgloss.NewStyle().Foreground(p.Green)
	s.StatusWarning = lipgloss.NewStyle().Foreground(p.Yellow)
	s.StatusError = lipgloss.NewStyle().Foreground(p.Red)
	s.StatusDanger = lipgloss.NewStyle().
		Background(p.Red).
		Foreground(p.BG).
		Bold(true).
		Padding(0, 1)

	s.ListItem = lipgloss.NewStyle().Padding(0, 1)
	s.ListItemSelected = lipgloss.NewStyle().
		Background(p.Selected).
		Foreground(p.Lavender).
		Bold(true).
		Padding(0, 1)
	s.ListItemFaded = s.ListItem.Foreground(p.FGMuted)

	s.StateActive = lipgloss.NewStyle().Foreground(p.Green).Bold(true)
	s.StateIdle = lipgloss.NewStyle().Foreground(p.Sky)
	s.StateNeedsInput = lipgloss.NewStyle().Foreground(p.Yellow).Bold(true)
	s.StateError = lipgloss.NewStyle().Foreground(p.Red).Bold(true)
	s.StateUnknown = lipgloss.NewStyle().Foreground(p.FGMuted)

	s.HostLocal = lipgloss.NewStyle().Foreground(p.Teal)

	s.Key = lipgloss.NewStyle().Foreground(p.Peach).Bold(true)
	s.Toast = lipgloss.NewStyle().
		Background(p.Mauve).
		Foreground(p.BG).
		Padding(0, 1)
	s.Muted = lipgloss.NewStyle().Foreground(p.FGMuted)
	s.Emphasis = lipgloss.NewStyle().Foreground(p.Lavender).Bold(true)

	return s
}

// Default returns the default Styles (Catppuccin Mocha).
func Default() Styles {
	return FromPalette(CatppuccinMocha)
}

// HostColor returns a deterministic color for a remote host name so the
// dashboard can color-code which sessions came from where. "local" is teal,
// others hash to one of the accent colors.
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
