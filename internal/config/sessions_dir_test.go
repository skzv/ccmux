package config

import "testing"

// TestResolveBareSessionDir_PriorityOrder pins the three-layer fallback
// every bare-session creator (ccmuxd, the TUI's local spawn) shares:
//
//	explicit path        → wins
//	sessions.default_dir → wins over $HOME
//	$HOME                → last resort
//
// If priority drifts, a user with default_dir="/work" who types
// "/tmp/x" in the form would land in /work instead, and a blank form
// would land in $HOME instead of default_dir (#234).
func TestResolveBareSessionDir_PriorityOrder(t *testing.T) {
	t.Setenv("HOME", "/home/test")

	cases := []struct {
		name       string
		reqPath    string
		defaultDir string
		want       string
	}{
		{"explicit wins over default", "/tmp/x", "/work", "/tmp/x"},
		{"explicit wins over nothing", "/tmp/x", "", "/tmp/x"},
		{"default wins when no explicit", "", "/work", "/work"},
		{"HOME when nothing set", "", "", "/home/test"},
		{"whitespace explicit treated as empty", "   ", "/work", "/work"},
		{"whitespace default treated as empty", "", " \t", "/home/test"},
		{"explicit is trimmed", "  /tmp/x  ", "/work", "/tmp/x"},
		{"default is trimmed", "", "  /work  ", "/work"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveBareSessionDir(tc.reqPath, tc.defaultDir); got != tc.want {
				t.Errorf("ResolveBareSessionDir(%q, %q) = %q, want %q",
					tc.reqPath, tc.defaultDir, got, tc.want)
			}
		})
	}
}

// TestResolveBareSessionDir_ExpandsTilde — users write "~/work" in
// config.toml and the form expecting this host's home, not a literal
// "~/work" directory. Both input layers expand; only a leading tilde
// does.
func TestResolveBareSessionDir_ExpandsTilde(t *testing.T) {
	t.Setenv("HOME", "/Users/test")

	cases := []struct {
		name       string
		reqPath    string
		defaultDir string
		want       string
	}{
		{"tilde in explicit", "~/work", "", "/Users/test/work"},
		{"tilde in default", "", "~/work", "/Users/test/work"},
		{"bare ~ in explicit", "~", "/work", "/Users/test"},
		{"bare ~ in default", "", "~", "/Users/test"},
		{"explicit tilde wins over default", "~/a", "~/b", "/Users/test/a"},
		{"non-tilde path is left alone", "/etc", "", "/etc"},
		{"tilde mid-path is NOT expanded", "/etc/~foo", "", "/etc/~foo"},
		{"tilde-username is NOT expanded", "~foo", "", "~foo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveBareSessionDir(tc.reqPath, tc.defaultDir); got != tc.want {
				t.Errorf("ResolveBareSessionDir(%q, %q) = %q, want %q",
					tc.reqPath, tc.defaultDir, got, tc.want)
			}
		})
	}
}
