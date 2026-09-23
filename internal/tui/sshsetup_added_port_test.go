package tui

import (
	"testing"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/sshsetup"
)

// TestPersistWizardAdded_SSHPortNotDaemonPort — regression: hosts the
// SSH wizard added from its "other users" step got the SSH port
// (22 / 2222) written into config.Host.Port — the ccmuxd HTTP port,
// default 7474 — so the Network screen dialled their daemon on the SSH
// port. The SSH port belongs in SSHPort; Port stays 0 (default).
func TestPersistWizardAdded_SSHPortNotDaemonPort(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := config.Save(config.Defaults()); err != nil {
		t.Fatal(err)
	}
	app := New(config.Defaults(), "test")
	target := sshsetup.Target{User: "alice", Host: "sputnik", Port: 2222}
	app = persistWizardAdded(app, target, []string{"bob"})

	disk, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, cfg := range []config.Config{app.cfg, disk} {
		if len(cfg.Hosts) != 1 {
			t.Fatalf("hosts = %+v, want exactly bob@sputnik", cfg.Hosts)
		}
		h := cfg.Hosts[0]
		if h.Port != 0 {
			t.Errorf("Host.Port (ccmuxd) = %d, want 0 (default 7474)", h.Port)
		}
		if h.SSHPort != 2222 {
			t.Errorf("Host.SSHPort = %d, want 2222", h.SSHPort)
		}
	}
}
