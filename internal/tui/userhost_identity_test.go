package tui

import (
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/sshsetup"
)

// TestUserHostIdentity_TwoUsersSameAddressRenderDistinctly — when
// the user has both `alice@sputnik` and `bob@sputnik` in hosts.toml,
// both rows must show in the Network screen as distinct entries.
// The dedup logic in configuredHostKeys is per-host-row and the
// network model iterates each row, so the invariant is "no
// silent merging". This test pins it.
func TestUserHostIdentity_TwoUsersSameAddressRenderDistinctly(t *testing.T) {
	cfg := config.Config{
		Hosts: []config.Host{
			{Name: "alice@sputnik", Address: "sputnik", User: "alice"},
			{Name: "bob@sputnik", Address: "sputnik", User: "bob"},
		},
	}
	app := New(cfg, "test")
	app.tour.Close()
	app.width, app.height = 100, 30
	// Simulate the data the refresh loop would push in. Use bare
	// hostStatus values since the live refresh is async.
	app.network.SetHosts([]hostStatus{
		{Name: "alice@sputnik", Address: "sputnik:7474", DialHost: "sputnik", User: "alice"},
		{Name: "bob@sputnik", Address: "sputnik:7474", DialHost: "sputnik", User: "bob"},
	})
	v := app.network.View(100, 30)
	if !strings.Contains(v, "alice@sputnik") {
		t.Errorf("Network view missing alice@sputnik row; got %q", firstN(v, 500))
	}
	if !strings.Contains(v, "bob@sputnik") {
		t.Errorf("Network view missing bob@sputnik row; got %q", firstN(v, 500))
	}
}

// TestPersistWizardAdded_AppendsDistinctRows — confirms the wizard's
// post-success persistence creates SEPARATE rows for each user.
func TestPersistWizardAdded_AppendsDistinctRows(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := New(config.Config{
		Hosts: []config.Host{{Name: "alice@sputnik", Address: "sputnik", User: "alice"}},
	}, "test")
	target := sshsetup.Target{User: "alice", Host: "sputnik", Port: 22}
	app2 := persistWizardAdded(app, target, []string{"bob", "carol"})
	if got := len(app2.cfg.Hosts); got != 3 {
		t.Fatalf("len(Hosts) = %d, want 3", got)
	}
	if app2.cfg.Hosts[1].User != "bob" {
		t.Errorf("row[1].User = %q, want bob", app2.cfg.Hosts[1].User)
	}
	if app2.cfg.Hosts[2].User != "carol" {
		t.Errorf("row[2].User = %q, want carol", app2.cfg.Hosts[2].User)
	}
	// All three rows share Address: "sputnik" but should NOT be
	// deduped on persistence — User is the identity discriminator.
	for i, h := range app2.cfg.Hosts {
		if h.Address != "sputnik" {
			t.Errorf("row[%d].Address = %q, want sputnik", i, h.Address)
		}
	}
}

// TestPersistWizardAdded_IdempotentOnRerun — re-running the wizard
// for the same user@host must not duplicate the row. The
// hostExistsByName guard inside persistWizardAdded enforces this.
func TestPersistWizardAdded_IdempotentOnRerun(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := New(config.Config{
		Hosts: []config.Host{{Name: "alice@sputnik", Address: "sputnik", User: "alice"}},
	}, "test")
	target := sshsetup.Target{User: "alice", Host: "sputnik", Port: 22}
	app2 := persistWizardAdded(app, target, []string{"bob"})
	if got := len(app2.cfg.Hosts); got != 2 {
		t.Fatalf("first add: len(Hosts) = %d, want 2", got)
	}
	app3 := persistWizardAdded(app2, target, []string{"bob"})
	if got := len(app3.cfg.Hosts); got != 2 {
		t.Errorf("second add: len(Hosts) = %d, want 2 (idempotent re-run)", got)
	}
}

// TestPersistWizardAdded_HostNameStripsMagicDNSSuffix — the
// generated host name uses the SHORT label so "bob@sputnik" is
// what shows up in `ccmux host list`, not
// "bob@sputnik.tail-1234.ts.net".
func TestPersistWizardAdded_HostNameStripsMagicDNSSuffix(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := New(config.Config{}, "test")
	target := sshsetup.Target{User: "alice", Host: "sputnik.tail-1234.ts.net", Port: 22}
	app2 := persistWizardAdded(app, target, []string{"bob"})
	if got := app2.cfg.Hosts[0].Name; got != "bob@sputnik" {
		t.Errorf("host Name = %q, want bob@sputnik (strip MagicDNS)", got)
	}
	// But Address keeps the full hostname so dialing still works.
	if got := app2.cfg.Hosts[0].Address; got != "sputnik.tail-1234.ts.net" {
		t.Errorf("host Address = %q, want full MagicDNS form", got)
	}
}
