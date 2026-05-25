package cmd

import (
	"strings"
	"testing"
)

// TestDefaultPort — tiny helper but the wrong default would route
// ccmux shell --host to a non-listening port and the user would see
// connection-refused with no hint pointing at the config. Pin 7474
// as the canonical tailnet port the rest of ccmux uses.
func TestDefaultPort(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, 7474},
		{7474, 7474},
		{8080, 8080}, // explicit override survives
		{1, 1},
	}
	for _, tc := range cases {
		if got := defaultPort(tc.in); got != tc.want {
			t.Errorf("defaultPort(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestShellQuote — local copy of the helper, must behave the same
// way as the TUI's. The single-quote escape ('foo'\”bar') is the
// canonical POSIX trick; getting it wrong breaks remote attach for
// any session with a quote in its name.
func TestShellQuote(t *testing.T) {
	cases := []struct{ in, want string }{
		{"c-foo", "'c-foo'"},
		{"c-foo bar", "'c-foo bar'"},
		{"c'with'quotes", `'c'\''with'\''quotes'`},
		{"", "''"},
	}
	for _, tc := range cases {
		if got := shellQuote(tc.in); got != tc.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestShellAttachCommandsOmitDetachFlag(t *testing.T) {
	local := shellAttachCmd("c-foo")
	if got := strings.Join(local.Args, " "); got != "tmux attach-session -t c-foo" {
		t.Errorf("local shell attach args = %q, want mirror attach without -d", got)
	}

	remote := remoteShellTmuxAttach("c-foo")
	if strings.Contains(remote, " -d ") {
		t.Errorf("remote shell attach should not pass -d: %q", remote)
	}
	if !strings.Contains(remote, " tmux attach-session -t 'c-foo'") {
		t.Errorf("remote shell attach should use mirror attach: %q", remote)
	}
}
