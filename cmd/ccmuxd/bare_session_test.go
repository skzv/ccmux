package main

import (
	"os"
	"testing"
)

// TestResolveBarePath_PriorityOrder pins the three-layer fallback:
//
//	explicit req.Path  → wins
//	config DefaultDir  → wins over $HOME
//	$HOME              → last-resort
//
// The Sessions tab's "new session" form pre-fills the request path
// from the daemon's config, but the user can override per-session.
// If priority drifts, a user with default_dir="/work" who picks
// "/tmp/x" in the form would silently land in /work instead.
func TestResolveBarePath_PriorityOrder(t *testing.T) {
	// Force HOME to a known value so the fallback is testable.
	t.Setenv("HOME", "/home/test")

	cases := []struct {
		name          string
		reqPath       string
		configDefault string
		want          string
	}{
		{"explicit wins over default", "/tmp/x", "/work", "/tmp/x"},
		{"explicit wins over nothing", "/tmp/x", "", "/tmp/x"},
		{"default wins when no explicit", "", "/work", "/work"},
		{"HOME when nothing set", "", "", "/home/test"},
		{"whitespace explicit treated as empty", "   ", "/work", "/work"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveBarePath(tc.reqPath, tc.configDefault); got != tc.want {
				t.Errorf("resolveBarePath(%q, %q) = %q, want %q",
					tc.reqPath, tc.configDefault, got, tc.want)
			}
		})
	}
}

// TestResolveBarePath_ExpandsTilde — users edit config.toml with
// "~/work" expecting the daemon's home, not a literal "~/work"
// path. The resolver must expand that on every input layer (explicit
// + config). $HOME-fallback never has a tilde to worry about.
func TestResolveBarePath_ExpandsTilde(t *testing.T) {
	t.Setenv("HOME", "/Users/test")

	cases := []struct {
		name          string
		reqPath       string
		configDefault string
		want          string
	}{
		{"tilde in explicit", "~/work", "", "/Users/test/work"},
		{"tilde in default", "", "~/work", "/Users/test/work"},
		{"bare ~ in explicit", "~", "", "/Users/test"},
		{"non-tilde path is left alone", "/etc", "", "/etc"},
		{"tilde mid-path is NOT expanded", "/etc/~foo", "", "/etc/~foo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveBarePath(tc.reqPath, tc.configDefault); got != tc.want {
				t.Errorf("resolveBarePath(%q, %q) = %q, want %q",
					tc.reqPath, tc.configDefault, got, tc.want)
			}
		})
	}
}

// TestExpandTilde_OnlyLeading — the helper deliberately does NOT
// handle mid-path tildes or other shell expansions (env vars, glob).
// Pinning that limitation keeps the next person from adding "smart"
// expansions to a daemon process, which is a quiet source of footguns.
func TestExpandTilde_OnlyLeading(t *testing.T) {
	t.Setenv("HOME", "/h")
	cases := []struct{ in, want string }{
		{"~", "/h"},
		{"~/foo", "/h/foo"},
		{"~foo", "~foo"},     // tilde-username NOT expanded
		{"/a/~/b", "/a/~/b"}, // mid-path tilde NOT expanded
		{"", ""},
		{"/abs", "/abs"},
	}
	for _, tc := range cases {
		if got := expandTilde(tc.in); got != tc.want {
			t.Errorf("expandTilde(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestExpandTilde_HOMEUnsetIsSafe — if $HOME isn't set (rare, but
// possible in stripped sandboxes) we should leave the tilde alone
// rather than expand to "". The bare-session creator would then
// fail os.Stat with a clearer error than "open '/foo': no such file".
func TestExpandTilde_HOMEUnsetIsSafe(t *testing.T) {
	t.Setenv("HOME", "")
	// On macOS, os.UserHomeDir() falls back to the SHELL or login
	// info even when HOME is empty, so we can't always force the
	// failure path. The assertion is "result is not empty" — either
	// the function expanded against the real home (fine) or it left
	// the tilde alone (also fine, both better than empty).
	for _, in := range []string{"~", "~/foo"} {
		got := expandTilde(in)
		if got == "" {
			t.Errorf("expandTilde(%q) returned empty even though we want a non-empty result on $HOME-unset", in)
		}
	}
	_ = os.Setenv
}
