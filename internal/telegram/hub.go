package telegram

import (
	"context"
	"fmt"
	"strconv"

	"github.com/skzv/ccmux/internal/daemon"
)

// This file is the interactive "hub": instead of plain text, the bot
// drives navigation with inline keyboards. /sessions becomes a tappable
// list (each row opens a per-session action menu), there's a main-menu
// button bar, and long lists paginate — all with no web app, just
// callbacks editing messages in place.

const sessionsPageSize = 8

// mainMenuKeyboard is the top-level button bar shown on /start and /menu.
func mainMenuKeyboard() *InlineKeyboardMarkup {
	return &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{
		{
			{Text: "📋 Sessions", CallbackData: encodeCB("menu", "sessions", "")},
			{Text: "📁 Projects", CallbackData: encodeCB("menu", "projects", "")},
		},
		{
			{Text: "📊 Usage", CallbackData: encodeCB("menu", "usage", "")},
			{Text: "❓ Help", CallbackData: encodeCB("menu", "help", "")},
		},
	}}
}

func (b *Bridge) cmdMenu(ctx context.Context, chatID int64) {
	b.send(ctx, SendMessageRequest{
		ChatID:      chatID,
		Text:        htmlBold("ccmux") + " — pick one:",
		ParseMode:   "HTML",
		ReplyMarkup: mainMenuKeyboard(),
	})
}

// sendWelcome greets a (re)paired chat with the help blurb + main menu.
func (b *Bridge) sendWelcome(ctx context.Context, chatID int64, prefix string) {
	b.send(ctx, SendMessageRequest{
		ChatID:      chatID,
		Text:        prefix + b.welcomeText(),
		ReplyMarkup: mainMenuKeyboard(),
	})
}

// sendSessionsList renders one page of the fan-out session list as a
// tappable keyboard. messageID==0 sends a new message; otherwise it edits
// in place (for pagination / back navigation).
func (b *Bridge) sendSessionsList(ctx context.Context, chatID, messageID int64, page int) {
	ss := b.router.AllSessions(ctx)
	if len(ss) == 0 {
		if messageID == 0 {
			b.reply(ctx, chatID, "No sessions running.")
		} else {
			b.editMessage(ctx, chatID, messageID, "No sessions running.", "", nil)
		}
		return
	}
	pages := (len(ss) + sessionsPageSize - 1) / sessionsPageSize
	if page < 0 {
		page = 0
	}
	if page >= pages {
		page = pages - 1
	}
	start := page * sessionsPageSize
	end := min(start+sessionsPageSize, len(ss))
	slice := ss[start:end]

	text, mode := formatSessions(slice)
	if pages > 1 {
		text += "\n\n" + fmt.Sprintf("page %d/%d — tap a session", page+1, pages)
	} else {
		text += "\n\ntap a session for actions"
	}
	kb := sessionsKeyboard(slice, page, pages)
	if messageID == 0 {
		b.send(ctx, SendMessageRequest{ChatID: chatID, Text: text, ParseMode: mode, ReplyMarkup: kb})
	} else {
		b.editMessage(ctx, chatID, messageID, text, mode, kb)
	}
}

func sessionsKeyboard(slice []daemon.SessionState, page, pages int) *InlineKeyboardMarkup {
	var rows [][]InlineKeyboardButton
	for _, s := range slice {
		t := Target{Host: s.Host, Session: s.Name}
		rows = append(rows, []InlineKeyboardButton{{
			Text:         stateGlyph(s.State) + " " + t.String(),
			CallbackData: encodeCB("smenu", t.String(), ""),
		}})
	}
	nav := []InlineKeyboardButton{}
	if page > 0 {
		nav = append(nav, InlineKeyboardButton{Text: "◀", CallbackData: encodeCB("spage", strconv.Itoa(page-1), "")})
	}
	nav = append(nav, InlineKeyboardButton{Text: "🔄", CallbackData: encodeCB("spage", strconv.Itoa(page), "")})
	if page < pages-1 {
		nav = append(nav, InlineKeyboardButton{Text: "▶", CallbackData: encodeCB("spage", strconv.Itoa(page+1), "")})
	}
	rows = append(rows, nav)
	return &InlineKeyboardMarkup{InlineKeyboard: rows}
}

// sessionActionMenu shows the per-session actions. Editing the list
// message in place keeps the chat tidy; a Back button returns to the list.
func (b *Bridge) sessionActionMenu(ctx context.Context, chatID, messageID int64, t Target) {
	b.chats.setCurrent(chatID, t)
	ts := t.String()
	text := htmlBold(ts) + " — choose an action"
	kb := &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{
		{
			{Text: "👁 Preview", CallbackData: encodeCB("prv", ts, "")},
			{Text: "🤖 Agent", CallbackData: encodeCB("agnt", ts, "")},
		},
		{
			{Text: "💬 Prompt", CallbackData: encodeCB("prompt", ts, "")},
			{Text: "⚠️ Kill", CallbackData: encodeCB("kilq", ts, "")},
		},
		{
			{Text: "⬅ Sessions", CallbackData: encodeCB("menu", "sessions", "")},
		},
	}}
	if messageID == 0 {
		b.send(ctx, SendMessageRequest{ChatID: chatID, Text: text, ParseMode: "HTML", ReplyMarkup: kb})
	} else {
		b.editMessage(ctx, chatID, messageID, text, "HTML", kb)
	}
}

// editMessage rewrites a message in place (best-effort).
func (b *Bridge) editMessage(ctx context.Context, chatID, messageID int64, text, mode string, kb *InlineKeyboardMarkup) {
	cctx, cancel := context.WithTimeout(ctx, actionDeadline)
	defer cancel()
	if err := b.bot.EditMessageText(cctx, EditMessageTextRequest{
		ChatID:      chatID,
		MessageID:   messageID,
		Text:        text,
		ParseMode:   mode,
		ReplyMarkup: kb,
	}); err != nil {
		b.logf("telegram: editMessageText: %v", err)
	}
}
