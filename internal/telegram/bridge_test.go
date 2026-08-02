package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/daemon"
)

// --- drivers ---------------------------------------------------------

func msgUpdate(chatID int64, text string) Update {
	return Update{Message: &Message{Chat: Chat{ID: chatID}, From: &User{ID: chatID}, Text: text}}
}

func replyUpdate(chatID, replyToID int64, text string) Update {
	u := msgUpdate(chatID, text)
	u.Message.ReplyTo = &Message{MessageID: replyToID}
	return u
}

func cbUpdate(chatID int64, data string) Update {
	return Update{CallbackQuery: &CallbackQuery{
		ID:      "cb",
		From:    User{ID: chatID},
		Message: &Message{Chat: Chat{ID: chatID}},
		Data:    data,
	}}
}

func inlineUpdate(chatID int64, query string) Update {
	return Update{InlineQuery: &InlineQuery{ID: "iq", From: User{ID: chatID}, Query: query}}
}

func newTestBridge(opts Options, local *fakeDaemon, peers map[string]DaemonClient) (*Bridge, *fakeBot) {
	bot := &fakeBot{}
	if opts.Allowed == nil {
		opts.Allowed = func(id int64) bool { return id == 7 }
	}
	if opts.Recipients == nil {
		opts.Recipients = func() []int64 { return []int64{7} }
	}
	return New(bot, NewRouter(local, peers), opts), bot
}

// --- auth ------------------------------------------------------------

func TestAuthGate_RejectsUnknownChat(t *testing.T) {
	local := &fakeDaemon{sessions: []daemon.SessionState{{Name: "build"}}}
	b, bot := newTestBridge(Options{}, local, nil)

	b.handleUpdate(context.Background(), msgUpdate(9, "/sessions"))
	if len(bot.sent) != 0 {
		t.Fatalf("non-allowlisted chat got a reply: %+v", bot.sent)
	}
}

func TestAuthGate_AllowsEnrolledChat(t *testing.T) {
	local := &fakeDaemon{sessions: []daemon.SessionState{{Name: "build", State: "active"}}}
	b, bot := newTestBridge(Options{}, local, nil)

	b.handleUpdate(context.Background(), msgUpdate(7, "/sessions"))
	if got := bot.sentTexts(); !strings.Contains(got, "build") {
		t.Fatalf("allowlisted /sessions didn't list build: %q", got)
	}
}

// --- read tier -------------------------------------------------------

func TestSessions_FanOutAcrossHosts(t *testing.T) {
	local := &fakeDaemon{sessions: []daemon.SessionState{{Name: "build", State: "active"}}}
	mini := &fakeDaemon{sessions: []daemon.SessionState{{Name: "api", State: "needs_input"}}}
	b, bot := newTestBridge(Options{}, local, map[string]DaemonClient{"mini": mini})

	b.handleUpdate(context.Background(), msgUpdate(7, "/sessions"))
	got := bot.sentTexts()
	if !strings.Contains(got, "local:build") || !strings.Contains(got, "mini:api") {
		t.Fatalf("fan-out list missing a host:session: %q", got)
	}
}

func TestPreview_RoutesToOwningHost(t *testing.T) {
	local := &fakeDaemon{previews: map[string]string{"api": "LOCALPANE"}}
	mini := &fakeDaemon{previews: map[string]string{"api": "PEERPANE"}}
	b, bot := newTestBridge(Options{}, local, map[string]DaemonClient{"mini": mini})

	b.handleUpdate(context.Background(), msgUpdate(7, "/preview mini:api 10"))
	got := bot.sentTexts()
	if !strings.Contains(got, "PEERPANE") {
		t.Fatalf("preview should come from the peer, got %q", got)
	}
	if strings.Contains(got, "LOCALPANE") {
		t.Fatalf("preview wrongly used the local daemon")
	}
}

// --- control tier ----------------------------------------------------

func TestSend_DeliversKeysThenEnter(t *testing.T) {
	local := &fakeDaemon{}
	b, _ := newTestBridge(Options{}, local, nil)

	b.handleUpdate(context.Background(), msgUpdate(7, "/send local:build make test"))
	keys := local.recordedKeys()
	if len(keys) != 2 || keys[0] != (keyEvent{"build", "make test"}) || keys[1] != (keyEvent{"build", "Enter"}) {
		t.Fatalf("send should deliver text then Enter, got %+v", keys)
	}
}

func TestKill_RequiresConfirmation(t *testing.T) {
	local := &fakeDaemon{}
	b, bot := newTestBridge(Options{}, local, nil)
	ctx := context.Background()

	b.handleUpdate(ctx, msgUpdate(7, "/kill local:build"))
	if len(local.killed) != 0 {
		t.Fatalf("kill must not fire before confirmation")
	}
	last, _ := bot.lastSent()
	if last.ReplyMarkup == nil || last.ReplyMarkup.InlineKeyboard[0][0].CallbackData != encodeCB("kill", "local:build", "confirm") {
		t.Fatalf("expected a confirm button, got %+v", last.ReplyMarkup)
	}

	// Tap confirm.
	b.handleUpdate(ctx, cbUpdate(7, encodeCB("kill", "local:build", "confirm")))
	if len(local.killed) != 1 || local.killed[0] != "build" {
		t.Fatalf("confirmed kill didn't fire: %+v", local.killed)
	}
}

func TestNew_SpawnsSession(t *testing.T) {
	local := &fakeDaemon{}
	b, _ := newTestBridge(Options{}, local, nil)

	b.handleUpdate(context.Background(), msgUpdate(7, "/new ccmux claude"))
	if len(local.newReqs) != 1 || local.newReqs[0].Project != "ccmux" || local.newReqs[0].Agent != "claude" {
		t.Fatalf("new session request wrong: %+v", local.newReqs)
	}
}

// --- exec gate -------------------------------------------------------

func TestRun_RefusedByDefault(t *testing.T) {
	local := &fakeDaemon{}
	b, bot := newTestBridge(Options{AllowExec: false}, local, nil)

	b.handleUpdate(context.Background(), msgUpdate(7, "/run local:build rm -rf build"))
	if len(local.recordedKeys()) != 0 {
		t.Fatalf("exec tier off must not send keys")
	}
	if !strings.Contains(strings.ToLower(bot.sentTexts()), "exec tier is disabled") {
		t.Fatalf("expected a refusal message, got %q", bot.sentTexts())
	}
}

func TestRun_AllowedWhenOptedIn(t *testing.T) {
	local := &fakeDaemon{}
	b, _ := newTestBridge(Options{AllowExec: true}, local, nil)

	b.handleUpdate(context.Background(), msgUpdate(7, "/run local:build make test"))
	keys := local.recordedKeys()
	if len(keys) != 2 || keys[0].Keys != "make test" {
		t.Fatalf("exec on should deliver the command, got %+v", keys)
	}
}

// --- command menu tiers ---------------------------------------------

func TestSetMyCommands_ReflectsTiers(t *testing.T) {
	local := &fakeDaemon{}
	ctx := context.Background()

	off, botOff := newTestBridge(Options{AllowExec: false}, local, nil)
	_ = off.installCommandMenu(ctx)
	if hasBotCommand(botOff.commands[0], "run") {
		t.Errorf("/run must not be registered when allow_exec is off")
	}
	if !hasBotCommand(botOff.commands[0], "sessions") {
		t.Errorf("/sessions should always be registered")
	}

	on, botOn := newTestBridge(Options{AllowExec: true}, local, nil)
	_ = on.installCommandMenu(ctx)
	if !hasBotCommand(botOn.commands[0], "run") {
		t.Errorf("/run should be registered when allow_exec is on")
	}
}

func hasBotCommand(cmds []BotCommand, name string) bool {
	for _, c := range cmds {
		if c.Command == name {
			return true
		}
	}
	return false
}

// --- pairing ---------------------------------------------------------

func TestPairing_EnrollsWithValidCode(t *testing.T) {
	local := &fakeDaemon{}
	enrolled := map[int64]bool{}
	allow := map[int64]bool{}
	opts := Options{
		Allowed: func(id int64) bool { return allow[id] },
		Enroll: func(id int64) error {
			enrolled[id] = true
			allow[id] = true
			return nil
		},
	}
	b, bot := newTestBridge(opts, local, nil)
	ctx := context.Background()

	code := b.NewPairingCode()
	b.handleUpdate(ctx, msgUpdate(99, "/start "+code))
	if !enrolled[99] {
		t.Fatalf("valid code should enroll the chat")
	}
	if !strings.Contains(strings.ToLower(bot.sentTexts()), "paired") {
		t.Fatalf("expected a paired confirmation, got %q", bot.sentTexts())
	}
}

func TestPairing_RejectsBadCode(t *testing.T) {
	local := &fakeDaemon{}
	enrolled := map[int64]bool{}
	opts := Options{
		Allowed: func(id int64) bool { return false },
		Enroll:  func(id int64) error { enrolled[id] = true; return nil },
	}
	b, _ := newTestBridge(opts, local, nil)

	b.handleUpdate(context.Background(), msgUpdate(99, "/start WRONGCODE"))
	if enrolled[99] {
		t.Fatalf("an invalid code must not enroll")
	}
}

func TestPairing_CodeIsSingleUseAndExpires(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	p := newPairingStore(func() time.Time { return now })

	code := p.mint(10 * time.Minute)
	if !p.consume(code) {
		t.Fatalf("fresh code should consume")
	}
	if p.consume(code) {
		t.Fatalf("code must be single-use")
	}

	code2 := p.mint(1 * time.Minute)
	now = now.Add(2 * time.Minute) // past expiry
	if p.consume(code2) {
		t.Fatalf("expired code must not consume")
	}
}

func TestSafeNotePath(t *testing.T) {
	good := []string{"docs/x.md", "a.md", "docs/01_Specs/Vision.md"}
	bad := []string{"../etc.md", "/etc/passwd", `..\x.md`, "docs/../../x.md", "a.txt", ""}
	for _, g := range good {
		if !safeNotePath(g) {
			t.Errorf("safeNotePath(%q) = false, want true", g)
		}
	}
	for _, bd := range bad {
		if safeNotePath(bd) {
			t.Errorf("safeNotePath(%q) = true, want false", bd)
		}
	}
}
