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
		{"BSpace", "Backspace"},
		{"M-BSpace", "Alt-Backspace"},
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

func TestOptions_SuppressesNativeTmuxBellForwarding(t *testing.T) {
	opts := Options("c-foo", "p", false, false, "Ctrl-b")
	asMap := map[string]string{}
	for _, kv := range opts {
		asMap[kv[0]] = kv[1]
	}
	if got := asMap["bell-action"]; got != "none" {
		t.Errorf("bell-action = %q, want none", got)
	}
	win := windowOptions()
	if len(win) != 1 || win[0][0] != "monitor-bell" || win[0][1] != "on" {
		t.Fatalf("windowOptions = %#v, want monitor-bell on", win)
	}
}

func TestWindowTargetsFromIndexes_AllWindowsInSession(t *testing.T) {
	got := windowTargetsFromIndexes("c-foo", []byte("1\n2\n\n10\n"))
	want := []string{"c-foo:1", "c-foo:2", "c-foo:10"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("windowTargetsFromIndexes = %#v, want %#v", got, want)
	}
}

// TestOptions_NestedSwitchesDetachHint — when ccmux's outer tmux is
// running and we got here via `tmux switch-client`, plain "prefix + d"
// would close the whole client. The chrome must instead tell the user
// to use `prefix then Backspace` to jump back to the outer session.
func TestOptions_NestedSwitchesDetachHint(t *testing.T) {
	opts := Options("c-foo", "p", false, true /*nested*/, "Ctrl-b")
	for _, kv := range opts {
		if kv[0] != "status-right" {
			continue
		}
		if !strings.Contains(kv[1], "Ctrl-b then Backspace") {
			t.Errorf("nested status-right should hint `then Backspace`: %q", kv[1])
		}
		if strings.Contains(kv[1], "then d to detach") {
			t.Errorf("nested status-right should NOT use the d-detach hint: %q", kv[1])
		}
	}
}

func TestNestedReturnBindingArgs(t *testing.T) {
	got := strings.Join(nestedReturnBindingArgs("F12"), " ")
	want := "tmux bind-key F12 switch-client -l"
	if got != want {
		t.Fatalf("nestedReturnBindingArgs = %q, want %q", got, want)
	}
}

func TestParsePrefixBindings(t *testing.T) {
	raw := "" +
		"bind-key    -T prefix C-b send-prefix\n" +
		"bind-key -r -T prefix L resize-pane -R 5\n" +
		"bind-key    -T prefix BSpace switch-client -l\n"
	got := parsePrefixBindings(raw)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3: %#v", len(got), got)
	}
	if got[1].Key != "L" || strings.Join(got[1].Command, " ") != "resize-pane -R 5" {
		t.Fatalf("repeat binding parse = %#v, want L resize-pane -R 5", got[1])
	}
	if got[2].Key != "BSpace" || strings.Join(got[2].Command, " ") != "switch-client -l" {
		t.Fatalf("return binding parse = %#v, want BSpace switch-client -l", got[2])
	}
}

func TestChooseNestedReturnBinding_UsesExistingReturnBinding(t *testing.T) {
	got := chooseNestedReturnBinding([]prefixBinding{
		{Key: "L", Command: []string{"resize-pane", "-R", "5"}},
		{Key: "G", Command: []string{"switch-client", "-l"}},
	})
	if got.Key != "G" || got.Display != "G" || got.ShouldBind {
		t.Fatalf("binding = %#v, want existing G without rebinding", got)
	}
}

func TestChooseNestedReturnBinding_SelectsFirstUnboundFallback(t *testing.T) {
	got := chooseNestedReturnBinding([]prefixBinding{
		{Key: "L", Command: []string{"resize-pane", "-R", "5"}},
	})
	if got.Key != "BSpace" || got.Display != "Backspace" || !got.ShouldBind {
		t.Fatalf("binding = %#v, want BSpace with binding", got)
	}
}

func TestChooseNestedReturnBinding_SkipsBoundFallbacks(t *testing.T) {
	got := chooseNestedReturnBinding([]prefixBinding{
		{Key: "BSpace", Command: []string{"send-prefix"}},
		{Key: "C-g", Command: []string{"display-message", "busy"}},
	})
	if got.Key != "F12" || got.Display != "F12" || !got.ShouldBind {
		t.Fatalf("binding = %#v, want F12 with binding", got)
	}
}

func TestChooseNestedReturnBinding_ExhaustedFallsBackToCommand(t *testing.T) {
	var bindings []prefixBinding
	for _, key := range nestedReturnFallbackKeys {
		bindings = append(bindings, prefixBinding{Key: key, Command: []string{"display-message", key}})
	}
	got := chooseNestedReturnBinding(bindings)
	if got.Key != "" || got.Display != "" || got.ShouldBind {
		t.Fatalf("binding = %#v, want command fallback", got)
	}
	hint := nestedReturnHint("Ctrl-a", got)
	if !strings.Contains(hint, "tmux switch-client -l") {
		t.Fatalf("hint = %q, want command fallback", hint)
	}
}

func TestOptions_NestedUsesResolvedReturnBinding(t *testing.T) {
	opts := optionsWithNestedReturnBinding("c-foo", "p", false, true, "Ctrl-a", NestedReturnBinding{
		Key:     "G",
		Display: "G",
	})
	for _, kv := range opts {
		if kv[0] != "status-right" {
			continue
		}
		if !strings.Contains(kv[1], "Ctrl-a then G") {
			t.Fatalf("status-right = %q, want resolved G hint", kv[1])
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

// TestOptions_SetsTerminalTitle pins the iTerm/GNOME/kitty window-title
// behavior: tmux's set-titles defaults to off, so without these two
// options the host terminal stays stuck on whatever the shell last set
// (typically "tmux"). The user reported the iTerm window showed only
// "tmux" with no way to tell which Claude session lived there; this
// surface ensures every ccmux-attached session pushes "ccmux • <label>"
// up to the terminal. Per-session via Apply so vanilla tmux sessions
// the user runs elsewhere keep their default titling.
func TestOptions_SetsTerminalTitle(t *testing.T) {
	opts := Options("c-foo", "auth-redesign", false, false, "Ctrl-b")
	asMap := map[string]string{}
	for _, kv := range opts {
		asMap[kv[0]] = kv[1]
	}
	if asMap["set-titles"] != "on" {
		t.Errorf("set-titles = %q, want on (host terminal won't update otherwise)", asMap["set-titles"])
	}
	title := asMap["set-titles-string"]
	if !strings.Contains(title, "ccmux") {
		t.Errorf("set-titles-string missing ccmux brand: %q", title)
	}
	if !strings.Contains(title, "auth-redesign") {
		t.Errorf("set-titles-string missing project label: %q", title)
	}
}

// TestOptions_TitleFallsBackToSessionWhenLabelEmpty mirrors the
// status-left fallback: a bare session with no project context should
// still surface its tmux name in the terminal title rather than just
// "ccmux • ".
func TestOptions_TitleFallsBackToSessionWhenLabelEmpty(t *testing.T) {
	opts := Options("c-foo", "", false, false, "Ctrl-b")
	for _, kv := range opts {
		if kv[0] == "set-titles-string" && !strings.Contains(kv[1], "c-foo") {
			t.Errorf("empty label: title should fall back to session name: %q", kv[1])
		}
	}
}

// TestOptions_EmitsTitleKeysForReset pins that Options emits
// set-titles + set-titles-string. Reset derives its unset list from
// Options() so the two stay in sync — but the keys must actually
// appear in Options for that derivation to do any good. Without this,
// a ccmux session killed mid-attach could leave its custom title
// pushing in place on the next vanilla `tmux attach`, and the user
// would see "ccmux • foo" in their terminal title with no ccmux
// running.
func TestOptions_EmitsTitleKeysForReset(t *testing.T) {
	opts := Options("c-foo", "p", false, false, "Ctrl-b")
	have := map[string]bool{}
	for _, kv := range opts {
		have[kv[0]] = true
	}
	for _, want := range []string{"set-titles", "set-titles-string"} {
		if !have[want] {
			t.Errorf("Options() must emit %q so Reset has it to unset", want)
		}
	}
}
