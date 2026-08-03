package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/config"
)

// TestHostAdd_CorruptConfigDoesNotWipe — regression for the data-loss
// bug where `ccmux host add` did `cfg, _ := config.Load()`, discarding
// the error. On a corrupt config.toml, Load returns Defaults() + an
// error; swallowing it and Saving would truncate the file, erasing
// every other host and all settings. The fix returns the error and
// leaves the file byte-for-byte untouched.
func TestHostAdd_CorruptConfigDoesNotWipe(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgPath := filepath.Join(home, ".config", "ccmux", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	// A deliberately broken TOML file with real user content we must not lose.
	corrupt := "theme = \"dracula\"\n[[host]\nname = \"mini\"\n=== not toml ==="
	if err := os.WriteFile(cfgPath, []byte(corrupt), 0o644); err != nil {
		t.Fatal(err)
	}

	c := newHostCmd()
	c.SetArgs([]string{"add", "newhost", "100.64.0.9"})
	c.SilenceUsage = true
	c.SilenceErrors = true
	err := c.Execute()
	if err == nil {
		t.Fatal("host add on a corrupt config should error, not silently rewrite")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Errorf("error should mention the load failure, got: %v", err)
	}

	// The file must be untouched — same bytes as before.
	got, rerr := os.ReadFile(cfgPath)
	if rerr != nil {
		t.Fatalf("config file disappeared: %v", rerr)
	}
	if string(got) != corrupt {
		t.Errorf("corrupt config was modified despite the abort:\n%s", got)
	}
}

// TestHostAdd_RejectsDuplicateName — `host add` used to silently
// append a second entry with the same name. `host remove <name>`
// deletes every match, so the eventual cleanup would wipe the
// original host too. A duplicate add must error and leave the config
// with exactly one entry.
func TestHostAdd_RejectsDuplicateName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	add := func(addr string) error {
		c := newHostCmd()
		c.SetArgs([]string{"add", "mini", addr})
		c.SilenceUsage = true
		c.SilenceErrors = true
		return c.Execute()
	}
	if err := add("100.64.0.9"); err != nil {
		t.Fatalf("first add: %v", err)
	}
	err := add("100.64.0.10")
	if err == nil {
		t.Fatal("duplicate host add should error, not append")
	}
	if !strings.Contains(err.Error(), "already exists") || !strings.Contains(err.Error(), "host remove") {
		t.Errorf("error should explain the duplicate and suggest `host remove`: %v", err)
	}

	cfg, lerr := config.Load()
	if lerr != nil {
		t.Fatal(lerr)
	}
	count := 0
	for _, h := range cfg.Hosts {
		if h.Name == "mini" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("config has %d hosts named mini, want exactly 1 (hosts: %+v)", count, cfg.Hosts)
	}
}

// TestHostSetupSSH_CorruptConfigRefusesEarly — same data-loss class as
// `host add`: runHostSetupSSH did `cfg, _ := config.Load()`, and its
// later Saves would rewrite config.toml from Defaults(), erasing every
// host. On a corrupt config it must refuse before probing anything.
func TestHostSetupSSH_CorruptConfigRefusesEarly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgPath := filepath.Join(home, ".config", "ccmux", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	corrupt := "theme = \"dracula\"\n[[host]\nname = \"mini\"\n=== not toml ==="
	if err := os.WriteFile(cfgPath, []byte(corrupt), 0o600); err != nil {
		t.Fatal(err)
	}

	err := runHostSetupSSH("mini", true)
	if err == nil {
		t.Fatal("setup-ssh on a corrupt config should error, not proceed toward a Save")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Errorf("error should mention the load failure, got: %v", err)
	}

	got, rerr := os.ReadFile(cfgPath)
	if rerr != nil {
		t.Fatalf("config file disappeared: %v", rerr)
	}
	if string(got) != corrupt {
		t.Errorf("corrupt config was modified despite the abort:\n%s", got)
	}
}

// TestAppendHostToFreshConfig_DoesNotRevertWriteBack — regression for
// the stale-snapshot revert: the enumerate loop used to Save the cfg
// snapshot taken at the top of runHostSetupSSH, silently undoing the
// SSH user that writeBackUserIfMissing had just persisted. The helper
// must load fresh state right before each Save.
func TestAppendHostToFreshConfig_DoesNotRevertWriteBack(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Seed a config with one user-less host, exactly the state after a
	// fresh `host add mini sputnik`.
	seed := config.Defaults()
	seed.Hosts = []config.Host{{Name: "mini", Address: "sputnik", Mosh: true, Port: 7474}}
	if err := config.Save(seed); err != nil {
		t.Fatal(err)
	}

	// The flow: a snapshot is loaded up-front…
	if _, err := config.Load(); err != nil {
		t.Fatal(err)
	}
	// …then the wizard persists the authenticated user…
	if err := writeBackUserIfMissing("mini", "alice"); err != nil {
		t.Fatal(err)
	}
	// …then the enumerate loop appends a discovered host and saves.
	if err := appendHostToFreshConfig(config.Host{Name: "bob@sputnik", Address: "sputnik", User: "bob", Mosh: true}); err != nil {
		t.Fatal(err)
	}

	final, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	var miniUser string
	var haveBob bool
	for _, h := range final.Hosts {
		if h.Name == "mini" {
			miniUser = h.User
		}
		if h.Name == "bob@sputnik" {
			haveBob = true
		}
	}
	if miniUser != "alice" {
		t.Errorf("write-back reverted: mini.User = %q, want alice", miniUser)
	}
	if !haveBob {
		t.Errorf("appended host missing from final config: %+v", final.Hosts)
	}
}

// TestAppendHostToFreshConfig_CorruptConfigRefuses — the helper itself
// must carry the same guard: never Save a Defaults()-on-error config.
func TestAppendHostToFreshConfig_CorruptConfigRefuses(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgPath := filepath.Join(home, ".config", "ccmux", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	corrupt := "=== not toml ==="
	if err := os.WriteFile(cfgPath, []byte(corrupt), 0o600); err != nil {
		t.Fatal(err)
	}
	err := appendHostToFreshConfig(config.Host{Name: "x", Address: "y"})
	if err == nil {
		t.Fatal("appendHostToFreshConfig on a corrupt config should error")
	}
	got, _ := os.ReadFile(cfgPath)
	if string(got) != corrupt {
		t.Errorf("corrupt config was rewritten:\n%s", got)
	}
}
