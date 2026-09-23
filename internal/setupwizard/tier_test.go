package setupwizard

import "testing"

// TestClaudeTierToSave — the wizard must not pin "api" merely because
// the picker defaulted to it: with the tier unset and nothing detected,
// it stays unset so the TUI can still show a plan detected later. A
// deliberate api (config already api, or chosen over a detected paid
// plan) and any paid tier are saved as chosen.
func TestClaudeTierToSave(t *testing.T) {
	cases := []struct {
		onDisk, detected, chosen, want string
	}{
		{"", "", "api", ""},             // default pick, nothing known → unset
		{"", "api", "api", ""},          // detection says api-only → unset (overlay no-ops)
		{"", "max5x", "api", "api"},     // user overrode a detected plan
		{"api", "", "api", "api"},       // already explicit
		{"", "max5x", "max5x", "max5x"}, // accepted the detected plan
		{"", "", "pro", "pro"},
		{"pro", "max20x", " max20x ", "max20x"},
	}
	for _, tc := range cases {
		if got := claudeTierToSave(tc.onDisk, tc.detected, tc.chosen); got != tc.want {
			t.Errorf("claudeTierToSave(%q, %q, %q) = %q, want %q", tc.onDisk, tc.detected, tc.chosen, got, tc.want)
		}
	}
}
