package telegram

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/daemon"
)

func cbUpdateMsg(chatID, msgID int64, data string) Update {
	u := cbUpdate(chatID, data)
	u.CallbackQuery.Message.MessageID = msgID
	return u
}

func kbHasData(kb *InlineKeyboardMarkup, data string) bool {
	if kb == nil {
		return false
	}
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			if btn.CallbackData == data {
				return true
			}
		}
	}
	return false
}

func TestHub_SessionsListIsTappable(t *testing.T) {
	local := &fakeDaemon{sessions: []daemon.SessionState{
		{Name: "build", State: "needs_input"},
		{Name: "api", State: "active"},
	}}
	b, bot := newTestBridge(Options{}, local, nil)

	b.handleUpdate(context.Background(), msgUpdate(7, "/sessions"))
	last, ok := bot.lastSent()
	if !ok || last.ReplyMarkup == nil {
		t.Fatal("/sessions should carry a keyboard")
	}
	if !kbHasData(last.ReplyMarkup, encodeCB("smenu", "local:build", "")) {
		t.Errorf("missing tappable button for local:build")
	}
	if !kbHasData(last.ReplyMarkup, encodeCB("smenu", "local:api", "")) {
		t.Errorf("missing tappable button for local:api")
	}
}

func TestHub_TapSessionOpensActionMenu(t *testing.T) {
	local := &fakeDaemon{sessions: []daemon.SessionState{{Name: "build", State: "active"}}}
	b, bot := newTestBridge(Options{}, local, nil)

	// Tap with a real message id → the list message is edited in place.
	b.handleUpdate(context.Background(), cbUpdateMsg(7, 5, encodeCB("smenu", "local:build", "")))
	if len(bot.edits) == 0 {
		t.Fatal("tapping a session should edit the message into an action menu")
	}
	kb := bot.edits[len(bot.edits)-1].ReplyMarkup
	for _, want := range []string{
		encodeCB("prv", "local:build", ""),
		encodeCB("agnt", "local:build", ""),
		encodeCB("prompt", "local:build", ""),
		encodeCB("kilq", "local:build", ""),
		encodeCB("menu", "sessions", ""),
	} {
		if !kbHasData(kb, want) {
			t.Errorf("action menu missing button %q", want)
		}
	}
}

func TestHub_MainMenuAndBack(t *testing.T) {
	local := &fakeDaemon{sessions: []daemon.SessionState{{Name: "build"}}}
	b, bot := newTestBridge(Options{}, local, nil)
	ctx := context.Background()

	b.handleUpdate(ctx, msgUpdate(7, "/menu"))
	last, _ := bot.lastSent()
	if !kbHasData(last.ReplyMarkup, encodeCB("menu", "sessions", "")) ||
		!kbHasData(last.ReplyMarkup, encodeCB("menu", "usage", "")) {
		t.Fatalf("main menu missing buttons: %+v", last.ReplyMarkup)
	}

	// Tapping "Sessions" from the menu edits the message into the list.
	b.handleUpdate(ctx, cbUpdateMsg(7, 9, encodeCB("menu", "sessions", "")))
	if len(bot.edits) == 0 || !kbHasData(bot.edits[len(bot.edits)-1].ReplyMarkup, encodeCB("smenu", "local:build", "")) {
		t.Errorf("menu→sessions should edit into the tappable list")
	}
}

func TestHub_Pagination(t *testing.T) {
	var ss []daemon.SessionState
	for i := 0; i < 10; i++ {
		ss = append(ss, daemon.SessionState{Name: fmt.Sprintf("s%d", i), State: "active"})
	}
	local := &fakeDaemon{sessions: ss}
	b, bot := newTestBridge(Options{}, local, nil)
	ctx := context.Background()

	// Page 0: a Next (page 1) button, no Prev.
	b.handleUpdate(ctx, msgUpdate(7, "/sessions"))
	p0, _ := bot.lastSent()
	if !kbHasData(p0.ReplyMarkup, encodeCB("spage", "1", "")) {
		t.Errorf("page 0 should offer a next-page button")
	}
	// 8 session buttons on page 0.
	count := 0
	for _, row := range p0.ReplyMarkup.InlineKeyboard {
		for _, btn := range row {
			if strings.HasPrefix(btn.CallbackData, "smenu|") {
				count++
			}
		}
	}
	if count != sessionsPageSize {
		t.Errorf("page 0 should show %d sessions, got %d", sessionsPageSize, count)
	}

	// Page 1: a Prev (page 0) button, no Next (page 2).
	b.handleUpdate(ctx, cbUpdateMsg(7, 5, encodeCB("spage", "1", "")))
	p1 := bot.edits[len(bot.edits)-1].ReplyMarkup
	if !kbHasData(p1, encodeCB("spage", "0", "")) {
		t.Errorf("page 1 should offer a prev-page button")
	}
	if kbHasData(p1, encodeCB("spage", "2", "")) {
		t.Errorf("page 1 (last) should not offer a next-page button")
	}
}

func TestHub_PromptCallbackSetsCurrent(t *testing.T) {
	local := &fakeDaemon{sessions: []daemon.SessionState{{Name: "build"}}}
	b, bot := newTestBridge(Options{}, local, nil)
	ctx := context.Background()

	b.handleUpdate(ctx, cbUpdate(7, encodeCB("prompt", "local:build", "")))
	if !strings.Contains(strings.ToLower(bot.sentTexts()), "type your prompt") {
		t.Errorf("prompt button should ask for the prompt text")
	}
	// The next bare message routes to the chosen session.
	b.handleUpdate(ctx, msgUpdate(7, "fix the build"))
	keys := local.recordedKeys()
	if len(keys) < 1 || keys[0].Name != "build" || keys[0].Keys != "fix the build" {
		t.Errorf("prompt should route to the tapped session, got %+v", keys)
	}
}

func TestHub_PairingShowsMenu(t *testing.T) {
	allow := map[int64]bool{}
	opts := Options{
		Allowed: func(id int64) bool { return allow[id] },
		Enroll:  func(id int64) error { allow[id] = true; return nil },
	}
	b, bot := newTestBridge(opts, &fakeDaemon{}, nil)
	ctx := context.Background()

	code := b.NewPairingCode()
	b.handleUpdate(ctx, msgUpdate(99, "/start "+code))
	last, ok := bot.lastSent()
	if !ok || !kbHasData(last.ReplyMarkup, encodeCB("menu", "sessions", "")) {
		t.Errorf("pairing should greet with the main menu, got %+v", last.ReplyMarkup)
	}
}
