package config

import (
	"os"
	"strings"
	"testing"
)

// TestSave_DoesNotAdvertiseIdleRelease — regression: sleep
// .idle_release_minutes was defaulted to 10 and written into every saved
// config (and shown in Settings), but nothing ever read it — the daemon
// holds the keep-awake lock whenever any session is active. The key is
// reserved now: unset by default and not written unless a user set it.
func TestSave_DoesNotAdvertiseIdleRelease(t *testing.T) {
	withFakeHome(t)
	if err := Save(Defaults()); err != nil {
		t.Fatal(err)
	}
	p, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "idle_release_minutes") {
		t.Errorf("saved config advertises the unimplemented idle_release_minutes:\n%s", data)
	}
}

// TestLoad_LegacyIdleReleaseStillLoads — configs written by older
// releases carry idle_release_minutes; they must keep loading and keep
// the value across a save.
func TestLoad_LegacyIdleReleaseStillLoads(t *testing.T) {
	withFakeHome(t)
	p, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(strings.TrimSuffix(p, "config.toml"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("[sleep]\nmode = \"safe\"\nidle_release_minutes = 15\nlow_battery_cutoff = 30\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Sleep.LowBatteryCutoff != 30 || cfg.Sleep.IdleReleaseMinutes != 15 {
		t.Errorf("sleep = %+v", cfg.Sleep)
	}
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	if !strings.Contains(string(data), "idle_release_minutes = 15") {
		t.Errorf("user's idle_release_minutes lost on save:\n%s", data)
	}
}
