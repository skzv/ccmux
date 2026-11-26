package cmd

import (
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/config"
)

func TestConfigFieldSpecs_ThemeValidation(t *testing.T) {
	var spec *configFieldSpec
	for i := range configFieldSpecs() {
		s := configFieldSpecs()[i]
		if s.key == "theme" {
			spec = &s
			break
		}
	}
	if spec == nil {
		t.Fatal("theme config field spec missing")
	}
	cfg := config.Defaults()
	if err := spec.set(&cfg, "catppuccin-latte"); err != nil {
		t.Fatalf("set catppuccin-latte: %v", err)
	}
	if cfg.Theme != "catppuccin-latte" {
		t.Errorf("Theme = %q, want catppuccin-latte", cfg.Theme)
	}
	if err := spec.set(&cfg, "not-a-theme"); err == nil || !strings.Contains(err.Error(), "unsupported theme") {
		t.Errorf("expected unsupported theme error, got %v", err)
	}
}
