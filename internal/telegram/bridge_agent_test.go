package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/daemon"
)

func claudeCatalog() daemon.AgentCommandsResponse {
	return daemon.AgentCommandsResponse{
		Agent: "claude",
		Commands: []daemon.AgentCommand{
			{Name: "/compact", Description: "Compact the conversation"},
			{Name: "/model", Description: "Switch model", TakesArg: true},
		},
	}
}

func TestAgent_ListsCatalogAsButtons(t *testing.T) {
	local := &fakeDaemon{agentCmds: map[string]daemon.AgentCommandsResponse{"build": claudeCatalog()}}
	b, bot := newTestBridge(Options{}, local, nil)

	b.handleUpdate(context.Background(), msgUpdate(7, "/agent local:build"))
	last, ok := bot.lastSent()
	if !ok {
		t.Fatal("no message sent")
	}
	if !strings.Contains(last.Text, "/compact") || !strings.Contains(last.Text, "/model") {
		t.Errorf("catalog body should list commands with descriptions: %q", last.Text)
	}
	if last.ReplyMarkup == nil {
		t.Fatal("expected an inline keyboard of commands")
	}
	// /compact is a no-arg send; /model takes an arg.
	var sawCompact, sawModelArg bool
	for _, row := range last.ReplyMarkup.InlineKeyboard {
		for _, btn := range row {
			if btn.CallbackData == encodeCB("acmd", "local:build", "/compact") {
				sawCompact = true
			}
			if btn.CallbackData == encodeCB("acmda", "local:build", "/model") {
				sawModelArg = true
			}
		}
	}
	if !sawCompact || !sawModelArg {
		t.Errorf("buttons wrong: compact=%v modelArg=%v", sawCompact, sawModelArg)
	}
}

func TestAgent_SendNoArgCommand(t *testing.T) {
	local := &fakeDaemon{agentCmds: map[string]daemon.AgentCommandsResponse{"build": claudeCatalog()}}
	b, _ := newTestBridge(Options{}, local, nil)

	b.handleUpdate(context.Background(), cbUpdate(7, encodeCB("acmd", "local:build", "/compact")))
	keys := local.recordedKeys()
	if len(keys) != 2 || keys[0] != (keyEvent{"build", "/compact"}) || keys[1] != (keyEvent{"build", "Enter"}) {
		t.Fatalf("expected /compact then Enter, got %+v", keys)
	}
}

func TestAgent_ArgCommandPromptsThenSends(t *testing.T) {
	local := &fakeDaemon{agentCmds: map[string]daemon.AgentCommandsResponse{"build": claudeCatalog()}}
	b, bot := newTestBridge(Options{}, local, nil)
	ctx := context.Background()

	// Tap /model (takes an arg): the bridge asks for the value.
	b.handleUpdate(ctx, cbUpdate(7, encodeCB("acmda", "local:build", "/model")))
	if !strings.Contains(strings.ToLower(bot.sentTexts()), "value for /model") {
		t.Fatalf("expected a prompt for the model value, got %q", bot.sentTexts())
	}
	if len(local.recordedKeys()) != 0 {
		t.Fatalf("nothing should be sent before the arg is supplied")
	}

	// Supply the value.
	b.handleUpdate(ctx, msgUpdate(7, "opus"))
	keys := local.recordedKeys()
	if len(keys) != 2 || keys[0] != (keyEvent{"build", "/model opus"}) {
		t.Fatalf("expected /model opus then Enter, got %+v", keys)
	}
}

func TestAgent_PromptOnlyAgent(t *testing.T) {
	local := &fakeDaemon{agentCmds: map[string]daemon.AgentCommandsResponse{
		"build": {Agent: "amp", Commands: nil},
	}}
	b, bot := newTestBridge(Options{}, local, nil)

	b.handleUpdate(context.Background(), msgUpdate(7, "/agent local:build"))
	if !strings.Contains(strings.ToLower(bot.sentTexts()), "prompt-only") {
		t.Fatalf("prompt-only agent should say so, got %q", bot.sentTexts())
	}
}

func TestPrompt_RoutesToCurrentSession(t *testing.T) {
	local := &fakeDaemon{previews: map[string]string{"build": "x"}}
	b, _ := newTestBridge(Options{}, local, nil)
	ctx := context.Background()

	// Preview sets the current session; a bare prompt then routes there.
	b.handleUpdate(ctx, msgUpdate(7, "/preview local:build"))
	b.handleUpdate(ctx, msgUpdate(7, "fix the failing test please"))
	keys := local.recordedKeys()
	if len(keys) != 2 || keys[0].Keys != "fix the failing test please" {
		t.Fatalf("bare prompt should reach the current session, got %+v", keys)
	}
}

func TestInlineAutocomplete_AgentAndCcmuxCommands(t *testing.T) {
	local := &fakeDaemon{agentCmds: map[string]daemon.AgentCommandsResponse{"build": claudeCatalog()}}
	b, bot := newTestBridge(Options{}, local, nil)
	// Make build the current session so agent commands are in scope.
	b.chats.setCurrent(7, Target{Host: LocalHost, Session: "build"})

	b.handleUpdate(context.Background(), inlineUpdate(7, "mod"))
	if len(bot.inline) == 0 {
		t.Fatal("no inline answer")
	}
	var sawModel bool
	for _, r := range bot.inline[len(bot.inline)-1].Results {
		if strings.Contains(r.Title, "/model") {
			sawModel = true
		}
	}
	if !sawModel {
		t.Errorf("inline query 'mod' should surface /model")
	}

	// A ccmux-command query surfaces /sessions.
	b.handleUpdate(context.Background(), inlineUpdate(7, "ses"))
	var sawSessions bool
	for _, r := range bot.inline[len(bot.inline)-1].Results {
		if strings.Contains(r.Title, "/sessions") {
			sawSessions = true
		}
	}
	if !sawSessions {
		t.Errorf("inline query 'ses' should surface /sessions")
	}
}

func TestControlTier_WorksWithoutExec(t *testing.T) {
	// Agent commands + prompts are control tier: they must work even
	// when allow_exec is false, while /run stays refused.
	local := &fakeDaemon{agentCmds: map[string]daemon.AgentCommandsResponse{"build": claudeCatalog()}}
	b, bot := newTestBridge(Options{AllowExec: false}, local, nil)
	ctx := context.Background()

	b.handleUpdate(ctx, cbUpdate(7, encodeCB("acmd", "local:build", "/compact")))
	if len(local.recordedKeys()) == 0 {
		t.Fatalf("agent command should work without allow_exec")
	}

	b.handleUpdate(ctx, msgUpdate(7, "/run local:build whatever"))
	if !strings.Contains(strings.ToLower(bot.sentTexts()), "disabled") {
		t.Fatalf("/run should still be refused")
	}
}
