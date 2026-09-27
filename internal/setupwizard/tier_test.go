package setupwizard

import "testing"

// TestClaudeTierToSave — the wizard must not pin a tier the user didn't
// choose: the picker's api default with nothing detected, or the
// detected plan itself. `setup --yes` used to save the detected plan
// permanently, contradicting the TUI's rule that a detected tier is
// shown but never saved (a later plan change then went unnoticed). A
// deliberate api (config already api, or chosen over a detected paid
// plan), any other choice, and any change to a tier on disk are saved.
func TestClaudeTierToSave(t *testing.T) {
	cases := []struct {
		onDisk, detected, chosen, want string
	}{
		{"", "", "api", ""},         // default pick, nothing known → unset
		{"", "api", "api", ""},      // detection says api-only → unset (overlay no-ops)
		{"", "max5x", "api", "api"}, // user overrode a detected plan
		{"api", "", "api", "api"},   // already explicit
		{"", "max5x", "max5x", ""},  // the detected plan (--yes) → unset, the TUI overlays it
		{"", "pro", " pro ", ""},
		{"", "max5x", "max20x", "max20x"}, // chose something other than the detected plan
		{"", "", "pro", "pro"},
		{"pro", "max20x", " max20x ", "max20x"},
		{"max5x", "max5x", "max5x", "max5x"}, // already on disk stays on disk
	}
	for _, tc := range cases {
		if got := claudeTierToSave(tc.onDisk, tc.detected, tc.chosen); got != tc.want {
			t.Errorf("claudeTierToSave(%q, %q, %q) = %q, want %q", tc.onDisk, tc.detected, tc.chosen, got, tc.want)
		}
	}
}
