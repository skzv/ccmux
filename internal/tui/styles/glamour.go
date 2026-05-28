package styles

import "github.com/charmbracelet/glamour/ansi"

// GlamourStyle returns the ansi.StyleConfig that the Notes preview
// pane (and any other Glamour render in ccmux) MUST pass via
// glamour.WithStyles(...). The mapping reads design-system tokens
// off the Styles aggregate so a future palette swap retunes Glamour
// alongside the rest of the TUI without touching this code.
//
// Mapping (locked by the tui-design-system spec):
//
//   - H1                  → bold bright FG (no color block — the old
//     mauve H1 read as a heavy "purple slab"
//     and competed with the rest of the chrome)
//   - H2 / H3             → bold muted FG (subtitle weight)
//   - H4+                 → muted, faint
//   - inline code / code  → Palette.Lavender on Palette.BGAlt
//   - links               → Palette.Sapphire (calm blue — matches
//     the focused-pane border)
//   - block quote         → Muted with a Sapphire leading bar
//
// The Palette uses lipgloss.Color (string hex like "#74c7ec"),
// which is what Glamour's StylePrimitive expects (pointer-to-string
// hex).
func GlamourStyle(s Styles) ansi.StyleConfig {
	titleColor := colorPtr(string(s.P.FG))
	subtitleColor := colorPtr(string(s.P.FG))
	mutedColor := colorPtr(string(s.P.FGMuted))
	codeBG := colorPtr(string(s.P.BGAlt))
	codeFG := colorPtr(string(s.P.Lavender))
	linkColor := colorPtr(string(s.P.Sapphire))
	accentColor := colorPtr(string(s.P.Sapphire))
	fgColor := colorPtr(string(s.P.FG))
	trueP := truePtr()
	indent1 := uintPtr(1)
	indent2 := uintPtr(2)
	margin0 := uintPtr(0)

	return ansi.StyleConfig{
		Document: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Color: fgColor,
				// No BackgroundColor — the preview pane should
				// inherit the terminal's normal background so the
				// rendered markdown blends into the rest of the
				// TUI instead of sitting on a colored slab.
			},
			Margin: margin0,
		},
		BlockQuote: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Color:       mutedColor,
				Italic:      trueP,
				BlockPrefix: "▌ ",
			},
			Indent: indent1,
		},
		Paragraph: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{Color: fgColor},
		},
		List: ansi.StyleList{
			StyleBlock: ansi.StyleBlock{
				StylePrimitive: ansi.StylePrimitive{Color: fgColor},
				Indent:         indent2,
			},
			LevelIndent: 2,
		},
		Heading: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				BlockSuffix: "\n",
				Color:       titleColor,
				Bold:        trueP,
			},
		},
		H1: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix: "# ",
				Color:  titleColor,
				Bold:   trueP,
			},
		},
		H2: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix: "## ",
				Color:  subtitleColor,
				Bold:   trueP,
			},
		},
		H3: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix: "### ",
				Color:  subtitleColor,
				Bold:   trueP,
			},
		},
		H4: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix: "#### ",
				Color:  mutedColor,
				Faint:  trueP,
			},
		},
		H5: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix: "##### ",
				Color:  mutedColor,
				Faint:  trueP,
			},
		},
		H6: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix: "###### ",
				Color:  mutedColor,
				Faint:  trueP,
			},
		},
		Strikethrough:  ansi.StylePrimitive{CrossedOut: trueP},
		Emph:           ansi.StylePrimitive{Italic: trueP},
		Strong:         ansi.StylePrimitive{Bold: trueP},
		HorizontalRule: ansi.StylePrimitive{Color: mutedColor, Format: "\n──────\n"},
		Item:           ansi.StylePrimitive{BlockPrefix: "• "},
		Enumeration:    ansi.StylePrimitive{BlockPrefix: ". "},
		Task: ansi.StyleTask{
			StylePrimitive: ansi.StylePrimitive{},
			Ticked:         "[✓] ",
			Unticked:       "[ ] ",
		},
		Link: ansi.StylePrimitive{
			Color:     linkColor,
			Underline: trueP,
		},
		LinkText: ansi.StylePrimitive{
			Color: accentColor,
			Bold:  trueP,
		},
		Image: ansi.StylePrimitive{
			Color:     linkColor,
			Underline: trueP,
		},
		ImageText: ansi.StylePrimitive{
			Color:  accentColor,
			Format: "Image: {{.text}} →",
		},
		Code: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix:          " ",
				Suffix:          " ",
				Color:           codeFG,
				BackgroundColor: codeBG,
			},
		},
		CodeBlock: ansi.StyleCodeBlock{
			StyleBlock: ansi.StyleBlock{
				StylePrimitive: ansi.StylePrimitive{
					Color:           codeFG,
					BackgroundColor: codeBG,
				},
				Margin: margin0,
			},
			Theme: "catppuccin-mocha",
		},
		Table: ansi.StyleTable{
			StyleBlock: ansi.StyleBlock{
				StylePrimitive: ansi.StylePrimitive{},
			},
			CenterSeparator: stringPtr("┼"),
			ColumnSeparator: stringPtr("│"),
			RowSeparator:    stringPtr("─"),
		},
		DefinitionList:        ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{}},
		DefinitionTerm:        ansi.StylePrimitive{},
		DefinitionDescription: ansi.StylePrimitive{BlockPrefix: "\n🠶 "},
		HTMLBlock:             ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{}},
		HTMLSpan:              ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{}},
	}
}

func colorPtr(s string) *string {
	return &s
}

func stringPtr(s string) *string {
	return &s
}

func truePtr() *bool {
	b := true
	return &b
}

func uintPtr(v uint) *uint {
	return &v
}
