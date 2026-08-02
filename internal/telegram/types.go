// Package telegram is a minimal Telegram Bot API client plus the ccmux
// "bridge" subsystem that turns a Telegram bot into a remote control
// surface for ccmux sessions across the tailnet.
//
// The client (this file + client.go) is deliberately small: only the
// handful of Bot API methods the bridge uses, over net/http, behind an
// injectable transport so the whole bridge is testable with no network.
// It mirrors the hand-rolled internal/daemon client rather than pulling
// in a third-party bot framework — see openspec/changes/telegram-control.
//
// Only the subset of each Bot API object ccmux reads is modeled. Fields
// we never touch are intentionally omitted so the surface stays auditable.
package telegram

// Update is one entry returned by getUpdates. Exactly one of the
// optional payload pointers is set per update (we only model the three
// kinds the bridge handles: messages, button taps, inline queries).
type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message,omitempty"`
	CallbackQuery *CallbackQuery `json:"callback_query,omitempty"`
	InlineQuery   *InlineQuery   `json:"inline_query,omitempty"`
}

// User is a Telegram user or bot.
type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	Username  string `json:"username,omitempty"`
	FirstName string `json:"first_name,omitempty"`
}

// Chat is the conversation a message belongs to. For a private chat the
// ID equals the user's ID; ccmux's allowlist is keyed on this ID.
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// Message is an incoming or sent message. ReplyTo lets the bridge
// attribute a bare "y"/"n" quick-reply to the alert it answers.
type Message struct {
	MessageID int64    `json:"message_id"`
	From      *User    `json:"from,omitempty"`
	Chat      Chat     `json:"chat"`
	Date      int64    `json:"date"`
	Text      string   `json:"text,omitempty"`
	ReplyTo   *Message `json:"reply_to_message,omitempty"`
}

// CallbackQuery is the event delivered when a user taps an inline
// keyboard button. Data carries whatever we packed into the button.
type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message,omitempty"`
	Data    string   `json:"data,omitempty"`
}

// InlineQuery is the event delivered as the user types "@bot <text>"
// in any chat; the bridge answers it with command-catalog matches.
type InlineQuery struct {
	ID    string `json:"id"`
	From  User   `json:"from"`
	Query string `json:"query"`
}

// ChatID returns the chat the update originated from, for any of the
// three update kinds, and whether one was present. Centralized so the
// allowlist check has a single source of truth.
func (u Update) ChatID() (int64, bool) {
	switch {
	case u.Message != nil:
		return u.Message.Chat.ID, true
	case u.CallbackQuery != nil && u.CallbackQuery.Message != nil:
		return u.CallbackQuery.Message.Chat.ID, true
	case u.CallbackQuery != nil:
		return u.CallbackQuery.From.ID, true
	case u.InlineQuery != nil:
		return u.InlineQuery.From.ID, true
	}
	return 0, false
}

// WebAppInfo is the target of a web_app inline button (the optional
// tailnet markdown viewer).
type WebAppInfo struct {
	URL string `json:"url"`
}

// InlineKeyboardButton is one tappable button. Exactly one action field
// is set: CallbackData (delivered back as a CallbackQuery), URL (opens
// in the in-app browser), or WebApp (a Mini App).
type InlineKeyboardButton struct {
	Text         string      `json:"text"`
	CallbackData string      `json:"callback_data,omitempty"`
	URL          string      `json:"url,omitempty"`
	WebApp       *WebAppInfo `json:"web_app,omitempty"`
}

// InlineKeyboardMarkup is a grid of buttons attached under a message.
type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

// BotCommand is one entry in the composer's "/" command menu, set via
// setMyCommands.
type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// InputTextMessageContent is the message an inline-query result inserts
// when chosen.
type InputTextMessageContent struct {
	MessageText string `json:"message_text"`
}

// InlineQueryResultArticle is the one inline-result kind ccmux uses: a
// titled, described row that, when picked, sends MessageText. Type is
// always "article"; NewInlineArticle sets it.
type InlineQueryResultArticle struct {
	Type                string                  `json:"type"`
	ID                  string                  `json:"id"`
	Title               string                  `json:"title"`
	Description         string                  `json:"description,omitempty"`
	InputMessageContent InputTextMessageContent `json:"input_message_content"`
}

// NewInlineArticle builds an article result with the required Type set.
func NewInlineArticle(id, title, description, messageText string) InlineQueryResultArticle {
	return InlineQueryResultArticle{
		Type:                "article",
		ID:                  id,
		Title:               title,
		Description:         description,
		InputMessageContent: InputTextMessageContent{MessageText: messageText},
	}
}

// SendMessageRequest is the payload for sendMessage.
type SendMessageRequest struct {
	ChatID      int64                 `json:"chat_id"`
	Text        string                `json:"text"`
	ParseMode   string                `json:"parse_mode,omitempty"`
	ReplyMarkup *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
	ReplyTo     int64                 `json:"reply_to_message_id,omitempty"`
}

// EditMessageTextRequest is the payload for editMessageText; used to
// rewrite an alert in place after it's been approved/denied.
type EditMessageTextRequest struct {
	ChatID      int64                 `json:"chat_id"`
	MessageID   int64                 `json:"message_id"`
	Text        string                `json:"text"`
	ParseMode   string                `json:"parse_mode,omitempty"`
	ReplyMarkup *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
}
