package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/telegram"
)

func TestRedactToken(t *testing.T) {
	if got := redactToken(""); got != "(not set)" {
		t.Errorf("empty = %q", got)
	}
	got := redactToken("123456:ABCDEF")
	if strings.Contains(got, "123456") || !strings.HasSuffix(got, "CDEF)") {
		t.Errorf("redactToken leaked or mis-masked: %q", got)
	}
	if got := redactToken("ab"); !strings.HasPrefix(got, "set") || strings.Contains(got, "ab") {
		t.Errorf("short token should still be masked: %q", got)
	}
}

// TestAgentsCommands_LocalCatalogJSON exercises `ccmux agents commands
// --agent claude --json` end-to-end (no daemon, no network): it resolves
// the local catalog and emits the protocol shape.
func TestAgentsCommands_LocalCatalogJSON(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // isolate ~/.claude so no user commands leak in

	cmd := newAgentsCommandsCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"--agent", "claude", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	var resp daemon.AgentCommandsResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, buf.String())
	}
	if resp.Agent != "claude" {
		t.Errorf("agent = %q, want claude", resp.Agent)
	}
	var sawModel bool
	for _, c := range resp.Commands {
		if c.Name == "/model" {
			sawModel = true
			if !c.TakesArg {
				t.Errorf("/model should take an arg")
			}
		}
	}
	if !sawModel {
		t.Errorf("claude catalog should include /model")
	}
}

func TestAgentsCommands_UnknownAgent(t *testing.T) {
	cmd := newAgentsCommandsCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"--agent", "nope"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected an error for an unknown agent")
	}
}

func joinDoctor(lines []string) string { return strings.Join(lines, "\n") }

func TestTelegramDoctor_NotConfigured(t *testing.T) {
	cfg := config.Defaults() // telegram disabled
	lines, bad := telegramDoctorReport(cfg)
	if bad != 0 {
		t.Errorf("not-configured should not count as bad, got %d", bad)
	}
	if !strings.Contains(joinDoctor(lines), "not configured") {
		t.Errorf("expected 'not configured', got %q", joinDoctor(lines))
	}
}

func TestTelegramDoctor_GoodToken(t *testing.T) {
	orig := telegramValidate
	defer func() { telegramValidate = orig }()
	telegramValidate = func(_ context.Context, _ string) (*telegram.User, error) {
		return &telegram.User{Username: "ccmuxbot"}, nil
	}
	cfg := config.Defaults()
	cfg.Telegram.Enabled = true
	cfg.Telegram.BotToken = "123:abc"
	cfg.Telegram.AllowedChatIDs = []int64{7}
	cfg.Telegram.AllowExec = true

	lines, bad := telegramDoctorReport(cfg)
	out := joinDoctor(lines)
	if bad != 0 {
		t.Errorf("valid token should be clean, got bad=%d", bad)
	}
	if !strings.Contains(out, "@ccmuxbot") || !strings.Contains(out, "1 chat") {
		t.Errorf("missing token/paired report: %q", out)
	}
	if !strings.Contains(out, "exec tier") {
		t.Errorf("should warn about the enabled exec tier: %q", out)
	}
	// The token must never appear in doctor output.
	if strings.Contains(out, "123:abc") {
		t.Errorf("doctor leaked the token: %q", out)
	}
}

func TestTelegramDoctor_RejectedAndConflict(t *testing.T) {
	orig := telegramValidate
	defer func() { telegramValidate = orig }()

	cfg := config.Defaults()
	cfg.Telegram.Enabled = true
	cfg.Telegram.BotToken = "bad"

	telegramValidate = func(_ context.Context, _ string) (*telegram.User, error) {
		return nil, &telegram.APIError{Method: "getMe", Code: 401, Description: "Unauthorized"}
	}
	lines, bad := telegramDoctorReport(cfg)
	if bad != 1 || !strings.Contains(joinDoctor(lines), "rejected") {
		t.Errorf("401 should be a bad/rejected report: bad=%d %q", bad, joinDoctor(lines))
	}

	telegramValidate = func(_ context.Context, _ string) (*telegram.User, error) {
		return nil, &telegram.APIError{Method: "getUpdates", Code: 409, Description: "Conflict"}
	}
	lines, bad = telegramDoctorReport(cfg)
	if bad != 1 || !strings.Contains(joinDoctor(lines), "already polling") {
		t.Errorf("409 should be a bad/conflict report: bad=%d %q", bad, joinDoctor(lines))
	}
}
