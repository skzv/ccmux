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
	in.Agents.Claude.Command = "/tmp/claude"
	in.Agents.Codex.Command = "/tmp/codex"
	in.Agents.Antigravity.Command = "/tmp/agy"
	in.Agents.Cursor.Command = "/tmp/cursor-agent"
	in.Hosts = []Host{
		{Name: "mac-mini", Address: "100.64.0.5", User: "skz", Port: 22, Mosh: true},
		{Name: "laptop", Address: "100.64.0.6", User: "skz"},
	}
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

func TestAgentCommands_CommandOverrides(t *testing.T) {
	cfg := Defaults()
	cfg.Agents.Claude.Command = "  /tmp/claude  "
	cfg.Agents.Codex.Command = "  /tmp/codex  "
	cfg.Agents.Antigravity.Command = "  /tmp/agy  "
	cfg.Agents.Cursor.Command = "  /tmp/cursor-agent  "
	if got := cfg.AgentCommands().Claude; got != "/tmp/claude" {
		t.Errorf("AgentCommands().Claude = %q, want /tmp/claude", got)
	}
	if got := cfg.AgentCommands().Codex; got != "/tmp/codex" {
		t.Errorf("AgentCommands().Codex = %q, want /tmp/codex", got)
	}
	if got := cfg.AgentCommands().Antigravity; got != "/tmp/agy" {
		t.Errorf("AgentCommands().Antigravity = %q, want /tmp/agy", got)
	}
	if got := cfg.AgentCommands().Cursor; got != "/tmp/cursor-agent" {
		t.Errorf("AgentCommands().Cursor = %q, want /tmp/cursor-agent", got)
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

// TestDefaults_AttachModeIsMirror — the default must be "mirror" so a
// fresh install lets the same session be watched from laptop + phone
// at once. The whole point of the feature is that it's on by default.
func TestDefaults_AttachModeIsMirror(t *testing.T) {
	withFakeHome(t)
	if got := Defaults().Sessions.AttachMode; got != "mirror" {
		t.Errorf("Defaults().Sessions.AttachMode = %q, want mirror", got)
	}
}

// TestDetachOthersOnAttach — the predicate every attach call site
// reads. Only "exclusive" detaches others; everything else (mirror,
// empty, garbage, casing variants) is treated as mirror — the
// less-destructive default when the value is anything unexpected.
func TestDetachOthersOnAttach(t *testing.T) {
	cases := []struct {
		mode string
		want bool
	}{
		{"exclusive", true},
		{"Exclusive", true},     // case-insensitive
		{"  exclusive  ", true}, // whitespace-tolerant
		{"mirror", false},
		{"", false},         // empty → mirror (back-compat with pre-field configs)
		{"nonsense", false}, // unknown → mirror (safe default)
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			s := SessionsConfig{AttachMode: tc.mode}
			if got := s.DetachOthersOnAttach(); got != tc.want {
				t.Errorf("AttachMode=%q: DetachOthersOnAttach() = %v, want %v", tc.mode, got, tc.want)
			}
		})
	}
}

// TestAttachMode_SurvivesSaveLoad — the field must round-trip through
// config.toml. A user who picks "exclusive" in Settings expects it to
// stick across restarts.
func TestAttachMode_SurvivesSaveLoad(t *testing.T) {
	withFakeHome(t)
	in := Defaults()
	in.Sessions.AttachMode = "exclusive"
	if err := Save(in); err != nil {
		t.Fatal(err)
	}
	out, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if out.Sessions.AttachMode != "exclusive" {
		t.Errorf("AttachMode = %q after round-trip, want exclusive", out.Sessions.AttachMode)
	}
}

// TestAttachMode_MissingKeyKeepsMirrorDefault — a config.toml written
// before this field existed has no [sessions].attach_mode key. Load()
// starts from Defaults() and unmarshals on top, so the missing key
// must leave the mirror default intact rather than zeroing it.
func TestAttachMode_MissingKeyKeepsMirrorDefault(t *testing.T) {
	home := withFakeHome(t)
	// A minimal config with NO [sessions] section at all.
	p := filepath.Join(home, ".config", "ccmux", "config.toml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("theme = \"dracula\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if out.Sessions.AttachMode != "mirror" {
		t.Errorf("AttachMode = %q for a config without the key, want mirror (default preserved)", out.Sessions.AttachMode)
	}
}

// TestDefaults_AutoCheckEnabled — auto-update-check defaults on. The
// feature is opt-out: a fresh install gets the launch-time check
// without the user doing anything.
func TestDefaults_AutoCheckEnabled(t *testing.T) {
	withFakeHome(t)
	if !Defaults().Update.AutoCheck {
		t.Error("Defaults().Update.AutoCheck = false, want true (opt-out feature)")
	}
}

// TestAutoCheck_SurvivesSaveLoad — a user who turns the check off in
// Settings expects it to stay off across restarts.
func TestAutoCheck_SurvivesSaveLoad(t *testing.T) {
	withFakeHome(t)
	in := Defaults()
	in.Update.AutoCheck = false
	if err := Save(in); err != nil {
		t.Fatal(err)
	}
	out, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if out.Update.AutoCheck {
		t.Error("AutoCheck = true after saving false — round-trip lost the setting")
	}
}

// TestAutoCheck_MissingKeyKeepsDefaultOn — a config.toml predating
// the [update] section must still get AutoCheck=true. Load() layers
// the file over Defaults(), so a missing key preserves the default.
func TestAutoCheck_MissingKeyKeepsDefaultOn(t *testing.T) {
	home := withFakeHome(t)
	p := filepath.Join(home, ".config", "ccmux", "config.toml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("theme = \"nord\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !out.Update.AutoCheck {
		t.Error("a config without [update] should keep AutoCheck=true")
	}
}

// TestDefaults_ShowHeadlessIsFalse — the noise-filter default. Fresh
// installs and configs that predate the field both hide headless /
// SDK Claude runs from the conversations list, because automation
// runs otherwise drown out interactive work. Users opt back in via
// conversations.show_headless=true or the H toggle in the TUI.
func TestDefaults_ShowHeadlessIsFalse(t *testing.T) {
	withFakeHome(t)
	if Defaults().Conversations.ShowHeadless {
		t.Error("Defaults().Conversations.ShowHeadless = true, want false (hide automation noise)")
	}
}

// TestShowHeadless_SurvivesSaveLoad — a user who flips it on expects
// the choice to persist across restarts; nothing in the config
// pipeline drops the field silently.
func TestShowHeadless_SurvivesSaveLoad(t *testing.T) {
	withFakeHome(t)
	in := Defaults()
	in.Conversations.ShowHeadless = true
	if err := Save(in); err != nil {
		t.Fatal(err)
	}
	out, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !out.Conversations.ShowHeadless {
		t.Error("ShowHeadless = false after saving true — round-trip lost the setting")
	}
}

// TestShowHeadless_MissingKeyKeepsHiddenDefault — a config.toml from
// before this field existed must keep the new "hide" behavior rather
// than accidentally opt the user in to noisy automation rows.
func TestShowHeadless_MissingKeyKeepsHiddenDefault(t *testing.T) {
	home := withFakeHome(t)
	p := filepath.Join(home, ".config", "ccmux", "config.toml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("theme = \"nord\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if out.Conversations.ShowHeadless {
		t.Error("a config without [conversations] should keep ShowHeadless=false")
	}
}
