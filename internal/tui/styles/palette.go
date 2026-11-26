package styles

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Palette is the small set of named colors that every other style in
// the design system derives from. A theme swap is a different Palette
// value passed to FromPalette.
type Palette struct {
	Name    string
	IsLight bool

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

// DefaultPalette is ccmux's ship-default theme (Catppuccin Mocha).
// The exported name stays theme-agnostic so call sites don't couple
// to a specific palette name.
var DefaultPalette = Palette{
	Name:     "catppuccin-mocha",
	IsLight:  false,
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

// CatppuccinLattePalette is a high-contrast light terminal theme.
var CatppuccinLattePalette = Palette{
	Name:     "catppuccin-latte",
	IsLight:  true,
	BG:       "#eff1f5",
	BGAlt:    "#e6e9ef",
	FG:       "#4c4f69",
	FGMuted:  "#6c6f85",
	Border:   "#bcc0cc",
	Selected: "#ccd0da",
	Pink:     "#ea76cb",
	Mauve:    "#8839ef",
	Red:      "#d20f39",
	Peach:    "#fe640b",
	Yellow:   "#df8e1d",
	Green:    "#40a02b",
	Teal:     "#179299",
	Sky:      "#04a5e5",
	Sapphire: "#209fb5",
	Blue:     "#1e66f5",
	Lavender: "#7287fd",
}

// TokyoNightPalette is the Tokyo Night dark palette.
var TokyoNightPalette = Palette{
	Name:     "tokyo-night",
	IsLight:  false,
	BG:       "#1a1b26",
	BGAlt:    "#16161e",
	FG:       "#c0caf5",
	FGMuted:  "#565f89",
	Border:   "#3b4261",
	Selected: "#283457",
	Pink:     "#f7768e",
	Mauve:    "#bb9af7",
	Red:      "#f7768e",
	Peach:    "#ff9e64",
	Yellow:   "#e0af68",
	Green:    "#9ece6a",
	Teal:     "#73daca",
	Sky:      "#7dcfff",
	Sapphire: "#2ac3de",
	Blue:     "#7aa2f7",
	Lavender: "#a9b1d6",
}

// DraculaPalette is the Dracula dark palette.
var DraculaPalette = Palette{
	Name:     "dracula",
	IsLight:  false,
	BG:       "#282a36",
	BGAlt:    "#21222c",
	FG:       "#f8f8f2",
	FGMuted:  "#6272a4",
	Border:   "#44475a",
	Selected: "#44475a",
	Pink:     "#ff79c6",
	Mauve:    "#bd93f9",
	Red:      "#ff5555",
	Peach:    "#ffb86c",
	Yellow:   "#f1fa8c",
	Green:    "#50fa7b",
	Teal:     "#8be9fd",
	Sky:      "#8be9fd",
	Sapphire: "#66d9ef",
	Blue:     "#8be9fd",
	Lavender: "#d6acff",
}

// NordPalette is the Nord arctic dark palette.
var NordPalette = Palette{
	Name:     "nord",
	IsLight:  false,
	BG:       "#2e3440",
	BGAlt:    "#242933",
	FG:       "#eceff4",
	FGMuted:  "#7b88a1",
	Border:   "#4c566a",
	Selected: "#3b4252",
	Pink:     "#b48ead",
	Mauve:    "#b48ead",
	Red:      "#bf616a",
	Peach:    "#d08770",
	Yellow:   "#ebcb8b",
	Green:    "#a3be8c",
	Teal:     "#8fbcbb",
	Sky:      "#88c0d0",
	Sapphire: "#81a1c1",
	Blue:     "#5e81ac",
	Lavender: "#d8dee9",
}

// GruvboxPalette is the Gruvbox dark palette.
var GruvboxPalette = Palette{
	Name:     "gruvbox",
	IsLight:  false,
	BG:       "#282828",
	BGAlt:    "#1d2021",
	FG:       "#ebdbb2",
	FGMuted:  "#928374",
	Border:   "#504945",
	Selected: "#3c3836",
	Pink:     "#d3869b",
	Mauve:    "#d3869b",
	Red:      "#fb4934",
	Peach:    "#fe8019",
	Yellow:   "#fabd2f",
	Green:    "#b8bb26",
	Teal:     "#8ec07c",
	Sky:      "#83a598",
	Sapphire: "#83a598",
	Blue:     "#458588",
	Lavender: "#d5c4a1",
}

var builtInPalettes = []Palette{
	DefaultPalette,
	CatppuccinLattePalette,
	TokyoNightPalette,
	DraculaPalette,
	NordPalette,
	GruvboxPalette,
}

// ThemeNames returns the canonical names of all built-in themes in
// picker order.
func ThemeNames() []string {
	out := make([]string, len(builtInPalettes))
	for i, p := range builtInPalettes {
		out[i] = p.Name
	}
	return out
}

// PaletteByName resolves a theme name (case-insensitive) to its
// Palette. Empty string and "default" resolve to DefaultPalette.
func PaletteByName(name string) (Palette, bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" || key == "default" {
		return DefaultPalette, true
	}
	for _, p := range builtInPalettes {
		if p.Name == key {
			return p, true
		}
	}
	return Palette{}, false
}

// ForTheme builds a Styles aggregate for the named theme, falling back
// to DefaultPalette if name is empty or unknown so an invalid config
// value never breaks startup.
func ForTheme(name string) Styles {
	if p, ok := PaletteByName(name); ok {
		return FromPalette(p)
	}
	return Default()
}
