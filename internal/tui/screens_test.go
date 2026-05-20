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
		screen: ScreenHome,
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
		screen: ScreenHome,
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

// TestRenderHeader_NarrowCollapsesToNumbers — on a sub-80-col
// terminal the strip collapses to numbers so it never wraps. The
// active tab still shows its initial. This pins that the narrow path
// also iterates every screen (a hardcoded narrow-mode slice would be
// the same bug in a different spot).
func TestRenderHeader_NarrowCollapsesToNumbers(t *testing.T) {
	a := App{
		styles: styles.Default(),
		keys:   DefaultKeymap(),
		width:  60, // < 80 → narrow
		screen: ScreenConversations,
	}
	header := a.renderHeader()
	// Every screen's number must be present even in narrow mode.
	for _, s := range allScreens() {
		num := itoaTest(int(s) + 1)
		if !strings.Contains(header, num) {
			t.Errorf("narrow header missing number %q for screen %q:\n%s", num, s.String(), header)
		}
	}
	// The active screen (Conversations) shows its initial "C".
	if !strings.Contains(header, "C") {
		t.Errorf("narrow header should show the active screen's initial:\n%s", header)
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
