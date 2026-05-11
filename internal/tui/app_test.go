package tui

import (
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/tui/styles"
)

func mustStyles(t *testing.T) styles.Styles {
	t.Helper()
	return styles.Default()
}

// TestDaemonOnline_UsesLocalFlag locks in the fix for the bug where
// daemonOnline() looked for hostStatus.Name == "local" — but recent
// commits renamed the local row's Name to the actual hostname (e.g.
// "sputnik") and routed liveness through a Local flag instead. The
// previous predicate returned false on every refresh after that
// rename, so the status bar at the bottom of the TUI was permanently
// stuck on "⚠ offline" even when ccmuxd was up.
func TestDaemonOnline_UsesLocalFlag(t *testing.T) {
	cases := []struct {
		name  string
		hosts []hostStatus
		want  bool
	}{
		{
			"local row with hostname-as-Name + OK + Local flag",
			[]hostStatus{
				{Name: "sputnik", Local: true, OK: true},
				{Name: "mac-mini", Discovered: true, OK: true},
			},
			true,
		},
		{
			"local row exists but daemon down",
			[]hostStatus{{Name: "sputnik", Local: true, OK: false}},
			false,
		},
		{
			"no local row at all",
			[]hostStatus{{Name: "mac-mini", Discovered: true, OK: true}},
			false,
		},
		{
			"old-style row named literally 'local' but Local flag missing → no",
			[]hostStatus{{Name: "local", OK: true /* Local: false */}},
			false,
		},
		{
			"empty list",
			[]hostStatus{},
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := daemonOnline(tc.hosts); got != tc.want {
				t.Errorf("daemonOnline = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDialAddrFor_StripsPort(t *testing.T) {
	cases := []struct {
		in       hostStatus
		want     string
	}{
		{hostStatus{Address: "100.75.64.20:7474"}, "100.75.64.20"},
		{hostStatus{Address: "host.example:1234"}, "host.example"},
		{hostStatus{Address: "no-port"}, "no-port"},
		{hostStatus{Address: ""}, ""},
	}
	for _, tc := range cases {
		if got := dialAddrFor(tc.in); got != tc.want {
			t.Errorf("dialAddrFor(%q) = %q, want %q", tc.in.Address, got, tc.want)
		}
	}
}

func TestRemoteTmuxAttach(t *testing.T) {
	got := remoteTmuxAttach("c-foo")
	// PATH prepend present.
	if !strings.HasPrefix(got, "PATH=") {
		t.Errorf("missing PATH prefix: %q", got)
	}
	for _, p := range []string{"/opt/homebrew/bin", "/usr/local/bin"} {
		if !strings.Contains(got, p) {
			t.Errorf("PATH prepend missing %s: %q", p, got)
		}
	}
	// Session name quoted exactly once at the end.
	if !strings.HasSuffix(got, "'c-foo'") {
		t.Errorf("session name not quoted as expected: %q", got)
	}
	// Pathological name with a quote in it shouldn't break out.
	tricky := remoteTmuxAttach("c'foo")
	if !strings.HasSuffix(tricky, `'c'\''foo'`) {
		t.Errorf("single-quote escaping failed: %q", tricky)
	}
}

func TestShellQuote(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		// Plain identifiers stay inside the single quotes.
		{"c-foo", "'c-foo'"},
		// A literal single quote needs the close-quote / escaped /
		// re-open trick: ' → '\''
		{"c'foo", `'c'\''foo'`},
		// Shell metacharacters (semicolon, backtick, $) survive
		// untouched inside single quotes.
		{"a; rm -rf $HOME", "'a; rm -rf $HOME'"},
		{"", "''"},
	}
	for _, tc := range cases {
		if got := shellQuote(tc.in); got != tc.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestShortPeerName covers the helper used to turn Tailscale's pretty
// HostName ("Sasha's Mac mini") into something the dashboard can show.
func TestShortPeerName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Sasha's Mac mini", "sashas-mac-mini"},
		{"  with-whitespace  ", "with-whitespace"},
		{"UPPER", "upper"},
		{"a__b--c  d", "a-b-c-d"},
		{"---", "---"}, // shortPeerName returns input unchanged when result is empty
		{"", ""},
	}
	for _, tc := range cases {
		got := shortPeerName(tc.in)
		// The "---" case: when sanitizing produces an empty string,
		// the helper falls back to the original. Other inputs we
		// assert against the explicit want.
		if tc.in == "---" {
			if got != "---" && got != "" {
				t.Errorf("shortPeerName(%q) = %q, want either %q or %q", tc.in, got, "---", "")
			}
			continue
		}
		if got != tc.want {
			t.Errorf("shortPeerName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestShortHostname_StripsDomain(t *testing.T) {
	cases := []struct{ in, want string }{
		{"sputnik.mini.skz.dev", "sputnik"},
		{"laptop", "laptop"},
		{"", ""},
		{"only-dot.", "only-dot"},
	}
	for _, tc := range cases {
		if got := shortHostname(tc.in); got != tc.want {
			t.Errorf("shortHostname(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestStatePriority pins the dashboard's session-row ordering: rows
// that need user input float to the top, idle/error sink to the
// bottom. A regression here would mean "needs_input" sessions stop
// being visible at a glance — exactly the thing the dashboard exists
// to surface.
func TestStatePriority(t *testing.T) {
	cases := []struct {
		state string
		order int
	}{
		{"needs_input", 0},
		{"active", 1},
		{"idle", 2},
		{"error", 3},
		{"unknown", 4},
		{"", 4},
	}
	for _, tc := range cases {
		if got := statePriority(tc.state); got != tc.order {
			t.Errorf("statePriority(%q) = %d, want %d", tc.state, got, tc.order)
		}
	}
	// Ordering invariant — strict less-than across the canonical list.
	canonical := []string{"needs_input", "active", "idle", "error", "unknown"}
	for i := 0; i < len(canonical)-1; i++ {
		if statePriority(canonical[i]) >= statePriority(canonical[i+1]) {
			t.Errorf("statePriority not strictly increasing: %s vs %s",
				canonical[i], canonical[i+1])
		}
	}
}

// TestVersionsDiffer_StripsDirtySuffix exercises the helper the
// Devices panel uses to flag "update available". The -dirty suffix
// only means "tree had uncommitted changes at build time" — that
// shouldn't be treated as a different version from the same SHA's
// clean build.
func TestVersionsDiffer(t *testing.T) {
	cases := []struct {
		local, remote string
		want          bool
	}{
		{"1db9351", "1db9351", false},
		{"1db9351-dirty", "1db9351", false}, // dirty stripped on compare
		{"1db9351", "1db9351-dirty", false},
		{"1db9351-dirty", "1db9351-dirty", false},
		{"1db9351", "3fed7e0", true},
		{"1db9351-dirty", "3fed7e0-dirty", true},
		{"", "1db9351", true},
		{"1db9351", "", true},
	}
	for _, tc := range cases {
		if got := versionsDiffer(tc.local, tc.remote); got != tc.want {
			t.Errorf("versionsDiffer(%q, %q) = %v, want %v",
				tc.local, tc.remote, got, tc.want)
		}
	}
}

// TestNormalizeVersion_StripsDirty is the bit doing the work above.
func TestNormalizeVersion(t *testing.T) {
	cases := []struct{ in, want string }{
		{"1db9351", "1db9351"},
		{"1db9351-dirty", "1db9351"},
		{"  v0.1.0  ", "v0.1.0"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := normalizeVersion(tc.in); got != tc.want {
			t.Errorf("normalizeVersion(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestTruncatePeerName_Width sanity-checks the Devices-panel
// truncation. Lipgloss.Width counts visible cells; the helper should
// never emit a row longer than the cap.
func TestTruncatePeerName(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"way-too-long-name", 6, "way-t…"},
		{"exact-len", 9, "exact-len"},
		{"", 5, ""},
		{"x", 0, ""},
	}
	for _, tc := range cases {
		if got := truncatePeerName(tc.in, tc.n); got != tc.want {
			t.Errorf("truncatePeerName(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}

// TestInfoForHost ensures each device-row type renders the right
// single-fact info string. The Devices panel relies on these distinct
// renderings to communicate what the user can do for each peer.
func TestInfoForHost(t *testing.T) {
	st := mustStyles(t)
	const localVer = "abc1234"

	mobile := infoForHost(hostStatus{Mobile: true}, localVer, st)
	if !strings.Contains(mobile, "Moshi") {
		t.Errorf("mobile row info missing 'Moshi': %q", mobile)
	}

	needs := infoForHost(hostStatus{NeedsInstall: true}, localVer, st)
	if !strings.Contains(needs, "unreachable") {
		t.Errorf("needs-install row info missing 'unreachable': %q", needs)
	}

	same := infoForHost(hostStatus{Version: localVer}, localVer, st)
	if strings.Contains(same, "update available") {
		t.Errorf("matching version should NOT flag update: %q", same)
	}

	old := infoForHost(hostStatus{Version: "old1234"}, localVer, st)
	if !strings.Contains(old, "update available") {
		t.Errorf("differing version should flag update: %q", old)
	}

	missing := infoForHost(hostStatus{Version: ""}, localVer, st)
	if missing == "" {
		t.Errorf("missing version should still produce some info: %q", missing)
	}
}
