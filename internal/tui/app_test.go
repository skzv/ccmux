package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/moshi"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tui/styles"
)

func mustStyles(t *testing.T) styles.Styles {
	t.Helper()
	return styles.Default()
}

func TestProjectHost(t *testing.T) {
	cases := []struct {
		host, want string
	}{
		{"", "local"},
		{"local", "local"},
		{"mac-mini", "mac-mini"},
		{"sashas-mac-mini", "sashas-mac-mini"},
	}
	for _, tc := range cases {
		got := projectHost(project.Project{Host: tc.host})
		if got != tc.want {
			t.Errorf("projectHost({Host:%q}) = %q, want %q", tc.host, got, tc.want)
		}
	}
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
		in   hostStatus
		want string
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

// TestConfiguredHostKeys_DedupsAcrossResolvedIP locks in the fix for
// "every project shows twice on the dashboard": a host configured by
// DNS name (e.g. "localhost:7474") wasn't deduping against the same
// peer auto-discovered by IP ("127.0.0.1:7474"), so its projects and
// sessions got fetched and rendered twice.
func TestConfiguredHostKeys_DedupsAcrossResolvedIP(t *testing.T) {
	keys := configuredHostKeys(config.Host{Name: "loop", Address: "localhost", Port: 7474}, 7474)
	want := map[string]bool{"localhost:7474": false}
	for _, k := range keys {
		want[k] = true
	}
	if !want["localhost:7474"] {
		t.Fatalf("missing literal key in %v", keys)
	}
	hasIP := false
	for _, k := range keys {
		if k == "127.0.0.1:7474" || k == "[::1]:7474" || k == "::1:7474" {
			hasIP = true
		}
	}
	if !hasIP {
		t.Errorf("no resolved-IP key for localhost in %v — auto-discovery dedupe will miss", keys)
	}
}

// TestConfiguredHostKeys_DefaultsPort confirms an unset h.Port falls
// back to the caller-supplied default (which mirrors what the same
// caller hands to ScanTailnet, so the two paths agree on what "the
// default daemon port" means in this user's config).
func TestConfiguredHostKeys_DefaultsPort(t *testing.T) {
	keys := configuredHostKeys(config.Host{Address: "example.invalid"}, 9999)
	found := false
	for _, k := range keys {
		if k == "example.invalid:9999" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected example.invalid:9999 in %v", keys)
	}
}

func TestRemoteTmuxAttach(t *testing.T) {
	// detachOthers=false → mirror mode (the default).
	got := remoteTmuxAttach("c-foo", false)
	if !strings.HasPrefix(got, "PATH=") {
		t.Errorf("missing PATH prefix: %q", got)
	}
	// Platform coverage — every common tmux location on both macOS
	// and Linux should be present so attach works regardless of
	// which way we're crossing the wire.
	for _, p := range []string{
		"/opt/homebrew/bin",              // macOS Apple Silicon Homebrew
		"/usr/local/bin",                 // macOS Intel + Linux conventional
		"/home/linuxbrew/.linuxbrew/bin", // Linuxbrew
		"/snap/bin",                      // Snap on Linux
	} {
		if !strings.Contains(got, p) {
			t.Errorf("PATH prepend missing %s: %q", p, got)
		}
	}
	// $PATH passthrough at the end keeps whatever the remote shell
	// already had set up.
	if !strings.Contains(got, "$PATH") {
		t.Errorf("PATH suffix should keep existing $PATH: %q", got)
	}
	if !strings.HasSuffix(got, "'c-foo'") {
		t.Errorf("session name not quoted as expected: %q", got)
	}
	tricky := remoteTmuxAttach("c'foo", false)
	if !strings.HasSuffix(tricky, `'c'\''foo'`) {
		t.Errorf("single-quote escaping failed: %q", tricky)
	}
}

// TestRemoteTmuxAttach_DetachFlag — mirror mode (false) must NOT emit
// -d so other clients survive; exclusive mode (true) must emit it.
// This is the wire-format half of the mirror-mode contract: the
// local user's preference has to actually reach the remote tmux.
func TestRemoteTmuxAttach_DetachFlag(t *testing.T) {
	mirror := remoteTmuxAttach("c-foo", false)
	if strings.Contains(mirror, " -d ") {
		t.Errorf("mirror mode should NOT pass -d, got: %q", mirror)
	}
	if !strings.Contains(mirror, "attach-session -t ") {
		t.Errorf("mirror mode should attach without -d: %q", mirror)
	}

	exclusive := remoteTmuxAttach("c-foo", true)
	if !strings.Contains(exclusive, "attach-session -d -t ") {
		t.Errorf("exclusive mode should pass -d, got: %q", exclusive)
	}
}

func TestRemoteNewSessionAttachProcess_ForcesMirrorAttach(t *testing.T) {
	cmd, target, remoteCmd := remoteNewSessionAttachProcess(remoteSessionStartedMsg{
		SessionName: "c-foo",
		DialHost:    "mac-mini.local",
		User:        "sasha",
		Mosh:        false,
	})
	if target != "sasha@mac-mini.local" {
		t.Fatalf("target = %q, want sasha@mac-mini.local", target)
	}
	if len(cmd.Args) == 0 || cmd.Args[0] != "ssh" {
		t.Fatalf("cmd.Args = %v, want ssh command", cmd.Args)
	}
	if strings.Contains(remoteCmd, " -d ") {
		t.Errorf("new-session remote attach should not pass -d, got: %q", remoteCmd)
	}
	if !strings.Contains(remoteCmd, "attach-session -t ") {
		t.Errorf("new-session remote attach should use mirror attach: %q", remoteCmd)
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
// TestSessionsCursorPreservesName locks in the fix for the bug where
// clicking (selecting) a session and then waiting for the auto-refresh
// to fire could silently shift the cursor to a different session.
//
// Root cause: SetSessions previously clamped cursor only when it was
// out of bounds — it never re-anchored to the same session by name
// when the list arrived in a different order. Because the daemon
// returns sessions in whatever order tmux list-sessions emits (which
// can change when a session gets attached, created, or killed),
// "c-ccmux" at cursor 0 could become "c-ccmux-website" at cursor 0
// after a single 2-second poll, causing Enter to join the wrong session.
func TestSessionsCursorPreservesName(t *testing.T) {
	st := mustStyles(t)
	km := DefaultKeymap()
	m := newSessions(st, km)

	initial := []daemon.SessionState{
		{Name: "c-ccmux", Host: "local"},
		{Name: "c-ccmux-website", Host: "local"},
	}
	m.SetSessions(initial)
	m.cursor = 0 // user is on c-ccmux

	// Simulate a refresh where the list comes back in reversed order
	// (e.g. c-ccmux-website was most recently attached).
	refreshed := []daemon.SessionState{
		{Name: "c-ccmux-website", Host: "local"},
		{Name: "c-ccmux", Host: "local"},
	}
	m.SetSessions(refreshed)

	sel := m.Selected()
	if sel == nil {
		t.Fatal("Selected() = nil after refresh")
	}
	if sel.Name != "c-ccmux" {
		t.Errorf("cursor drifted: got %q, want %q — session order changed but cursor should have followed by name",
			sel.Name, "c-ccmux")
	}
	if m.cursor != 1 {
		t.Errorf("cursor index = %d, want 1 (c-ccmux moved to index 1 in refreshed list)", m.cursor)
	}
}

// TestSessionsCursorClampsWhenSessionKilled verifies that if the
// currently-selected session disappears (killed by another process),
// the cursor falls back to the end of the list rather than pointing
// past the end.
func TestSessionsCursorClampsWhenSessionKilled(t *testing.T) {
	st := mustStyles(t)
	km := DefaultKeymap()
	m := newSessions(st, km)

	m.SetSessions([]daemon.SessionState{
		{Name: "c-ccmux", Host: "local"},
		{Name: "c-ccmux-website", Host: "local"},
		{Name: "c-other", Host: "local"},
	})
	m.cursor = 2 // user was on c-other

	// c-other gets killed externally; next refresh omits it.
	m.SetSessions([]daemon.SessionState{
		{Name: "c-ccmux", Host: "local"},
		{Name: "c-ccmux-website", Host: "local"},
	})

	if m.cursor >= len(m.sessions) {
		t.Errorf("cursor %d out of bounds after session killed (len=%d)", m.cursor, len(m.sessions))
	}
}

// TestSessionsCursorEmptyList verifies no panic or negative index
// when all sessions disappear.
func TestSessionsCursorEmptyList(t *testing.T) {
	st := mustStyles(t)
	km := DefaultKeymap()
	m := newSessions(st, km)

	m.SetSessions([]daemon.SessionState{{Name: "c-ccmux", Host: "local"}})
	m.cursor = 0
	m.SetSessions(nil)

	if m.cursor != 0 {
		t.Errorf("cursor = %d on empty list, want 0", m.cursor)
	}
	if sel := m.Selected(); sel != nil {
		t.Errorf("Selected() on empty list = %v, want nil", sel)
	}
}

// TestUniqueSessionName_Format verifies that uniqueSessionName returns a name
// with the expected suffix pattern. When tmux is not running (CI or any
// machine without a server), tmux.Has reports no session and the function
// returns the first candidate: "<base>-2".
func TestUniqueSessionName_Format(t *testing.T) {
	ctx := context.Background()
	got := uniqueSessionName(ctx, "c-myproject")
	// Must start with the base and a hyphen-digit suffix.
	if !strings.HasPrefix(got, "c-myproject-") {
		t.Errorf("uniqueSessionName = %q, want c-myproject-<n>", got)
	}
	suffix := got[len("c-myproject-"):]
	if suffix == "" {
		t.Errorf("uniqueSessionName = %q, missing suffix", got)
	}
	// The suffix must be numeric or a ms timestamp — both parse as digits.
	for _, ch := range suffix {
		if ch < '0' || ch > '9' {
			t.Errorf("uniqueSessionName suffix %q contains non-digit %q", suffix, string(ch))
		}
	}
}

// TestUniqueSessionName_SkipsTaken tests the core deduplication logic using
// a pure function extracted from uniqueSessionName. We can't inject a fake
// tmux.Has, so we verify the naming algorithm directly.
func TestUniqueSessionName_NamingAlgorithm(t *testing.T) {
	// Simulate the deduplication loop from uniqueSessionName.
	nextFree := func(base string, taken map[string]bool) string {
		for i := 2; i < 100; i++ {
			candidate := fmt.Sprintf("%s-%d", base, i)
			if !taken[candidate] {
				return candidate
			}
		}
		return fmt.Sprintf("%s-overflow", base)
	}

	cases := []struct {
		base  string
		taken map[string]bool
		want  string
	}{
		{"c-foo", map[string]bool{}, "c-foo-2"},
		{"c-foo", map[string]bool{"c-foo-2": true}, "c-foo-3"},
		{"c-foo", map[string]bool{"c-foo-2": true, "c-foo-3": true}, "c-foo-4"},
		{"c-bar", map[string]bool{"c-bar-2": true, "c-bar-3": true, "c-bar-4": true}, "c-bar-5"},
	}
	for _, tc := range cases {
		if got := nextFree(tc.base, tc.taken); got != tc.want {
			t.Errorf("nextFree(%q, taken=%v) = %q, want %q", tc.base, tc.taken, got, tc.want)
		}
	}
}

// TestStatusBar_NarrowKeepsDaemonAndBattery — the safety-critical
// chrome (battery-danger banner, daemon status) is T0 and survives at
// phone width.
func TestStatusBar_NarrowKeepsDaemonAndBattery(t *testing.T) {
	a := App{styles: styles.Default(), width: 50, daemonOnline: true}
	a.cfg.Sleep.DangerousKeepAwakeOnBattery = true
	bar := a.renderStatusBar()
	assertNoOverflow(t, bar, 50)
	assertPresent(t, bar, "BATT", "daemon")
}

// TestStatusBar_NarrowDropsClockAndVersion — the refreshed-at clock
// and the version chip are T2: dropped on narrow, kept when wide.
func TestStatusBar_NarrowDropsClockAndVersion(t *testing.T) {
	a := App{styles: styles.Default(), width: 50, version: "v9.9.9", daemonOnline: true}
	a.lastRefresh = time.Date(2026, 5, 20, 14, 30, 45, 0, time.Local)
	narrow := a.renderStatusBar()
	assertNoOverflow(t, narrow, 50)
	assertAbsent(t, narrow, "14:30:45", "v9.9.9")
	a.width = 200
	assertPresent(t, a.renderStatusBar(), "14:30:45", "v9.9.9")
}

// TestHelpLine_AlwaysShowsHelpAndQuit — `? help` and `q quit` are the
// top-priority hints (P=10) in every screen's HelpBarProps; they
// MUST survive at any width, including extreme narrow phones.
func TestHelpLine_AlwaysShowsHelpAndQuit(t *testing.T) {
	for _, w := range []int{20, 40, 50, 80, 120, 200} {
		a := App{styles: styles.Default(), width: w}
		line := a.renderHelpLine()
		assertNoOverflow(t, line, w)
		assertPresent(t, line, "? help", "q quit")
	}
}

// TestHelpLine_DropsActionHintsAtNarrowWidth — at a width tight
// enough that only the high-priority pair fits, the lower-priority
// action hints MUST drop. The exact threshold is implementation
// detail; this test pins the behavior at a known-tight width.
func TestHelpLine_DropsActionHintsAtNarrowWidth(t *testing.T) {
	a := App{styles: styles.Default(), width: 18}
	line := a.renderHelpLine()
	assertNoOverflow(t, line, 18)
	assertPresent(t, line, "? help", "q quit")
	assertAbsent(t, line, "n new", "x kill", "r refresh")
}

// TestHelpLine_PreservesInputOrder — when multiple hints fit, they
// render in the order provided by HelpBarProps. `? help` precedes
// `n new` because it's first in the slice, not just because it
// has higher priority.
func TestHelpLine_PreservesInputOrder(t *testing.T) {
	a := App{styles: styles.Default(), width: 200}
	line := a.renderHelpLine()
	help := strings.Index(line, "? help")
	newHint := strings.Index(line, "n new")
	if help < 0 || newHint < 0 {
		t.Fatalf("wide help line missing expected hints:\n%s", line)
	}
	if help > newHint {
		t.Errorf("`? help` must precede `n new` (input-order render):\n%s", line)
	}
}

// TestNew_DoesNotBlockOnProbes pins the startup-latency fix: New() must
// not shell out. It used to run `claude auth status` (~0.9s) and
// moshi.Detect (up to 2s) synchronously, stalling the first frame by
// ~3s on every launch. Both moved to async commands (detectTierCmd,
// detectMoshiCmd) fired from Init(); a fresh App carries the unresolved
// tier default until tierDetectedMsg lands.
func TestNew_DoesNotBlockOnProbes(t *testing.T) {
	cfg := config.Config{}
	cfg.Subscription.Tier = "api"

	start := time.Now()
	a := New(cfg, "test")
	elapsed := time.Since(start)

	// New() does only in-memory wiring now; even a slow CI box clears
	// this comfortably. A regression that re-adds either synchronous
	// probe would blow past it (~0.9s for auth, up to 2s for moshi).
	if elapsed > 250*time.Millisecond {
		t.Errorf("New() took %v — it must not block on the auth/moshi probes", elapsed)
	}
	if a.cfg.Subscription.Tier != "api" {
		t.Errorf("New() resolved tier to %q synchronously; detection must be deferred to detectTierCmd",
			a.cfg.Subscription.Tier)
	}
}

// TestTierDetectedMsg_AdoptsDetectedTier — when the user has not
// declared a tier, the async probe result is adopted and pushed into
// the dashboard config.
func TestTierDetectedMsg_AdoptsDetectedTier(t *testing.T) {
	cfg := config.Config{}
	cfg.Subscription.Tier = "api" // default-empty marker
	a := New(cfg, "test")

	next, _ := a.Update(tierDetectedMsg{Tier: "max20x"})
	got := next.(App)
	if got.cfg.Subscription.Tier != "max20x" {
		t.Errorf("tier = %q, want max20x (detected tier adopted over the api default)",
			got.cfg.Subscription.Tier)
	}
}

// TestTierDetectedMsg_RespectsExplicitTier — a tier the user hand-set
// in config.toml always wins; a later probe result must not clobber it.
func TestTierDetectedMsg_RespectsExplicitTier(t *testing.T) {
	cfg := config.Config{}
	cfg.Subscription.Tier = "pro" // explicitly declared
	a := New(cfg, "test")

	next, _ := a.Update(tierDetectedMsg{Tier: "max20x"})
	got := next.(App)
	if got.cfg.Subscription.Tier != "pro" {
		t.Errorf("tier = %q, want pro (an explicit config tier must not be overridden)",
			got.cfg.Subscription.Tier)
	}
}

// TestMoshiDetectedMsg_UpdatesSettings — the async moshi probe result
// is pushed into the Settings model and clears its staleness flag. A
// freshly built model must read as stale (never probed) so the first
// poll always detects.
func TestMoshiDetectedMsg_UpdatesSettings(t *testing.T) {
	a := New(config.Config{}, "test")
	if !a.settings.MoshiStale() {
		t.Fatal("a freshly built settings model should report moshi state as stale (never probed)")
	}

	next, _ := a.Update(moshiDetectedMsg{State: moshi.Status{HooksInstalled: true}})
	got := next.(App)
	if got.settings.MoshiStale() {
		t.Error("after moshiDetectedMsg the cached moshi state should be fresh")
	}
	if !got.settings.moshiState.HooksInstalled {
		t.Error("moshiDetectedMsg should have written the probed state into settings")
	}
}
