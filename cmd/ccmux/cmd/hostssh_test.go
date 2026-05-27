package cmd

import (
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/config"
)

// TestParseAdHocTarget_Shapes pins the input grammar: bare host,
// user@host, user@host:port, and the error paths. The CLI's first
// user-facing surface, so the parse tests double as the documented
// contract.
func TestParseAdHocTarget_Shapes(t *testing.T) {
	cases := []struct {
		in       string
		wantHost string
		wantPort int
		wantUser string // "" means "default to current user — don't pin"
		wantErr  bool
	}{
		{"sputnik", "sputnik", 22, "", false},
		{"alice@sputnik", "sputnik", 22, "alice", false},
		{"alice@sputnik:2222", "sputnik", 2222, "alice", false},
		{"sputnik:22", "sputnik", 22, "", false},
		{"bob@10.0.0.1", "10.0.0.1", 22, "bob", false},
		{"bob@10.0.0.1:65000", "10.0.0.1", 65000, "bob", false},
		{"", "", 0, "", true},
		{"alice@", "", 0, "", true},
		{"alice@sputnik:abc", "", 0, "", true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, name, err := parseAdHocTarget(c.in)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, c.wantErr)
			}
			if c.wantErr {
				return
			}
			if name != "" {
				t.Errorf("ad-hoc parse returned configured-name %q", name)
			}
			if got.Host != c.wantHost {
				t.Errorf("Host = %q, want %q", got.Host, c.wantHost)
			}
			if got.Port != c.wantPort {
				t.Errorf("Port = %d, want %d", got.Port, c.wantPort)
			}
			if c.wantUser != "" && got.User != c.wantUser {
				t.Errorf("User = %q, want %q", got.User, c.wantUser)
			}
			if c.wantUser == "" && got.User == "" {
				t.Errorf("User defaulted to empty — must fall back to local $USER")
			}
		})
	}
}

// TestResolveTarget_PicksConfiguredHostByName — when the arg matches
// a configured host name, we hydrate User/Address/Port from the
// stored entry. The 7474 → 22 fallback is the subtle invariant
// here: hosts.toml stores the ccmuxd HTTP port (7474), but SSH
// itself runs on 22. setup-ssh must hit 22, not 7474.
func TestResolveTarget_PicksConfiguredHostByName(t *testing.T) {
	cfg := config.Config{
		Hosts: []config.Host{
			{Name: "mini", Address: "sputnik", User: "alice", Port: 7474},
		},
	}
	got, name, err := resolveTarget("mini", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if name != "mini" {
		t.Errorf("configured-name = %q, want mini", name)
	}
	if got.Host != "sputnik" || got.User != "alice" {
		t.Errorf("Target = %+v, want sputnik/alice", got)
	}
	if got.Port != 22 {
		t.Errorf("Port = %d, want 22 (7474 is the ccmuxd port, not SSH)", got.Port)
	}
}

// TestResolveTarget_HonorsExplicitNonStandardPort — if the user set
// Port to a non-default, non-7474 value, we trust it. This is the
// "my sshd lives on 2222" case.
func TestResolveTarget_HonorsExplicitNonStandardPort(t *testing.T) {
	cfg := config.Config{
		Hosts: []config.Host{
			{Name: "weirdo", Address: "sputnik", User: "alice", Port: 2222},
		},
	}
	got, _, err := resolveTarget("weirdo", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 2222 {
		t.Errorf("Port = %d, want 2222 (explicit non-standard port must be honored)", got.Port)
	}
}

// TestResolveTarget_FallsBackToCurrentUserWhenConfiguredEmpty — a
// fresh `host add mini sputnik` produces an entry with User="". On
// `setup-ssh mini` we fill in the local $USER so the wizard prompts
// for a real password rather than failing with "user is empty".
func TestResolveTarget_FallsBackToCurrentUserWhenConfiguredEmpty(t *testing.T) {
	cfg := config.Config{
		Hosts: []config.Host{
			{Name: "mini", Address: "sputnik", User: "", Port: 22},
		},
	}
	got, _, err := resolveTarget("mini", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got.User == "" {
		t.Error("User defaulted to empty — must fall back to local $USER")
	}
}

// TestResolveTarget_FallsThroughToAdHocWhenNoMatch — passing a
// "[user@]host" string that's NOT a configured name still works;
// we route it through parseAdHocTarget. Without this, scripted use
// (`setup-ssh alice@new-machine`) would break for any host the
// user hasn't added yet.
func TestResolveTarget_FallsThroughToAdHocWhenNoMatch(t *testing.T) {
	cfg := config.Config{
		Hosts: []config.Host{{Name: "elsewhere", Address: "other-host"}},
	}
	got, name, err := resolveTarget("alice@new-machine", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if name != "" {
		t.Errorf("expected ad-hoc path, got configured-name %q", name)
	}
	if got.Host != "new-machine" || got.User != "alice" {
		t.Errorf("ad-hoc parse failed: %+v", got)
	}
}

// TestDefaultHostName_StripsMagicDNS — the MagicDNS-suffixed name
// is verbose for a display label. We want "sputnik" not
// "sputnik.tail-1234.ts.net".
func TestDefaultHostName_StripsMagicDNS(t *testing.T) {
	cases := []struct{ in, want string }{
		{"sputnik", "sputnik"},
		{"sputnik.tail-1234.ts.net", "sputnik"},
		{"10.0.0.1", "10"},
	}
	for _, c := range cases {
		got := defaultHostName(c.in)
		if got != c.want {
			t.Errorf("defaultHostName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestCurrentUser_NeverEmpty — never produce an empty user, no
// matter how stripped the environment.
func TestCurrentUser_NeverEmpty(t *testing.T) {
	if got := currentUser(); strings.TrimSpace(got) == "" {
		t.Error("currentUser() returned empty")
	}
}

// TestHostSetupSSH_RegisteredOnHostCommand — the cobra wiring: the
// subcommand exists with the expected name. Catches a future
// refactor that forgets to call newHostSetupSSHCmd().
func TestHostSetupSSH_RegisteredOnHostCommand(t *testing.T) {
	host := newHostCmd()
	var got []string
	for _, sub := range host.Commands() {
		got = append(got, sub.Name())
	}
	want := "setup-ssh"
	found := false
	for _, n := range got {
		if n == want {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("`ccmux host` is missing the %q subcommand; got %v", want, got)
	}
}
