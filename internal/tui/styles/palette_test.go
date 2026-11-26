package styles

import (
	"testing"

	"github.com/charmbracelet/glamour"
)

func TestPaletteByName_AllThemesValid(t *testing.T) {
	names := ThemeNames()
	if len(names) != 6 {
		t.Fatalf("ThemeNames() = %v (len %d), want 6", names, len(names))
	}
	hasLight := false
	for _, name := range names {
		p, ok := PaletteByName(name)
		if !ok {
			t.Fatalf("PaletteByName(%q) returned ok=false", name)
		}
		if p.Name != name {
			t.Errorf("Palette.Name = %q, want %q", p.Name, name)
		}
		if p.BG == "" || p.FG == "" || p.Mauve == "" || p.Sapphire == "" {
			t.Errorf("palette %q has empty core colors: %+v", name, p)
		}
		if p.IsLight {
			hasLight = true
		}
		// Ensure Glamour builds cleanly for every theme.
		st := FromPalette(p)
		if _, err := glamour.NewTermRenderer(glamour.WithStyles(GlamourStyle(st)), glamour.WithWordWrap(80)); err != nil {
			t.Errorf("glamour renderer failed for %q: %v", name, err)
		}
	}
	if !hasLight {
		t.Error("expected at least one light theme (catppuccin-latte)")
	}
}

func TestPaletteByName_AliasesAndUnknown(t *testing.T) {
	for _, alias := range []string{"", "default", "DEFAULT", "  Catppuccin-Mocha  "} {
		p, ok := PaletteByName(alias)
		if !ok || p.Name != DefaultPalette.Name {
			t.Errorf("PaletteByName(%q) = (%q, %v), want (%q, true)", alias, p.Name, ok, DefaultPalette.Name)
		}
	}
	if _, ok := PaletteByName("nonexistent-theme"); ok {
		t.Error("PaletteByName(nonexistent-theme) = true, want false")
	}
	if got := ForTheme("nonexistent-theme"); got.P.Name != DefaultPalette.Name {
		t.Errorf("ForTheme(unknown) fell back to %q, want %q", got.P.Name, DefaultPalette.Name)
	}
}
