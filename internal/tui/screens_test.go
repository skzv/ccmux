package tui

import (
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/tui/styles"
)

// TestAllScreens_CoversEveryEnumValue — allScreens() must return
// exactly screenCount screens, each Screen value once, in enum order.
// This is the linchpin of the drift-proofing: renderHeader iterates
// allScreens(), so if a new screen is added to the const block but
// allScreens() somehow doesn't include it, this trips.
func TestAllScreens_CoversEveryEnumValue(t *testing.T) {
	got := allScreens()
	if len(got) != int(screenCount) {
		t.Fatalf("allScreens() len = %d, want %d (screenCount)", len(got), int(screenCount))
	}
	for i, s := range got {
		if int(s) != i {
			t.Errorf("allScreens()[%d] = %d, want %d (must be in enum order)", i, int(s), i)
		}
	}
}

// TestScreenLabels_CoverEveryScreen — every Screen must have a non-
// empty label and String() must never return the "?" fallback for a
// real screen. A new screen added without a screenLabels entry would
// render as an empty string in the tab bar — caught here.
func TestScreenLabels_CoverEveryScreen(t *testing.T) {
	for _, s := range allScreens() {
		label := s.String()
		if label == "" {
			t.Errorf("Screen(%d) has an empty label", int(s))
		}
		if label == "?" {
			t.Errorf("Screen(%d).String() = %q — missing screenLabels entry", int(s), label)
		}
	}
	// Out-of-range values get the "?" sentinel rather than panicking.
	if got := Screen(-1).String(); got != "?" {
		t.Errorf("Screen(-1).String() = %q, want ? (bounds guard)", got)
	}
	if got := Screen(screenCount).String(); got != "?" {
		t.Errorf("Screen(screenCount).String() = %q, want ? (bounds guard)", got)
	}
}

// TestRenderHeader_ShowsEveryScreen is the regression test for the
// reported bug: the Conversations tab was missing from the top bar
// because renderHeader had a hardcoded slice that wasn't updated when
// ScreenConversations was added.
//
// This asserts EVERY screen's label appears in the rendered header.
// Adding a new screen without it surfacing in the bar now fails here
// — the bug class is closed.
func TestRenderHeader_ShowsEveryScreen(t *testing.T) {
	a := App{
		styles: styles.Default(),
		keys:   DefaultKeymap(),
		width:  200, // wide enough that labels aren't collapsed to numbers
		screen: ScreenSessions,
	}
	header := a.renderHeader()
	for _, s := range allScreens() {
		if !strings.Contains(header, s.String()) {
			t.Errorf("renderHeader() is missing the %q tab:\n%s", s.String(), header)
		}
	}
}

// TestRenderHeader_NumbersMatchKeymap — the number shown on each tab
// must equal the number key that switches to it. The keymap binds
// Dashboard→1 … Network→8; the header derives its numbers from
// int(Screen)+1. Both share the enum order, so they must agree.
// A mismatch would mean the bar says "[3] Conversations" while
// pressing 3 lands somewhere else.
func TestRenderHeader_NumbersMatchKeymap(t *testing.T) {
	a := App{
		styles: styles.Default(),
		keys:   DefaultKeymap(),
		width:  200,
		screen: ScreenSessions,
	}
	header := a.renderHeader()
	// Each screen's tab should render as "[N] Label" with N = enum+1.
	for _, s := range allScreens() {
		want := "[" + itoaTest(int(s)+1) + "] " + s.String()
		if !strings.Contains(header, want) {
			t.Errorf("header missing %q (number must equal the keymap binding):\n%s", want, header)
		}
	}
}

// TestRenderHeader_NarrowCollapsesToNumbers — on a sub-tabBarMinWidth
// terminal the strip collapses to numbers so it never wraps. The
// active tab still shows its initial. This pins that the narrow path
// also iterates every screen (a hardcoded narrow-mode slice would be
// the same bug in a different spot).
//
// The threshold is computed from the actual label lengths
// (tabBarMinWidth) — exactly the cols at which all the
// `[N] ScreenName` segments would still fit — so a future screen
// rename auto-adjusts both the production code and this test.
func TestRenderHeader_NarrowCollapsesToNumbers(t *testing.T) {
	min := tabBarMinWidth()
	// Anything below the computed threshold should collapse to
	// numbers. Two sub-threshold samples + the boundary - 1.
	for _, w := range []int{60, 80, min - 1} {
		a := App{
			styles: styles.Default(),
			keys:   DefaultKeymap(),
			width:  w,
			screen: ScreenConversations,
		}
		header := a.renderHeader()
		for _, s := range allScreens() {
			num := itoaTest(int(s) + 1)
			if !strings.Contains(header, num) {
				t.Errorf("width %d: narrow header missing number %q for screen %q:\n%s", w, num, s.String(), header)
			}
		}
		// The active screen (Conversations) shows its initial "C".
		if !strings.Contains(header, "C") {
			t.Errorf("width %d: narrow header should show the active screen's initial:\n%s", w, header)
		}
		// Full labels must NOT appear in narrow mode.
		if strings.Contains(header, "Conversations") {
			t.Errorf("width %d: narrow header should collapse to numbers, not labels:\n%s", w, header)
		}
	}
	// At the breakpoint and above, the full labels return.
	a := App{styles: styles.Default(), keys: DefaultKeymap(), width: min, screen: ScreenConversations}
	if header := a.renderHeader(); !strings.Contains(header, "Conversations") {
		t.Errorf("width %d: header should show full screen labels:\n%s", min, header)
	}
}

// TestTabBarMinWidth_PinsExpectedValue locks in the computed
// threshold so an accidental rename that shrinks a screen label
// doesn't quietly shift the boundary. The arithmetic mirrors the
// helper exactly; if you rename a screen, update both sides.
func TestTabBarMinWidth_PinsExpectedValue(t *testing.T) {
	// " ccmux " + " [N] Name " for each of the 7 current screens.
	want := len(" ccmux ") +
		len(" [1] Sessions ") +
		len(" [2] Projects ") +
		len(" [3] Conversations ") +
		len(" [4] Notes ") +
		len(" [5] Agents ") +
		len(" [6] Settings ") +
		len(" [7] Network ")
	if got := tabBarMinWidth(); got != want {
		t.Errorf("tabBarMinWidth = %d, want %d (rename a screen → update one of the sides)", got, want)
	}
}

// itoaTest is a tiny int→string for the test — strconv.Itoa would do
// but keeping the import surface minimal in this file. Only ever
// called with single-digit screen numbers.
func itoaTest(n int) string {
	if n < 0 || n > 9 {
		return ""
	}
	return string(rune('0' + n))
}

// TestHomeView_NarrowSingleColumn — below the breakpoint the Home
// screen is a single full-width column with the hero dropped entirely:
// sessions, then the Session summary / Devices / Usage tiles.
func TestHomeView_NarrowSingleColumn(t *testing.T) {
	a := newTestApp(ScreenSessions)
	// The Devices tile only renders when at least one host is known;
	// give it one so the full column order can be checked.
	a.dashboard.SetHosts([]hostStatus{{Name: "sputnik", Local: true, OK: true}})
	out := a.homeView(80, 60) // < 120 → single column, no hero
	if strings.Contains(out, "Hello.") {
		t.Errorf("narrow homeView should drop the Hello hero entirely:\n%s", out)
	}
	// JoinVertical lays blocks top-to-bottom, so byte offset increases
	// with row: each anchor must appear after the previous one.
	//
	// Post-redesign tile order: Sessions (with inline state counts in
	// the title — no standalone "Session summary" tile anymore),
	// Devices, Usage (renamed from "Claude usage" — Claude is now a
	// sub-section heading inside the consolidated Usage panel).
	anchors := []string{"Sessions", "Devices", "Usage"}
	prev := -1
	for _, want := range anchors {
		at := strings.Index(out, want)
		if at < 0 {
			t.Fatalf("narrow homeView is missing %q", want)
		}
		if at <= prev {
			t.Errorf("%q (offset %d) should render below the previous tile (offset %d)", want, at, prev)
		}
		prev = at
	}
}

// TestHomeView_WideTwoColumn — at or above the breakpoint the Home
// screen is a hero banner over two columns: the sessions list on the
// left, the session detail + stat tiles on the right. Every tile is
// present and no line overflows.
//
// After the design-system redesign: the standalone "Session summary"
// tile dissolved into the Sessions pane title (`Sessions (3 · 1
// active · ...)`), and "Claude usage" became "Usage" with a
// `Claude · 5h window` sub-section. We assert on the current names.
func TestHomeView_WideTwoColumn(t *testing.T) {
	a := newTestApp(ScreenSessions)
	a.dashboard.SetHosts([]hostStatus{{Name: "sputnik", Local: true, OK: true}})
	out := a.homeView(200, 60) // ≥ 120 → banner + two columns
	assertNoOverflow(t, out, 200)
	for _, want := range []string{"Hello.", "Sessions", "Devices", "Usage"} {
		if !strings.Contains(out, want) {
			t.Errorf("wide homeView is missing %q", want)
		}
	}
}

// TestHomeView_WideHeroIsBanner — the hero is a full-width banner
// above both columns, so "Hello." renders before the sessions list
// (left column) and before the Usage tile (right column).
//
// After the redesign: "Session summary" no longer exists as a
// standalone tile — its counts inline into the Sessions pane title.
// The right column's primary tile is "Usage" now.
func TestHomeView_WideHeroIsBanner(t *testing.T) {
	a := newTestApp(ScreenSessions)
	a.dashboard.SetHosts([]hostStatus{{Name: "sputnik", Local: true, OK: true}})
	out := a.homeView(200, 60)
	hello := strings.Index(out, "Hello.")
	sessions := strings.Index(out, "Sessions")
	summary := strings.Index(out, "Usage")
	if hello < 0 || sessions < 0 || summary < 0 {
		t.Fatalf("wide homeView missing an expected anchor:\n%s", out)
	}
	if hello > sessions || hello > summary {
		t.Errorf("the hero banner should render above both columns:\n%s", out)
	}
}
