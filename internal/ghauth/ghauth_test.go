package ghauth

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestStatus_OK(t *testing.T) {
	if !(Status{State: StateAuthed}).OK() {
		t.Error("authed status should be OK")
	}
	for _, s := range []State{StateMissing, StateNotAuthed, StateUnknown} {
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
	// StateUnknown is "we couldn't check" — there's no action for the
	// user to take, so Hint must stay empty (no nag).
	if got := (Status{State: StateUnknown}).Hint(); got != "" {
		t.Errorf("unknown should have empty hint (no nag), got %q", got)
	}
}

// TestClassify locks in the fix for the false "gh not signed in" the
// setup wizard reported when `gh auth status` was slow. That command
// validates the token against the GitHub API, so a degraded network can
// blow past the timeout. A timed-out check means "couldn't tell"
// (StateUnknown) — it must NEVER be reported as StateNotAuthed, which
// would nag an already-signed-in user to re-run `gh auth login`.
func TestClassify(t *testing.T) {
	cases := []struct {
		name       string
		out        string
		runErr     error
		timedOut   bool
		wantState  State
		wantUser   string
		wantDetail string // substring expected in Detail; "" means Detail must be empty
	}{
		{
			name:      "happy path → authed, user parsed, no detail",
			out:       "github.com\n  ✓ Logged in to github.com account skzv (keyring)",
			wantState: StateAuthed, wantUser: "skzv", wantDetail: "",
		},
		{
			name:       "non-zero exit → not authed, detail carries gh's message",
			out:        "You are not logged into any GitHub hosts. Run gh auth login.",
			runErr:     errors.New("exit status 1"),
			wantState:  StateNotAuthed,
			wantDetail: "not logged into any GitHub hosts",
		},
		{
			name:       "non-zero exit with no output → detail falls back to the error",
			out:        "",
			runErr:     errors.New("exit status 1"),
			wantState:  StateNotAuthed,
			wantDetail: "exit status 1",
		},
		{
			name:       "killed by our deadline → unknown (NOT not-authed), detail says timed out",
			out:        "",
			runErr:     errors.New("signal: killed"),
			timedOut:   true,
			wantState:  StateUnknown,
			wantDetail: "timed out",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classify([]byte(tc.out), tc.runErr, tc.timedOut)
			if got.State != tc.wantState {
				t.Errorf("State = %v, want %v", got.State, tc.wantState)
			}
			if got.User != tc.wantUser {
				t.Errorf("User = %q, want %q", got.User, tc.wantUser)
			}
			if tc.wantDetail == "" {
				if got.Detail != "" {
					t.Errorf("Detail = %q, want empty", got.Detail)
				}
			} else if !strings.Contains(got.Detail, tc.wantDetail) {
				t.Errorf("Detail = %q, want substring %q", got.Detail, tc.wantDetail)
			}
		})
	}
}

// TestAuthStatusTimeoutHasHeadroom guards the timeout from being tightened
// back to a value that turns a slow network into a false negative.
// `gh auth status` does a network round-trip; 3s (the old value) had no
// headroom for a Mac that just woke or a tailnet still settling.
func TestAuthStatusTimeoutHasHeadroom(t *testing.T) {
	if authStatusTimeout < 8*time.Second {
		t.Errorf("authStatusTimeout=%v is too tight for a network-bound check", authStatusTimeout)
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
