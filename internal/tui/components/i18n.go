package components

import "github.com/skzv/ccmux/internal/i18n"

// tr mirrors internal/tui's tr: shared chrome (headers, tab names) in
// this package is localized the same way, so the sync test picks it up.
func tr(key string) string { return i18n.T(key) }
