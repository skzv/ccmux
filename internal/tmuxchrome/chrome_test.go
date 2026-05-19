package tmuxchrome

import (
	"strings"
	"testing"
)

func TestPrettyKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"C-b", "Ctrl-b"},
		{"C-a", "Ctrl-a"},
		{"M-x", "Alt-x"},
		{"S-F1", "Shift-F1"},
		{"`", "`"}, // backtick remap left as-is
		{"", ""},
		{"plainkey", "plainkey"},
	}
	for _, tc := range cases {
		if got := PrettyKey(tc.in); got != tc.want {
			t.Errorf("PrettyKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestInTmux_RespectsTmuxEnv(t *testing.T) {
	// Save and restore the env hook.
	orig := envLookup
	defer func() { envLookup = orig }()

	envLookup = func(name string) string { return "" }
	if InTmux() {
		t.Error("InTmux should be false when $TMUX is empty")
	}

	envLookup = func(name string) string {
		if name == "TMUX" {
			return "/tmp/tmux-501/default,12345,0"
		}
		return ""
	}
	if !InTmux() {
		t.Error("InTmux should be true when $TMUX is set")
	}

	// Whitespace-only counts as empty.
	envLookup = func(name string) string {
		if name == "TMUX" {
			return "   "
		}
		return ""
	}
	if InTmux() {
		t.Error("InTmux should treat whitespace-only $TMUX as unset")
	}
}

// TestApply_RejectsEmptySession is the one explicit error path Apply
// has. Everything else is best-effort tmux calls we deliberately ignore.
func TestApply_RejectsEmptySession(t *testing.T) {
	if err := Apply(nil, "", "label", false, false); err == nil {
		t.Fatal("expected error for empty session name, got nil")
	}
}

// TestOptions_IncludesProjectAndDetachHint pins what shows up in
// status-left / status-right. Locked down because the remote-create
// regression we just fixed (no chrome on ssh-attached remote sessions)
// would also be caught here if the daemon ever stopped applying these.
func TestOptions_IncludesProjectAndDetachHint(t *testing.T) {
	opts := Options("c-foo", "auth-redesign", false, false, "Ctrl-b")
	asMap := map[string]string{}
	for _, kv := range opts {
		asMap[kv[0]] = kv[1]
	}

	// status-left must brand the bar and surface the project name.
	if got := asMap["status-left"]; !strings.Contains(got, " ccmux ") || !strings.Contains(got, "auth-redesign") {
		t.Errorf("status-left missing ccmux/project: %q", got)
	}
	// status-right must spell out the detach gesture so the user can
	// always recover the dashboard. Not-nested case uses `d`.
	if got := asMap["status-right"]; !strings.Contains(got, "Ctrl-b then d") {
		t.Errorf("status-right missing detach hint: %q", got)
	}
	// Bar visible (status on, position bottom).
	if asMap["status"] != "on" {
		t.Errorf("status = %q, want on", asMap["status"])
	}
	if asMap["status-position"] != "bottom" {
		t.Errorf("status-position = %q, want bottom", asMap["status-position"])
	}
}

// TestOptions_WindowSizeLatest — ccmux sets window-size=latest on
// every session so mirror mode (laptop + phone on the same session)
// sizes the window to whichever client is active rather than the
// smallest one. Pinned here because it's a per-session option a
// future "trim the chrome" refactor could drop without noticing —
// and its absence only shows up when a second client attaches.
func TestOptions_WindowSizeLatest(t *testing.T) {
	opts := Options("c-foo", "p", false, false, "Ctrl-b")
	asMap := map[string]string{}
	for _, kv := range opts {
		asMap[kv[0]] = kv[1]
	}
	if got := asMap["window-size"]; got != "latest" {
		t.Errorf("window-size = %q, want latest (mirror-mode sizing)", got)
	}
}

// TestOptions_NestedSwitchesDetachHint — when ccmux's outer tmux is
// running and we got here via `tmux switch-client`, plain "prefix + d"
// would close the whole client. The chrome must instead tell the user
// to use `prefix + L` to jump back to the outer session.
func TestOptions_NestedSwitchesDetachHint(t *testing.T) {
	opts := Options("c-foo", "p", false, true /*nested*/, "Ctrl-b")
	for _, kv := range opts {
		if kv[0] != "status-right" {
			continue
		}
		if !strings.Contains(kv[1], "Ctrl-b then L") {
			t.Errorf("nested status-right should hint `then L`: %q", kv[1])
		}
		if strings.Contains(kv[1], "then d to detach") {
			t.Errorf("nested status-right should NOT use the d-detach hint: %q", kv[1])
		}
	}
}

// TestOptions_MoshiBadge — paired hosts get the green reachable badge;
// otherwise the muted "not paired" hint pointing at `ccmux moshi-setup`.
func TestOptions_MoshiBadge(t *testing.T) {
	paired := Options("c-foo", "p", true, false, "Ctrl-b")
	unpaired := Options("c-foo", "p", false, false, "Ctrl-b")
	asRight := func(opts [][]string) string {
		for _, kv := range opts {
			if kv[0] == "status-right" {
				return kv[1]
			}
		}
		return ""
	}
	if !strings.Contains(asRight(paired), "reachable via Moshi") {
		t.Errorf("paired: missing reachable badge: %q", asRight(paired))
	}
	if !strings.Contains(asRight(unpaired), "not paired") {
		t.Errorf("unpaired: missing setup nudge: %q", asRight(unpaired))
	}
}

// TestOptions_EmptyLabelFallsBackToSession — projectLabel is optional;
// when the daemon's createProject calls Apply with an empty label,
// the status bar should still render something useful (the session
// name itself).
func TestOptions_EmptyLabelFallsBackToSession(t *testing.T) {
	opts := Options("c-foo", "", false, false, "Ctrl-b")
	for _, kv := range opts {
		if kv[0] == "status-left" && !strings.Contains(kv[1], "c-foo") {
			t.Errorf("empty label: status-left should fall back to session name: %q", kv[1])
		}
	}
}
