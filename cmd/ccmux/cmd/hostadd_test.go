package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/config"
)

// runHostAddCmd runs `ccmux host add <args…>` in-process against an
// isolated config and returns its stdout and error.
func runHostAddCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newHostCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"add"}, args...))
	err := cmd.Execute()
	return out.String(), err
}

// TestHostAdd_RejectsInvalidInput — `host add` stored anything: an
// empty name or address (which later dialed localhost), the reserved
// name "local", names with spaces or shell metacharacters, an address
// ssh would read as an option, and host:port (which failed much later
// as "…:7474:7474"). Each must be refused, and nothing written.
func TestHostAdd_RejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name, addr string
		flags      []string
		wantErr    string
	}{
		{"", "100.64.0.5", nil, "invalid host name"},
		{"mini", "", nil, "required"},
		{"local", "100.64.0.5", nil, "reserved"},
		{"LOCAL", "100.64.0.5", nil, "reserved"},
		{"bad name", "100.64.0.5", nil, "invalid host name"},
		{"host;rm -rf /", "100.64.0.5", nil, "invalid host name"},
		{"-mini", "100.64.0.5", nil, "invalid host name"},
		{"mini", "-oProxyCommand=touch /tmp/pwned", nil, "start with '-'"},
		{"mini", "-oProxyCommand=x@mini", nil, "start with '-'"},
		{"mini", "mini.tail.ts.net:7474", nil, "--port"},
		{"mini", "[fd7a::1]:7474", nil, "--port"},
		{"mini", "http://mini", nil, "scheme"},
		{"mini", "mini;reboot", nil, "not a host name"},
		{"mini", "mini tail", nil, "not a host name"},
		{"mini", "$(id).ts.net", nil, "not a host name"},
		{"mini", "-x@mini", nil, "start with '-'"},
		{"mini", "@mini", nil, "empty user"},
		{"mini", "bad user@mini", nil, "invalid SSH user"},
		{"mini", "mini", []string{"--user", "-oProxyCommand=x"}, "invalid SSH user"},
		{"mini", "alice@mini", []string{"--user", "bob"}, "--user"},
		{"mini", "mini", []string{"--port", "0"}, "--port"},
		{"mini", "mini", []string{"--ssh-port", "70000"}, "--ssh-port"},
	}
	for _, tc := range cases {
		withTempCcmuxConfig(t)
		// "--" so a leading-dash name/address reaches the validation
		// (cobra would otherwise refuse it as an unknown flag).
		_, err := runHostAddCmd(t, append(tc.flags, "--", tc.name, tc.addr)...)
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("host add %q %q %v: err = %v, want it to mention %q", tc.name, tc.addr, tc.flags, err, tc.wantErr)
		}
		cfg, _ := config.Load()
		if len(cfg.Hosts) != 0 {
			t.Errorf("host add %q %q %v stored %+v despite the error", tc.name, tc.addr, tc.flags, cfg.Hosts)
		}
	}
}

// TestHostAdd_FlagsAndConfirmation — the README and SSH guide document
// --mosh, --user and a port option; none existed ("unknown flag:
// --user"). They must land in the config row, and success must say
// what was added (it printed nothing).
func TestHostAdd_FlagsAndConfirmation(t *testing.T) {
	withTempCcmuxConfig(t)
	out, err := runHostAddCmd(t, "alice@sputnik", "sputnik", "--user", "alice", "--ssh-port", "2222", "--port", "7575", "--mosh=false")
	if err != nil {
		t.Fatalf("host add: %v", err)
	}
	cfg, err := config.Load()
	if err != nil || len(cfg.Hosts) != 1 {
		t.Fatalf("hosts = %+v, %v", cfg.Hosts, err)
	}
	want := config.Host{Name: "alice@sputnik", Address: "sputnik", User: "alice", Port: 7575, SSHPort: 2222, Mosh: false}
	if cfg.Hosts[0] != want {
		t.Errorf("stored %+v, want %+v", cfg.Hosts[0], want)
	}
	for _, s := range []string{"added host alice@sputnik", "sputnik:7575", "ssh", "alice@sputnik", "2222"} {
		if !strings.Contains(out, s) {
			t.Errorf("confirmation %q should mention %q", out, s)
		}
	}
}

// TestHostAdd_UserInAddressAndDefaults — user@host in the address sets
// the user; defaults stay mosh / 7474 / ssh 22 (SSHPort left unset), and
// IPv6 literals are accepted.
func TestHostAdd_UserInAddressAndDefaults(t *testing.T) {
	withTempCcmuxConfig(t)
	if _, err := runHostAddCmd(t, "mini", "alice@mini.tail-x.ts.net"); err != nil {
		t.Fatal(err)
	}
	if _, err := runHostAddCmd(t, "v6", "fd7a:115c:a1e0::1"); err != nil {
		t.Fatal(err)
	}
	if _, err := runHostAddCmd(t, "v6b", "[fd7a:115c:a1e0::2]"); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load()
	if len(cfg.Hosts) != 3 {
		t.Fatalf("hosts = %+v", cfg.Hosts)
	}
	want := config.Host{Name: "mini", Address: "mini.tail-x.ts.net", User: "alice", Port: 7474, Mosh: true}
	if cfg.Hosts[0] != want {
		t.Errorf("stored %+v, want %+v", cfg.Hosts[0], want)
	}
	if cfg.Hosts[1].Address != "fd7a:115c:a1e0::1" || cfg.Hosts[2].Address != "fd7a:115c:a1e0::2" {
		t.Errorf("IPv6 addresses stored as %q / %q", cfg.Hosts[1].Address, cfg.Hosts[2].Address)
	}
}

// TestHostAdd_CustomDaemonPortPinsSSHPort — an entry with a non-7474
// Port and no SSHPort reads as a legacy "port is the SSH port" host, so
// a custom --port alone must also store ssh port 22.
func TestHostAdd_CustomDaemonPortPinsSSHPort(t *testing.T) {
	withTempCcmuxConfig(t)
	if _, err := runHostAddCmd(t, "mini", "mini", "--port", "8080"); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load()
	if got := sshTargetForHost(cfg.Hosts[0]).Port; got != 22 {
		t.Errorf("ssh target port = %d, want 22 (8080 is the ccmuxd port)", got)
	}
}
