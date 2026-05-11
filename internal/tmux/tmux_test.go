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
	// a UTF-8 locale — tabs between fields, newline between rows.
	raw := []byte(strings.Join([]string{
		"0\t1778364516\t1778367593\t/Users/skz/src\t0\t1",
		"c-foo\t1778469669\t1778523925\t/Users/skz/Projects/foo\t1\t2",
		"",
	}, "\n"))
	got := parseList(raw)
	if len(got) != 2 {
		t.Fatalf("parseList returned %d sessions, want 2", len(got))
	}
	if got[0].Name != "0" || got[0].Attached || got[0].Windows != 1 {
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
		"ok\t1\t2\t/path\t0\t1\n")
	got := parseList(raw)
	if len(got) != 1 || got[0].Name != "ok" {
		t.Fatalf("expected only the well-formed row, got %v", got)
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

func TestSessionNameForPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/Users/skz/Projects/foo", "c-foo"},
		{"/Users/skz/Projects/foo/", "c-foo"},
		{"/Users/skz/Projects/with.dots", "c-with_dots"},
		{"/Users/skz/Projects/a.b.c", "c-a_b_c"},
		{"foo", "c-foo"},
		{"/", "c-"},
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
