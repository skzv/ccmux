package keychain

import "testing"

// TestLooksLocked pins the `security show-keychain-info` failure-output
// match. A locked login keychain is what makes `gh` / `moshi-hook` look
// unconfigured over SSH; mis-reading this output would resurrect the
// false "not signed in / not paired" report.
func TestLooksLocked(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want bool
	}{
		{
			"locked, non-interactive context (prompt auto-canceled)",
			"security: SecKeychainCopySettings /Users/x/Library/Keychains/login.keychain-db: User canceled the operation.",
			true,
		},
		{
			"locked over SSH (no GUI to show an unlock prompt)",
			"security: SecKeychainCopySettings login.keychain-db: User interaction is not allowed.",
			true,
		},
		{
			"unlocked — settings printed, no failure text",
			`Keychain "login.keychain-db" lock-on-sleep timeout=300s`,
			false,
		},
		{
			"unrelated failure: keychain file missing",
			"security: SecKeychainCopySettings: The specified keychain could not be found.",
			false,
		},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksLocked(tc.out); got != tc.want {
				t.Errorf("looksLocked(%q) = %v, want %v", tc.out, got, tc.want)
			}
		})
	}
}
