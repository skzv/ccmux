package cmd

import (
	"bytes"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/i18n"
	"strings"
	"testing"
)

func TestLanguageCommand(t *testing.T) {
	withTempCcmuxConfig(t)
	list := newLanguageCmd()
	var out bytes.Buffer
	list.SetOut(&out)
	if err := list.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, language := range i18n.Languages() {
		if !strings.Contains(out.String(), language.Name) {
			t.Fatal(language.Code)
		}
	}
	for _, code := range i18n.Codes() {
		cmd := newLanguageCmd()
		cmd.SetArgs([]string{code})
		cmd.SetOut(&out)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.Load()
		if err != nil || cfg.Lang != code {
			t.Fatalf("language not saved: %s, %v", cfg.Lang, err)
		}
	}
	cmd := newLanguageCmd()
	cmd.SetArgs([]string{"invalid"})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	if err := cmd.Execute(); err == nil {
		t.Fatal("accepted unknown language")
	}
	cfg, _ := config.Load()
	if cfg.Lang != "ru" {
		t.Fatal("invalid input overwrote saved preference")
	}
}
func TestContributeCommand(t *testing.T) {
	withTempCcmuxConfig(t)
	var out bytes.Buffer
	cmd := newContributeCmd()
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"CONTRIBUTING.md", "/issues", "/pulls"} {
		if !strings.Contains(out.String(), suffix) {
			t.Fatal("missing contribution link", suffix)
		}
	}
}
