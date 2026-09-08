package i18n

import "testing"

func TestExpandedLocaleResolution(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  Lang
		valid bool
	}{
		{"es_MX.UTF-8", "es", true}, {"ja_JP", "ja", true}, {"ko-KR", "ko", true},
		{"fr_CA.UTF-8", "fr", true}, {"de_DE@euro", "de", true}, {"pt_BR.UTF-8", "pt-br", true},
		{"pt-PT", "pt-br", true}, {"RU_ru.UTF-8", "ru", true}, {"zh_CN", LangZh, true},
		{"english", LangEn, false}, {"zhgarbage", LangEn, false}, {"C.UTF-8", LangEn, false},
	} {
		got, valid := Parse(tc.input)
		if got != tc.want || valid != tc.valid {
			t.Errorf("Parse(%q) = %s, %t", tc.input, got, valid)
		}
		env := func(key string) string {
			if key == "LC_ALL" {
				return tc.input
			}
			return "zh_CN"
		}
		if got := Resolve("", env); got != tc.want {
			t.Errorf("Resolve(%s) = %s", tc.input, got)
		}
	}
}
func TestAllCatalogsAreLoaded(t *testing.T) {
	defer SetLanguage("en")
	for _, language := range Languages() {
		SetLanguage(string(language.Code))
		if Current() != language.Code {
			t.Fatal(language.Code)
		}
		if language.Code != LangEn && T("Help") == "Help" {
			t.Errorf("%s catalog not loaded", language.Code)
		}
	}
}
