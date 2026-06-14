package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/daemon"
)

func TestNotify_AlertCarriesApproveDeny(t *testing.T) {
	local := &fakeDaemon{}
	b, bot := newTestBridge(Options{}, local, nil)

	b.Notify(context.Background(), "local", "build", "Allow edit to main.go? (y/n)", "c1")
	last, ok := bot.lastSent()
	if !ok {
		t.Fatal("no alert sent")
	}
	if !strings.Contains(last.Text, "build") {
		t.Errorf("alert should name the session: %q", last.Text)
	}
	if last.ReplyMarkup == nil {
		t.Fatal("alert should carry buttons")
	}
	var approve, deny bool
	for _, row := range last.ReplyMarkup.InlineKeyboard {
		for _, btn := range row {
			if btn.CallbackData == encodeCB("apr", "local:build", "c1") {
				approve = true
			}
			if btn.CallbackData == encodeCB("dny", "local:build", "c1") {
				deny = true
			}
		}
	}
	if !approve || !deny {
		t.Errorf("alert missing approve/deny buttons")
	}
}

func TestApprove_ViaButton(t *testing.T) {
	local := &fakeDaemon{}
	b, bot := newTestBridge(Options{}, local, nil)
	ctx := context.Background()

	b.Notify(ctx, "local", "build", "pane", "c1")
	b.handleUpdate(ctx, cbUpdate(7, encodeCB("apr", "local:build", "c1")))

	keys := local.recordedKeys()
	if len(keys) != 1 || keys[0] != (keyEvent{"build", "Enter"}) {
		t.Fatalf("approve should send the accept keystroke (Enter), got %+v", keys)
	}
	if len(bot.edits) == 0 || !strings.Contains(strings.ToLower(bot.edits[0].Text), "approved") {
		t.Fatalf("alert should be edited to 'approved', got %+v", bot.edits)
	}
	// Second tap is a no-op: already handled.
	b.handleUpdate(ctx, cbUpdate(7, encodeCB("apr", "local:build", "c1")))
	if len(local.recordedKeys()) != 1 {
		t.Fatalf("second approve must not re-send keys")
	}
	if last := bot.answers[len(bot.answers)-1]; !strings.Contains(strings.ToLower(last.Text), "already") {
		t.Errorf("second tap should report already-handled, got %q", last.Text)
	}
}

func TestApprove_ViaQuickReplyText(t *testing.T) {
	local := &fakeDaemon{}
	b, _ := newTestBridge(Options{}, local, nil)
	ctx := context.Background()

	b.Notify(ctx, "local", "build", "pane", "c1")
	// A plain "approve" with a single outstanding alert (no reply-to).
	b.handleUpdate(ctx, msgUpdate(7, "approve"))
	keys := local.recordedKeys()
	if len(keys) != 1 || keys[0] != (keyEvent{"build", "Enter"}) {
		t.Fatalf("quick-reply approve should accept, got %+v", keys)
	}
}

func TestDeny_SendsDecline(t *testing.T) {
	local := &fakeDaemon{}
	b, bot := newTestBridge(Options{}, local, nil)
	ctx := context.Background()

	b.Notify(ctx, "local", "build", "pane", "c1")
	b.handleUpdate(ctx, cbUpdate(7, encodeCB("dny", "local:build", "c1")))
	keys := local.recordedKeys()
	if len(keys) != 1 || keys[0] != (keyEvent{"build", "Escape"}) {
		t.Fatalf("deny should send the decline keystroke (Escape), got %+v", keys)
	}
	if len(bot.edits) == 0 || !strings.Contains(strings.ToLower(bot.edits[0].Text), "denied") {
		t.Fatalf("alert should be edited to 'denied'")
	}
}

func TestNotify_DedupOneAlertPerBlock(t *testing.T) {
	local := &fakeDaemon{}
	b, bot := newTestBridge(Options{}, local, nil)
	ctx := context.Background()

	b.Notify(ctx, "local", "build", "pane", "c1")
	b.Notify(ctx, "local", "build", "pane", "c1") // same block
	b.Notify(ctx, "local", "build", "pane", "c2") // still unresolved → deduped by target
	if len(bot.sent) != 1 {
		t.Fatalf("expected exactly one alert, got %d", len(bot.sent))
	}
}

func TestNotify_MutedSuppressesAlert(t *testing.T) {
	local := &fakeDaemon{}
	b, bot := newTestBridge(Options{Muted: func() bool { return true }}, local, nil)

	b.Notify(context.Background(), "local", "build", "pane", "c1")
	if len(bot.sent) != 0 {
		t.Fatalf("muted bridge must not alert")
	}
}

func TestMarkSeen_ResolvesAlert(t *testing.T) {
	local := &fakeDaemon{}
	b, bot := newTestBridge(Options{}, local, nil)
	ctx := context.Background()

	b.Notify(ctx, "local", "build", "pane", "c1")
	b.MarkSeen(ctx, "local", "build")
	if len(bot.edits) == 0 || !strings.Contains(strings.ToLower(bot.edits[0].Text), "handled") {
		t.Fatalf("seen-elsewhere should edit the alert, got %+v", bot.edits)
	}
	// A subsequent approve tap is a no-op.
	b.handleUpdate(ctx, cbUpdate(7, encodeCB("apr", "local:build", "c1")))
	if len(local.recordedKeys()) != 0 {
		t.Fatalf("approving a seen-resolved alert must not send keys")
	}
}

func TestMultiSession_QuickReplyDisambiguation(t *testing.T) {
	local := &fakeDaemon{}
	b, bot := newTestBridge(Options{}, local, nil)
	ctx := context.Background()

	b.Notify(ctx, "local", "build", "pane", "c1") // message id 1
	b.Notify(ctx, "local", "api", "pane", "c2")   // message id 2

	// Bare "y" with two outstanding → ask which, send no keys.
	b.handleUpdate(ctx, msgUpdate(7, "y"))
	if len(local.recordedKeys()) != 0 {
		t.Fatalf("ambiguous quick-reply must not act")
	}
	if !strings.Contains(strings.ToLower(bot.sentTexts()), "which session") {
		t.Fatalf("ambiguous quick-reply should ask which session, got %q", bot.sentTexts())
	}

	// Reply to build's alert (message id 1) → approves only build.
	b.handleUpdate(ctx, replyUpdate(7, 1, "y"))
	keys := local.recordedKeys()
	if len(keys) != 1 || keys[0].Name != "build" {
		t.Fatalf("reply-to should approve only build, got %+v", keys)
	}
}

func TestNotify_NoRecipientsNoSend(t *testing.T) {
	local := &fakeDaemon{}
	b, bot := newTestBridge(Options{Recipients: func() []int64 { return nil }}, local, nil)
	b.Notify(context.Background(), "local", "build", "pane", "c1")
	if len(bot.sent) != 0 {
		t.Fatalf("no recipients → no alert")
	}
}

func TestNotes_ListAndSendDocument(t *testing.T) {
	local := &fakeDaemon{
		notes:      map[string][]daemon.NoteEntry{"ccmux": {{Rel: "docs/vision.md", Display: "Vision"}}},
		noteBodies: map[string]string{"ccmux/docs/vision.md": "# Vision\nthe why"},
	}
	b, bot := newTestBridge(Options{}, local, nil)
	ctx := context.Background()

	b.handleUpdate(ctx, msgUpdate(7, "/notes ccmux"))
	last, _ := bot.lastSent()
	if last.ReplyMarkup == nil || last.ReplyMarkup.InlineKeyboard[0][0].CallbackData != encodeCB("note", "0", "") {
		t.Fatalf("notes list should offer an indexed button, got %+v", last.ReplyMarkup)
	}

	b.handleUpdate(ctx, cbUpdate(7, encodeCB("note", "0", "")))
	if len(bot.docs) != 1 {
		t.Fatalf("selecting a note should send a document, got %d", len(bot.docs))
	}
	if bot.docs[0].Filename != "vision.md" || !strings.Contains(string(bot.docs[0].Content), "the why") {
		t.Fatalf("wrong document sent: %+v", bot.docs[0])
	}
}

func TestNotes_Search(t *testing.T) {
	local := &fakeDaemon{
		searchHits: map[string][]daemon.SearchHit{"ccmux": {{Rel: "docs/net.md", LineNum: 3, Snippet: "tailnet"}}},
	}
	b, bot := newTestBridge(Options{}, local, nil)

	b.handleUpdate(context.Background(), msgUpdate(7, "/notes ccmux tailnet"))
	last, _ := bot.lastSent()
	if last.ReplyMarkup == nil || last.ReplyMarkup.InlineKeyboard[0][0].Text != "docs/net.md" {
		t.Fatalf("search should list matching files, got %+v", last.ReplyMarkup)
	}
}
