package config

import (
	"testing"
)

// TestTelegramConfig_RoundTrip is the load-bearing guarantee for the
// pairing flow: enrolling a chat (Save) and re-reading (Load) must
// persist the Telegram fields without clobbering unrelated sections.
func TestTelegramConfig_RoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := Defaults()
	cfg.Theme = "dracula"
	cfg.Hosts = []Host{{Name: "mini", Address: "100.64.0.2"}}
	cfg.OpenRouter.Enabled = true
	cfg.OpenRouter.APIKey = "or-key"
	cfg.Telegram.Enabled = true
	cfg.Telegram.BotToken = "123:abc"
	cfg.Telegram.AllowedChatIDs = []int64{7, 42}
	cfg.Telegram.AllowExec = true

	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !got.Telegram.Enabled || got.Telegram.BotToken != "123:abc" {
		t.Errorf("telegram enable/token not persisted: %+v", got.Telegram)
	}
	if len(got.Telegram.AllowedChatIDs) != 2 || got.Telegram.AllowedChatIDs[0] != 7 || got.Telegram.AllowedChatIDs[1] != 42 {
		t.Errorf("allowlist not persisted: %+v", got.Telegram.AllowedChatIDs)
	}
	if !got.Telegram.AllowExec {
		t.Errorf("allow_exec not persisted")
	}
	// Unrelated sections survive the write.
	if got.Theme != "dracula" {
		t.Errorf("theme clobbered: %q", got.Theme)
	}
	if len(got.Hosts) != 1 || got.Hosts[0].Name != "mini" {
		t.Errorf("hosts clobbered: %+v", got.Hosts)
	}
	if !got.OpenRouter.Enabled || got.OpenRouter.APIKey != "or-key" {
		t.Errorf("openrouter clobbered: %+v", got.OpenRouter)
	}
}

func TestTelegramConfig_AllowAndSet(t *testing.T) {
	tc := TelegramConfig{AllowedChatIDs: []int64{1, 2, 3}}
	if !tc.Allows(2) {
		t.Errorf("Allows(2) should be true")
	}
	if tc.Allows(99) {
		t.Errorf("Allows(99) should be false")
	}
	set := tc.AllowedChatIDSet()
	if !set[1] || !set[3] || set[4] {
		t.Errorf("AllowedChatIDSet wrong: %+v", set)
	}
	// Empty allowlist allows no one and yields a nil set.
	empty := TelegramConfig{}
	if empty.Allows(1) {
		t.Errorf("empty allowlist must allow no one")
	}
	if empty.AllowedChatIDSet() != nil {
		t.Errorf("empty allowlist set should be nil")
	}
}

func TestTelegramConfig_EffectivePaneTailLines(t *testing.T) {
	if got := (TelegramConfig{}).EffectivePaneTailLines(); got != DefaultPaneTailLines {
		t.Errorf("unset = %d, want default %d", got, DefaultPaneTailLines)
	}
	if got := (TelegramConfig{PaneTailLines: 60}).EffectivePaneTailLines(); got != 60 {
		t.Errorf("set = %d, want 60", got)
	}
	// Defaults() ships a non-zero cap.
	if Defaults().Telegram.PaneTailLines != DefaultPaneTailLines {
		t.Errorf("Defaults pane tail = %d, want %d", Defaults().Telegram.PaneTailLines, DefaultPaneTailLines)
	}
}
