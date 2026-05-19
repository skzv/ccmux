package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// withFakeHome redirects $HOME to a tempdir so Load/Save/Path are
// hermetic. Restored automatically by t.Setenv.
func withFakeHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	return dir
}

func TestDefaults_SaneShape(t *testing.T) {
	withFakeHome(t)
	d := Defaults()
	if d.Theme == "" {
		t.Error("default theme is empty")
	}
	if d.Daemon.PollIntervalSeconds <= 0 {
		t.Errorf("default poll interval = %d, want > 0", d.Daemon.PollIntervalSeconds)
	}
	if d.Daemon.IdleSecondsForNeedsInput <= 0 {
		t.Errorf("default idle threshold = %d, want > 0", d.Daemon.IdleSecondsForNeedsInput)
	}
	if d.Daemon.TailnetPort == 0 {
		t.Errorf("default tailnet port = 0")
	}
	if !d.Notes.AutoLogSessions {
		t.Error("AutoLogSessions should default true")
	}
	if d.Sleep.LowBatteryCutoff <= 0 || d.Sleep.LowBatteryCutoff > 100 {
		t.Errorf("low_battery_cutoff = %d, want 1..100", d.Sleep.LowBatteryCutoff)
	}
	// agents.default must seed to claude on fresh installs. A user who
	// runs `ccmux new` without ever touching Settings shouldn't fall
	// into a bare shell — that breaks the multi-agent refactor's
	// "every session lands in an agent" promise.
	if d.Agents.Default != "claude" {
		t.Errorf("Agents.Default = %q, want claude (default for fresh installs)", d.Agents.Default)
	}
}

func TestPath_LivesUnderHome(t *testing.T) {
	home := withFakeHome(t)
	p, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".config", "ccmux", "config.toml")
	if p != want {
		t.Fatalf("Path() = %q, want %q", p, want)
	}
}

func TestLoad_MissingFileReturnsDefaults(t *testing.T) {
	withFakeHome(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load on missing file: %v", err)
	}
	if cfg.Theme != Defaults().Theme {
		t.Errorf("Theme = %q, want default %q", cfg.Theme, Defaults().Theme)
	}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	withFakeHome(t)
	in := Defaults()
	in.Theme = "dracula"
	in.Daemon.ListenTailnet = true
	in.Daemon.TailnetPort = 8888
	in.Notes.AutoLogSessions = false
	in.Subscription.Tier = "max20x"
	in.Hosts = []Host{
		{Name: "mac-mini", Address: "100.64.0.5", User: "skz", Port: 22, Mosh: true},
		{Name: "laptop", Address: "100.64.0.6", User: "skz"},
	}
	in.Scaffold.Dirs = []string{"src", "docs"}
	in.Scaffold.InitialPrompt = "hello {{name}}"

	if err := Save(in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("round-trip mismatch:\ngot=%+v\nwant=%+v", got, in)
	}
}

func TestLoad_PartialFileMergesWithDefaults(t *testing.T) {
	home := withFakeHome(t)
	cfgPath := filepath.Join(home, ".config", "ccmux", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	// File overrides theme + one nested field; everything else should
	// stay at the default.
	body := "" +
		"theme = \"nord\"\n" +
		"\n[daemon]\n" +
		"tailnet_port = 9999\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Theme != "nord" {
		t.Errorf("Theme override didn't take: %q", got.Theme)
	}
	if got.Daemon.TailnetPort != 9999 {
		t.Errorf("TailnetPort override didn't take: %d", got.Daemon.TailnetPort)
	}
	// Default fields should still be present.
	d := Defaults()
	if got.Daemon.PollIntervalSeconds != d.Daemon.PollIntervalSeconds {
		t.Errorf("PollIntervalSeconds: got %d, want default %d",
			got.Daemon.PollIntervalSeconds, d.Daemon.PollIntervalSeconds)
	}
	if got.Notes.AutoLogSessions != d.Notes.AutoLogSessions {
		t.Errorf("Notes.AutoLogSessions clobbered: %v", got.Notes.AutoLogSessions)
	}
}

func TestLoad_BadTOMLReturnsError(t *testing.T) {
	home := withFakeHome(t)
	cfgPath := filepath.Join(home, ".config", "ccmux", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("this is = = not toml ="), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("expected parse error on bad TOML, got nil")
	}
}

func TestSave_CreatesParentDirs(t *testing.T) {
	home := withFakeHome(t)
	// Parent dirs deliberately absent — Save must create them.
	if err := Save(Defaults()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "ccmux", "config.toml")); err != nil {
		t.Fatalf("config file not created: %v", err)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"", "", "x"}, "x"},
		{[]string{"first", "second"}, "first"},
		{[]string{"", ""}, ""},
		{nil, ""},
	}
	for _, tc := range cases {
		if got := firstNonEmpty(tc.in...); got != tc.want {
			t.Errorf("firstNonEmpty(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
