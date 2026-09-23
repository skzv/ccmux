package cmd

import (
	"testing"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/sshsetup"
)

// TestEnumeratedHost_SSHPortNotDaemonPort — regression: setup-ssh's
// "add other users" step stored the SSH port (22 / 2222) in
// config.Host.Port, which is the ccmuxd HTTP port (default 7474), so
// every added host dialled its daemon on the SSH port. The SSH port
// belongs in SSHPort and Port must stay at its zero default.
func TestEnumeratedHost_SSHPortNotDaemonPort(t *testing.T) {
	for _, port := range []int{22, 2222} {
		h := enumeratedHost("bob", sshsetup.Target{User: "alice", Host: "sputnik.tail-1.ts.net", Port: port})
		if h.Port != 0 {
			t.Errorf("port %d: Host.Port (ccmuxd) = %d, want 0 (default 7474)", port, h.Port)
		}
		if h.SSHPort != port {
			t.Errorf("port %d: Host.SSHPort = %d, want %d", port, h.SSHPort, port)
		}
		if h.Name != "bob@sputnik" || h.User != "bob" || h.Address != "sputnik.tail-1.ts.net" {
			t.Errorf("port %d: unexpected identity %+v", port, h)
		}
	}
}

// TestAppendHostToFreshConfig_SkipsDuplicateName — re-running
// setup-ssh and accepting the same discovered user again must not
// append a second identical row (the TUI wizard already guarded this;
// the CLI didn't).
func TestAppendHostToFreshConfig_SkipsDuplicateName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	seed := config.Defaults()
	seed.Hosts = []config.Host{{Name: "bob@sputnik", Address: "sputnik", User: "bob", Mosh: true}}
	if err := config.Save(seed); err != nil {
		t.Fatal(err)
	}

	added, err := appendHostToFreshConfig(config.Host{Name: "bob@sputnik", Address: "sputnik", User: "bob", SSHPort: 2222})
	if err != nil {
		t.Fatal(err)
	}
	if added {
		t.Error("duplicate host reported as added")
	}
	final, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if n := len(final.Hosts); n != 1 {
		t.Fatalf("hosts = %d, want 1 (no duplicate row): %+v", n, final.Hosts)
	}
	if final.Hosts[0].SSHPort != 0 {
		t.Errorf("existing row was modified: %+v", final.Hosts[0])
	}

	added, err = appendHostToFreshConfig(config.Host{Name: "carol@sputnik", Address: "sputnik", User: "carol"})
	if err != nil || !added {
		t.Fatalf("new host: added=%v err=%v, want added", added, err)
	}
}
