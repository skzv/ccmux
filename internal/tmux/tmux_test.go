package tmux

import (
	"strings"
	"testing"
	"time"
)

func TestWithLocale(t *testing.T) {
	cases := []struct {
		name      string
		env       []string
		wantAdded bool
	}{
		{
			name:      "empty env adds LC_ALL=C.UTF-8 (the launchd/systemd case)",
			env:       nil,
			wantAdded: true,
		},
		{
			name:      "no locale vars, other vars present",
			env:       []string{"HOME=/x", "PATH=/usr/bin"},
			wantAdded: true,
		},
		{
			name:      "LANG already set — leave it alone",
			env:       []string{"LANG=en_US.UTF-8"},
			wantAdded: false,
		},
		{
			name:      "LC_ALL already set — leave it alone",
			env:       []string{"LC_ALL=C"},
			wantAdded: false,
		},
		{
			name:      "LC_CTYPE alone is enough",
			env:       []string{"LC_CTYPE=en_US.UTF-8"},
			wantAdded: false,
		},
		{
			name:      "var only superficially starts with LC_ — must not match",
			env:       []string{"LC_TOWN=Tokyo"},
			wantAdded: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := withLocale(tc.env)
			added := containsLC_ALL_CUTF8(got) && !containsLC_ALL_CUTF8(tc.env)
			if added != tc.wantAdded {
				t.Fatalf("withLocale(%v) added=%v, want %v (result=%v)", tc.env, added, tc.wantAdded, got)
			}
			// Caller must always receive at least one entry that pins
			// the locale, whether we added it or it was inherited.
			if !hasAnyLocale(got) {
				t.Fatalf("withLocale(%v) returned env with no locale at all: %v", tc.env, got)
			}
		})
	}
}

// TestWithLocale_DoesNotMutateInput ensures we never modify the caller's
// slice — Go's append may or may not reuse the backing array depending on
// capacity, and silent mutation of os.Environ() would be surprising.
func TestWithLocale_DoesNotMutateInput(t *testing.T) {
	in := []string{"HOME=/x"}
	_ = withLocale(in)
	if len(in) != 1 || in[0] != "HOME=/x" {
		t.Fatalf("withLocale mutated input slice: %v", in)
	}
}

func containsLC_ALL_CUTF8(env []string) bool {
	for _, e := range env {
		if e == "LC_ALL=C.UTF-8" {
			return true
		}
	}
	return false
}

func hasAnyLocale(env []string) bool {
	for _, e := range env {
		if strings.HasPrefix(e, "LC_ALL=") || strings.HasPrefix(e, "LC_CTYPE=") || strings.HasPrefix(e, "LANG=") {
			return true
		}
	}
	return false
}

func TestParseList_HappyPath(t *testing.T) {
	// Exactly the format `tmux list-sessions -F listFormat` produces in
	// a UTF-8 locale — tabs between fields, newline between rows. Field
	// order: name, created, activity, attached, windows, path (path last).
	raw := []byte(strings.Join([]string{
		"0\t1778364516\t1778367593\t0\t1\t/Users/skz/src",
		"c-foo\t1778469669\t1778523925\t1\t2\t/Users/skz/Projects/foo",
		"",
	}, "\n"))
	got := parseList(raw)
	if len(got) != 2 {
		t.Fatalf("parseList returned %d sessions, want 2", len(got))
	}
	if got[0].Name != "0" || got[0].Attached || got[0].Windows != 1 || got[0].Path != "/Users/skz/src" {
		t.Errorf("session 0: %+v", got[0])
	}
	if got[1].Name != "c-foo" || !got[1].Attached || got[1].Windows != 2 || got[1].Path != "/Users/skz/Projects/foo" {
		t.Errorf("session c-foo: %+v", got[1])
	}
	wantCreated := time.Unix(1778364516, 0)
	if !got[0].Created.Equal(wantCreated) {
		t.Errorf("session 0 created = %v, want %v", got[0].Created, wantCreated)
	}
}

func TestParseList_Empty(t *testing.T) {
	if got := parseList(nil); len(got) != 0 {
		t.Fatalf("empty input returned %v", got)
	}
	if got := parseList([]byte("")); len(got) != 0 {
		t.Fatalf("empty string returned %v", got)
	}
	if got := parseList([]byte("\n\n\n")); len(got) != 0 {
		t.Fatalf("whitespace-only returned %v", got)
	}
}

func TestParseList_TooFewFieldsSkipped(t *testing.T) {
	raw := []byte("not-enough\tfields\n" +
		"ok\t1\t2\t0\t1\t/path\n")
	got := parseList(raw)
	if len(got) != 1 || got[0].Name != "ok" {
		t.Fatalf("expected only the well-formed row, got %v", got)
	}
}

// TestParseList_TabInPath — a directory path containing a tab must not
// shift the attached/windows columns. session_path is the last field
// and parseList uses SplitN, so the path absorbs the embedded tab
// rather than spilling into a phantom column (which previously
// mis-parsed attached/windows).
func TestParseList_TabInPath(t *testing.T) {
	raw := []byte("c-weird\t100\t200\t1\t3\t/Users/skz/od\td name\n")
	got := parseList(raw)
	if len(got) != 1 {
		t.Fatalf("expected 1 session, got %d", len(got))
	}
	if got[0].Path != "/Users/skz/od\td name" {
		t.Errorf("path with tab not preserved: %q", got[0].Path)
	}
	if !got[0].Attached || got[0].Windows != 3 {
		t.Errorf("tab in path shifted columns: attached=%v windows=%d", got[0].Attached, got[0].Windows)
	}
}

// TestParseList_CLocaleCorruption_RegressionDocumentsBug locks in the
// behavior of the parser when tmux output has been corrupted by a C
// locale — tabs replaced with `_`. The expectation is that the parser
// rejects every row (returns 0 sessions), which is what made the bug
// invisible (TUI showed "no sessions"). The real fix lives in
// withLocale() / command(); this test only proves the parser doesn't
// silently accept the corrupted shape.
func TestParseList_CLocaleCorruption_RegressionDocumentsBug(t *testing.T) {
	raw := []byte(strings.Join([]string{
		"0_1778364516_1778367593_/Users/skz/src_0_1",
		"c-foo_1778469669_1778523925_/Users/skz/Projects/foo_1_2",
	}, "\n"))
	got := parseList(raw)
	if len(got) != 0 {
		t.Fatalf("parser accepted C-locale-corrupted rows: got %d sessions, expected 0", len(got))
	}
}

func TestClientTTYs_FiltersEmptyUnsafeAndDuplicateRows(t *testing.T) {
	raw := []byte(strings.Join([]string{
		"/dev/ttys001",
		"",
		"not-a-device",
		"/tmp/fake",
		"/dev/ttys001",
		" /dev/pts/4 ",
	}, "\n"))
	got := clientTTYs(raw)
	want := []string{"/dev/ttys001", "/dev/pts/4"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("clientTTYs = %#v, want %#v", got, want)
	}
}

func TestSessionNameForPath(t *testing.T) {
	cases := []struct{ in, want string }{
		// Original cases — `.` → `_` substitution is preserved.
		{"/Users/skz/Projects/foo", "c-foo"},
		{"/Users/skz/Projects/foo/", "c-foo"},
		{"/Users/skz/Projects/with.dots", "c-with_dots"},
		{"/Users/skz/Projects/a.b.c", "c-a_b_c"},
		{"foo", "c-foo"},
		{"/", "c-"},

		// Broader sanitization added after fuzz uncovered the `:` case
		// — tmux's `-t` target parser treats `:` as the session/window
		// separator, so a name like `c-a:b` would route to window `b`
		// of session `c-a`. Anything outside [a-zA-Z0-9_-] becomes `_`.
		{"a:b", "c-a_b"},
		{"a b c", "c-a_b_c"},
		{"a\nb", "c-a_b"},
		{"a\x00b", "c-a_b"},
		{"name-with-dashes", "c-name-with-dashes"},
		{"under_score", "c-under_score"},
	}
	for _, tc := range cases {
		if got := SessionNameForPath(tc.in); got != tc.want {
			t.Errorf("SessionNameForPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestUnixSecsAndAtoi(t *testing.T) {
	if !unixSecs("0").IsZero() {
		t.Error("unixSecs(\"0\") should be zero time")
	}
	if got := unixSecs("1778364516"); got.Unix() != 1778364516 {
		t.Errorf("unixSecs(\"1778364516\").Unix() = %d", got.Unix())
	}
	if atoi("42xyz") != 42 {
		t.Error("atoi should stop at first non-digit")
	}
	if atoi("") != 0 {
		t.Error("atoi(empty) should be 0")
	}
	if atoi("abc") != 0 {
		t.Error("atoi of non-numeric should be 0")
	}
}

func TestListFormatStaysAlignedWithParser(t *testing.T) {
	// Defensive: the parser hard-codes 6 fields by index. If anyone
	// changes listFormat without updating parseList (or vice versa),
	// this test catches it.
	wantFields := 6
	gotFields := strings.Count(listFormat, "\t") + 1
	if gotFields != wantFields {
		t.Fatalf("listFormat has %d fields, parser expects %d — keep them in sync", gotFields, wantFields)
	}
}

// TestAttachArgs_MirrorVsExclusive pins the -d flag decision — the
// single most load-bearing bit of mirror mode. Mirror (detachOthers=
// false) must omit -d so other clients survive the attach; exclusive
// (true) must include it so the session resizes cleanly to this
// terminal. A regression either way silently breaks the feature:
// a stray -d in mirror mode kicks the user's other device; a missing
// -d in exclusive mode leaves the window stuck at smallest-client.
func TestAttachArgs_MirrorVsExclusive(t *testing.T) {
	mirror := AttachArgs("c-foo", false)
	if got := strings.Join(mirror, " "); got != "attach-session -t c-foo" {
		t.Errorf("mirror AttachArgs = %q, want 'attach-session -t c-foo' (no -d)", got)
	}
	for _, a := range mirror {
		if a == "-d" {
			t.Errorf("mirror mode must not include -d, got %v", mirror)
		}
	}

	exclusive := AttachArgs("c-foo", true)
	if got := strings.Join(exclusive, " "); got != "attach-session -d -t c-foo" {
		t.Errorf("exclusive AttachArgs = %q, want 'attach-session -d -t c-foo'", got)
	}
}

// TestAttachArgs_SessionNameWithSpecials — the session name is the
// last argv element, passed as-is (exec, not a shell), so no quoting
// is needed; but the name must land intact even with characters that
// would matter in a shell context.
func TestAttachArgs_SessionNameWithSpecials(t *testing.T) {
	for _, name := range []string{"c-foo", "c-a.b.c", "c-resume-3dc0131a"} {
		got := AttachArgs(name, false)
		if got[len(got)-1] != name {
			t.Errorf("AttachArgs(%q): last arg = %q, want the name verbatim", name, got[len(got)-1])
		}
	}
}

// TestPaneTitle_MissingSessionIsEmpty — `tmux display-message` errors
// when the target session/pane doesn't exist. PaneTitle deliberately
// swallows that error and returns "" so the poll loop's title signal
// never aborts a tick on a session that vanished between List and
// PaneTitle. Verified against a known-bad session name that no tmux
// server should be serving.
func TestPaneTitle_MissingSessionIsEmpty(t *testing.T) {
	// Use a name that includes the per-test PID + nanos so even a
	// developer with an exotic real session can't accidentally collide.
	bogus := "ccmux-paneTitle-bogus-DOES-NOT-EXIST"
	got, err := PaneTitle(t.Context(), bogus)
	if err != nil {
		t.Errorf("PaneTitle on a missing session should not return an error, got: %v", err)
	}
	if got != "" {
		t.Errorf("missing session should return empty string, got: %q", got)
	}
}
