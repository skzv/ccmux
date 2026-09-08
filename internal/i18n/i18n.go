// Package i18n localizes the TUI using embedded, immutable catalogs.
// English phrases are keys and remain the fallback for unknown phrases.
package i18n

import (
	"embed"
	"os"
	"strings"
	"sync/atomic"

	"github.com/BurntSushi/toml"
)

type Lang string

const (
	LangEn Lang = "en"
	LangZh Lang = "zh"
)

type Language struct {
	Code Lang
	Name string
}

var languages = []Language{
	{LangEn, "English"}, {LangZh, "简体中文"}, {"es", "Español"},
	{"ja", "日本語"}, {"ko", "한국어"}, {"fr", "Français"},
	{"de", "Deutsch"}, {"pt-br", "Português (Brasil)"}, {"ru", "Русский"},
}

// Languages returns a copy so callers cannot mutate the registry.
func Languages() []Language { return append([]Language(nil), languages...) }
func Name(code Lang) string {
	for _, language := range languages {
		if language.Code == code {
			return language.Name
		}
	}
	return string(code)
}

func Codes() []string {
	codes := make([]string, len(languages))
	for i, language := range languages {
		codes[i] = string(language.Code)
	}
	return codes
}

//go:embed locales/*.toml
var localesFS embed.FS
var active atomic.Value
var tables = map[Lang]map[string]string{}

func init() {
	active.Store(LangEn)
	for _, language := range languages {
		if language.Code == LangEn {
			continue
		}
		b, err := localesFS.ReadFile("locales/" + string(language.Code) + ".toml")
		if err != nil {
			continue
		}
		table := map[string]string{}
		if _, err := toml.Decode(string(b), &table); err == nil {
			tables[language.Code] = table
		}
	}
}

// Parse accepts supported language tags and common POSIX locale variants.
// Unknown tags are rejected, rather than accidentally matching a prefix.
func Parse(value string) (Lang, bool) {
	tag := strings.ToLower(strings.TrimSpace(value))
	tag = strings.SplitN(strings.SplitN(tag, ".", 2)[0], "@", 2)[0]
	tag = strings.ReplaceAll(tag, "_", "-")
	base := strings.SplitN(tag, "-", 2)[0]
	if base == "pt" {
		return "pt-br", true
	}
	for _, language := range languages {
		if tag == string(language.Code) || base == string(language.Code) {
			return language.Code, true
		}
	}
	return LangEn, false
}

// Resolve honors explicit config, then the first nonempty LC_ALL / LANG.
// Unsupported and C/POSIX locales use English.
func Resolve(lang string, env func(string) string) Lang {
	if lang != "" {
		code, _ := Parse(lang)
		return code
	}
	if env == nil {
		env = os.Getenv
	}
	for _, key := range []string{"LC_ALL", "LANG"} {
		if value := env(key); value != "" {
			code, _ := Parse(value)
			return code
		}
	}
	return LangEn
}
func SetLanguage(lang string) { active.Store(Resolve(lang, nil)) }
func Current() Lang           { return active.Load().(Lang) }
func T(key string) string {
	if value, ok := tables[Current()][key]; ok && value != "" {
		return value
	}
	return key
}
