package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfigFile(t *testing.T, body string) string {
	t.Helper()
	p, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestSave_CarriesUnknownKeys — keys this build doesn't model (from a
// newer ccmux, or hand-added) must survive a Save instead of vanishing
// the first time the user touches a Settings row.
func TestSave_CarriesUnknownKeys(t *testing.T) {
	withFakeHome(t)
	p := writeConfigFile(t, `
theme = "nord"
future_top = "keep me"

[agents]
default = "codex"
future_nested = 42

[future_table]
a = "x"
b = [1, 2]
`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Theme = "dracula"
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("saved file no longer loads: %v\n%s", err, data)
	}
	if got.Theme != "dracula" || got.Agents.Default != "codex" {
		t.Errorf("known fields wrong after save: theme=%q default=%q", got.Theme, got.Agents.Default)
	}
	for _, want := range []string{`future_top = "keep me"`, "future_nested = 42", "[future_table]", `a = "x"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("saved config lost %q:\n%s", want, data)
		}
	}
}

// TestUpdate_RefusesToOverwriteUnparseableFile — a TOML syntax error in
// a hand-edited config must not be "fixed" by writing defaults over the
// user's hosts and API keys.
func TestUpdate_RefusesToOverwriteUnparseableFile(t *testing.T) {
	withFakeHome(t)
	body := "theme = \"nord\"\n[[hosts]]\nname = \"mini\"\naddress = \n"
	p := writeConfigFile(t, body)
	if _, err := Update(func(c *Config) error { c.Theme = "dracula"; return nil }); err == nil {
		t.Fatal("Update on an unparseable file should fail")
	}
	data, _ := os.ReadFile(p)
	if string(data) != body {
		t.Errorf("unparseable config was rewritten:\n%s", data)
	}
}

// TestUpdate_AppliesToOnDiskState — Update must start from the file,
// not from some caller's stale copy, so two independent edits compose.
func TestUpdate_AppliesToOnDiskState(t *testing.T) {
	withFakeHome(t)
	if _, err := Update(func(c *Config) error {
		c.Hosts = append(c.Hosts, Host{Name: "mini", Address: "mini.ts.net"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(func(c *Config) error { c.Theme = "nord"; return nil }); err != nil {
		t.Fatal(err)
	}
	got, _ := Load()
	if len(got.Hosts) != 1 || got.Theme != "nord" {
		t.Errorf("edits didn't compose: hosts=%v theme=%q", got.Hosts, got.Theme)
	}
}

// TestLoad_LegacyAPITierIsUnset — every save before v0.6.1 wrote the
// then-default `tier = "api"`, and v0.6.1 started treating "api" as an
// explicit choice that hides the auto-detected Claude plan: a Max
// subscriber upgrading with an old config.toml saw "api". A file with
// no schema_version predates the distinction, so its "api" is unset.
func TestLoad_LegacyAPITierIsUnset(t *testing.T) {
	withFakeHome(t)
	writeConfigFile(t, "theme = \"nord\"\n\n[subscription]\n  tier = \"api\"\n  [subscription.tiers]\n    codex = \"plus\"\n")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Subscription.TierFor("claude"); got != "" {
		t.Errorf("legacy claude tier = %q, want unset", got)
	}
	if got := cfg.Subscription.TierFor("codex"); got != "plus" {
		t.Errorf("codex tier = %q, want plus (only the old claude default is reinterpreted)", got)
	}
	if cfg.Theme != "nord" {
		t.Errorf("theme = %q, want the file's nord", cfg.Theme)
	}
}

// TestSave_VersionedAPITierIsExplicit — once a file carries the
// current schema version, "api" is the user's choice and must stick,
// and every save writes the version so the legacy rule never reapplies.
func TestSave_VersionedAPITierIsExplicit(t *testing.T) {
	withFakeHome(t)
	p := writeConfigFile(t, "[subscription]\n  tier = \"max5x\"\n")
	if _, err := Update(func(c *Config) error {
		c.Subscription.SetTierFor("claude", "api")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "schema_version = ") {
		t.Fatalf("saved config has no schema_version:\n%s", data)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Subscription.TierFor("claude"); got != "api" {
		t.Errorf("explicit api after a save = %q, want api", got)
	}
}
