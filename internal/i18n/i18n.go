// Package i18n provides the ccmux TUI's lightweight localization.
// English phrases are the keys; a translation table maps them to the
// current language. A missing key returns the English key verbatim, so
// English needs no table and an untranslated string degrades to English
// rather than breaking rendering.
package i18n

import (
	"embed"
	"os"
	"strings"
	"sync/atomic"

	"github.com/BurntSushi/toml"
)

// Lang is a supported UI language.
type Lang string

const (
	LangEn Lang = "en"
	LangZh Lang = "zh"
)

//go:embed locales/*.toml
var localesFS embed.FS

var (
	useChinese atomic.Bool
	zhTable    map[string]string
)

func init() {
	b, err := localesFS.ReadFile("locales/zh.toml")
	if err != nil {
		return // no zh table: everything falls back to English
	}
	if _, err := toml.Decode(string(b), &zhTable); err != nil {
		zhTable = nil // corrupt file: fall back to English, never crash
	}
}

// Resolve picks the effective language. A non-empty explicit value wins
// (recognized as zh by prefix match, anything else is English); an empty
// explicit value uses the first non-empty locale (LC_ALL then LANG).
// Pure so it is table-testable; env may be nil to use os.Getenv.
func Resolve(lang string, env func(string) string) Lang {
	if lang != "" {
		if strings.HasPrefix(strings.ToLower(lang), "zh") {
			return LangZh
		}
		return LangEn
	}
	if env == nil {
		env = os.Getenv
	}
	for _, k := range []string{"LC_ALL", "LANG"} {
		if v := env(k); v != "" {
			return Resolve(v, nil)
		}
	}
	return LangEn
}

// SetLanguage switches the package's current language. An empty value
// resolves from the environment.
func SetLanguage(lang string) {
	useChinese.Store(Resolve(lang, nil) == LangZh)
}

// Current returns the active language.
func Current() Lang {
	if useChinese.Load() {
		return LangZh
	}
	return LangEn
}

// T returns the current language's translation of key, or the key
// itself when untranslated. Never panics.
func T(key string) string {
	if Current() == LangZh && zhTable != nil {
		if v, ok := zhTable[key]; ok {
			return v
		}
	}
	return key
}
