package i18n

import "testing"

func TestResolve(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	cases := []struct {
		name string
		lang string
		env  map[string]string
		want Lang
	}{
		{name: "explicit zh wins", lang: "zh", env: map[string]string{"LANG": "en_US.UTF-8"}, want: LangZh},
		{name: "explicit en wins", lang: "en", env: map[string]string{"LANG": "zh_CN.UTF-8"}, want: LangEn},
		{name: "empty falls back to zh LANG", lang: "", env: map[string]string{"LANG": "zh_CN.UTF-8"}, want: LangZh},
		{name: "empty falls back to zh LC_ALL", lang: "", env: map[string]string{"LC_ALL": "zh_TW"}, want: LangZh},
		{name: "empty + english LANG", lang: "", env: map[string]string{"LANG": "en_US.UTF-8"}, want: LangEn},
		{name: "empty + no locale", lang: "", env: map[string]string{}, want: LangEn},
		{name: "unknown explicit falls back to env", lang: "fr", env: map[string]string{"LANG": "en_US"}, want: LangEn},
		{name: "explicit zh-CN variant", lang: "zh-CN", env: map[string]string{"LANG": "en_US"}, want: LangZh},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Resolve(tc.lang, env(tc.env)); got != tc.want {
				t.Errorf("Resolve(%q, %v) = %q, want %q", tc.lang, tc.env, got, tc.want)
			}
		})
	}
}

func TestT_EnglishFallback(t *testing.T) {
	SetLanguage("zh")
	defer SetLanguage("en")
	if got := T("Some untranslated string"); got != "Some untranslated string" {
		t.Errorf("T(missing) = %q, want English key verbatim", got)
	}
}

func TestT_ChineseTranslation(t *testing.T) {
	SetLanguage("zh")
	defer SetLanguage("en")
	if got := T("Sessions"); got != "会话" {
		t.Errorf("T(Sessions) = %q, want 会话", got)
	}
}

func TestSetLanguage_EmptyResolvesEnv(t *testing.T) {
	SetLanguage("")
	if l := Current(); l != LangEn && l != LangZh {
		t.Errorf("Current() = %q, want en or zh", l)
	}
}
