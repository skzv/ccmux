package ghauth

import "testing"

func TestStatus_OK(t *testing.T) {
	if !(Status{State: StateAuthed}).OK() {
		t.Error("authed status should be OK")
	}
	for _, s := range []State{StateMissing, StateNotAuthed} {
		if (Status{State: s}).OK() {
			t.Errorf("state %v should not be OK", s)
		}
	}
}

func TestStatus_Hint(t *testing.T) {
	if got := (Status{State: StateAuthed}).Hint(); got != "" {
		t.Errorf("authed should have empty hint, got %q", got)
	}
	if got := (Status{State: StateMissing}).Hint(); got == "" {
		t.Error("missing should have a hint")
	}
	if got := (Status{State: StateNotAuthed}).Hint(); got == "" {
		t.Error("not-authed should have a hint")
	}
}

func TestParseUser_VariousGhOutputForms(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"newer gh: account <user> (keyring)",
			`github.com
  ✓ Logged in to github.com account skzv (keyring)
  - Active account: true`,
			"skzv",
		},
		{
			"older gh: Logged in to github.com as <user>",
			`github.com
  ✓ Logged in to github.com as skzv (oauth_token)
  ✓ Git operations for github.com configured to use ssh protocol.`,
			"skzv",
		},
		{
			"weird whitespace and trailing paren",
			"  Logged in to github.com as alice (oauth)\n",
			"alice",
		},
		{
			"no match",
			"some entirely different output",
			"",
		},
		{
			"empty",
			"",
			"",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseUser(tc.in); got != tc.want {
				t.Errorf("parseUser = %q, want %q (in=%q)", got, tc.want, tc.in)
			}
		})
	}
}

// TestDetect_NoBinaryYieldsMissing — exercises the LookPath error path
// without depending on what's installed on the test host: we override
// $PATH so `gh` is unreachable.
func TestDetect_NoBinaryYieldsMissing(t *testing.T) {
	t.Setenv("PATH", "/var/empty")
	got := Detect(t.Context())
	if got.State != StateMissing {
		t.Fatalf("expected StateMissing with empty PATH, got %v", got.State)
	}
}
