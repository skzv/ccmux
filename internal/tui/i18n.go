package tui

import "github.com/skzv/ccmux/internal/i18n"

// tr returns the current language's translation of key, falling back to
// the English key itself when untranslated. Every user-visible string in
// this package goes through tr so the sync test in internal/i18n can
// keep the zh.toml translations honest.
func tr(key string) string { return i18n.T(key) }
