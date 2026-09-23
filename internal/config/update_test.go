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
