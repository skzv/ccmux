package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/conversations"
	"github.com/skzv/ccmux/internal/i18n"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tui/styles"
)

func TestConversationPreview_ChineseFormatArguments(t *testing.T) {
	withLang(t, "zh")
	var preview conversationPreviewOverlay
	preview.Open(conversations.Conversation{Project: "demo"})
	out := preview.View(styles.Default(), 120, 40)
	want := fmt.Sprintf("来自 demo 的最近 %d 条消息", previewMessageLimit)
	if strings.Contains(out, "%!") || !strings.Contains(out, want) {
		t.Fatalf("subtitle must contain %q without formatting errors; got:\n%s", want, out)
	}
}

func TestSearchPlaceholders_FollowLanguageSwitch(t *testing.T) {
	withLang(t, "en")
	st := styles.Default()
	projects := newProjects(st, DefaultKeymap())
	projects.projects = []project.Project{{Name: "demo", Path: t.TempDir()}}
	projects.filterActive = true
	notes := newNotes(st, DefaultKeymap())
	notes.project = &projects.projects[0]
	notes.searching = true
	notes.searchInput.Focus()
	for _, lang := range i18n.Codes() {
		i18n.SetLanguage(lang)
		for _, tc := range []struct{ name, view, key string }{
			{"projects", projects.View(240, 40), "type to filter…"},
			{"notes", notes.View(240, 40), "search this project's notes…"},
		} {
			if !strings.Contains(tc.view, i18n.T(tc.key)) {
				t.Errorf("%s placeholder did not switch to %s", tc.name, lang)
			}
		}
	}
}

func TestAgentBrowser_HeadingsFollowLanguageSwitch(t *testing.T) {
	withLang(t, "en")
	st := styles.Default()
	model := claudeModel{st: st}
	browser := newAgentBrowser(st)
	browser.SetSections("Claude", model.browserSections())
	for _, lang := range i18n.Codes() {
		i18n.SetLanguage(lang)
		out := browser.View(120, 40)
		for _, key := range []string{"Hooks", "MCP servers", "Commands", "Skills"} {
			if !strings.Contains(out, i18n.T(key)) {
				t.Errorf("browser heading %q did not switch to %s", key, lang)
			}
		}
	}
}

func TestSettingsLanguage_AllSupportedCodes(t *testing.T) {
	withLang(t, "en")
	field := byLabel(editableFields(), "i18n.lang")
	cfg := config.Defaults()
	for _, code := range i18n.Codes() {
		if err := field.set(&cfg, code); err != nil {
			t.Fatal(err)
		}
		if cfg.Lang != code || string(i18n.Current()) != code {
			t.Fatalf("language did not switch to %s", code)
		}
	}
	previous := cfg.Lang
	if err := field.set(&cfg, "invalid"); err == nil || cfg.Lang != previous {
		t.Fatal("invalid language changed config")
	}
}

func TestLocalizedSettingsFitNarrowTerminals(t *testing.T) {
	withLang(t, "en")
	for _, code := range i18n.Codes() {
		i18n.SetLanguage(code)
		m := newSettings(styles.Default(), DefaultKeymap(), config.Defaults(), "test")
		for _, width := range []int{40, 50, 120} {
			assertNoOverflow(t, m.View(width, 60), width)
		}
		field := byLabel(editableFields(), "i18n.lang")
		options := strings.Join(m.renderDetailOptions(*field), "\n")
		for _, language := range i18n.Languages() {
			if !strings.Contains(options, language.Name) {
				t.Fatal(language.Name)
			}
		}
	}
}

func TestHelpInvitesContributions(t *testing.T) {
	withLang(t, "en")
	for _, code := range i18n.Codes() {
		i18n.SetLanguage(code)
		app := App{styles: styles.Default(), keys: DefaultKeymap(), screen: ScreenSessions}
		out := app.renderHelpOverlay(120, 100)
		for _, value := range []string{"ccmux contribute", i18n.T("Help improve ccmux")} {
			if !strings.Contains(out, value) {
				t.Errorf("%s: missing contribution guidance %q", code, value)
			}
		}
	}
}
