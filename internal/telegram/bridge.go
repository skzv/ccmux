package telegram

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// BotAPI is the subset of *Client the bridge uses. An interface so the
// whole bridge runs against a fake in tests, with no network.
type BotAPI interface {
	GetMe(ctx context.Context) (*User, error)
	GetUpdates(ctx context.Context, offset int64, timeoutSecs int) ([]Update, error)
	SendMessage(ctx context.Context, req SendMessageRequest) (*Message, error)
	EditMessageText(ctx context.Context, req EditMessageTextRequest) error
	AnswerCallbackQuery(ctx context.Context, id, text string, showAlert bool) error
	AnswerInlineQuery(ctx context.Context, id string, results []InlineQueryResultArticle, cacheTimeSecs int) error
	SetMyCommands(ctx context.Context, cmds []BotCommand) error
	SendDocument(ctx context.Context, chatID int64, filename string, content []byte, caption string) (*Message, error)
}

const (
	// pollTimeoutSecs is the server-side long-poll hold; pollHTTPSlack
	// is added to the request deadline so the HTTP call always outlasts
	// the server hold.
	pollTimeoutSecs = 30
	pollHTTPSlack   = 10 * time.Second

	maxBackoff     = 30 * time.Second
	pairingCodeTTL = 10 * time.Minute
	actionDeadline = 8 * time.Second
	spawnDeadline  = 30 * time.Second
)

// Options configures a Bridge. The callbacks decouple the bridge from
// config/persistence: the ccmuxd wiring supplies closures over the live
// allowlist and config save, so the telegram package stays free of
// config and filesystem dependencies (and trivially testable).
type Options struct {
	// AllowExec opens the arbitrary-exec tier (`/run`). Off by default.
	AllowExec bool
	// PaneTailLines caps pane content shipped in alerts/previews.
	PaneTailLines int
	// Allowed reports whether a chat id may drive the bot (the
	// allowlist). Required; a nil Allowed denies everyone.
	Allowed func(chatID int64) bool
	// Recipients enumerates the allowlist for proactive alerts (the
	// bridge can't fan a needs-input alert out from a predicate alone).
	// nil/empty means no one is alerted.
	Recipients func() []int64
	// Muted reports whether proactive alerts are currently silenced.
	// Optional; nil means never muted.
	Muted func() bool
	// Enroll persists a newly paired chat id (append to the allowlist +
	// save config). Called after the bridge validates a pairing code.
	Enroll func(chatID int64) error
	// WebViewerURL is the base URL of the optional tailnet markdown
	// viewer, or "" when disabled.
	WebViewerURL string
	// Now is the clock (injectable for tests). nil → time.Now.
	Now func() time.Time
	// Log is an optional structured logger sink.
	Log func(format string, args ...any)
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func (o Options) paneTail() int {
	if o.PaneTailLines <= 0 {
		return 24
	}
	return o.PaneTailLines
}

// Bridge is the long-lived Telegram subsystem: it long-polls for
// updates, authorizes them against the allowlist, and dispatches to the
// read/control/exec/agent surfaces, routing each action to the daemon
// that owns the target session.
type Bridge struct {
	bot      BotAPI
	router   *Router
	opts     Options
	pairing  *pairingStore
	alerts   *alertStore
	chats    *chatState
	username string
	offset   int64
}

// New builds a Bridge. The bot and router are required; opts carries the
// allowlist/enroll callbacks.
func New(bot BotAPI, router *Router, opts Options) *Bridge {
	return &Bridge{
		bot:     bot,
		router:  router,
		opts:    opts,
		pairing: newPairingStore(opts.now),
		alerts:  newAlertStore(),
		chats:   newChatState(),
	}
}

func (b *Bridge) logf(format string, args ...any) {
	if b.opts.Log != nil {
		b.opts.Log(format, args...)
	}
}

// Username returns the bot's @handle (available after Start validates
// the token). Used to format inline-query hints.
func (b *Bridge) Username() string { return b.username }

// Start validates the token, registers the command menu, then runs the
// long-poll loop until ctx is cancelled. Returns a non-nil error on a
// terminal condition (bad token, another daemon owns the token).
func (b *Bridge) Start(ctx context.Context) error {
	me, err := b.bot.GetMe(ctx)
	if err != nil {
		if IsUnauthorized(err) {
			return fmt.Errorf("telegram: bot token rejected (check @BotFather token): %w", err)
		}
		return fmt.Errorf("telegram: getMe: %w", err)
	}
	b.username = me.Username
	if err := b.installCommandMenu(ctx); err != nil {
		b.logf("telegram: setMyCommands: %v", err)
	}
	b.logf("telegram: bridge online as @%s", b.username)

	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		updates, err := b.poll(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if IsConflict(err) {
				// Exactly one daemon may long-poll a token. Stop hard so
				// status/doctor can report the conflict rather than spin.
				return fmt.Errorf("telegram: token already in use by another ccmuxd: %w", err)
			}
			b.logf("telegram: poll error: %v", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}
		backoff = time.Second
		for _, u := range updates {
			if u.UpdateID+1 > b.offset {
				b.offset = u.UpdateID + 1
			}
			b.handleUpdate(ctx, u)
		}
	}
}

func (b *Bridge) poll(ctx context.Context) ([]Update, error) {
	cctx, cancel := context.WithTimeout(ctx, pollTimeoutSecs*time.Second+pollHTTPSlack)
	defer cancel()
	return b.bot.GetUpdates(cctx, b.offset, pollTimeoutSecs)
}

// handleUpdate authorizes then dispatches one update. Pairing (/start
// <code>) is the only action allowed pre-authorization; everything else
// requires an allowlisted chat.
func (b *Bridge) handleUpdate(ctx context.Context, u Update) {
	if u.Message != nil && isStartCommand(u.Message.Text) {
		b.handleStart(ctx, u.Message)
		return
	}
	chatID, ok := u.ChatID()
	if !ok {
		return
	}
	if !b.allowed(chatID) {
		// Unknown chat: take no action and leak no data. We don't even
		// reply, to avoid being a chatty oracle for token-guessers.
		b.logf("telegram: ignoring update from non-allowlisted chat %d", chatID)
		return
	}
	switch {
	case u.Message != nil:
		b.handleMessage(ctx, u.Message)
	case u.CallbackQuery != nil:
		b.handleCallback(ctx, u.CallbackQuery)
	case u.InlineQuery != nil:
		b.handleInline(ctx, u.InlineQuery)
	}
}

func (b *Bridge) allowed(chatID int64) bool {
	return b.opts.Allowed != nil && b.opts.Allowed(chatID)
}

// reply sends a plain text message to a chat, best-effort.
func (b *Bridge) reply(ctx context.Context, chatID int64, text string) {
	b.send(ctx, SendMessageRequest{ChatID: chatID, Text: text})
}

func (b *Bridge) send(ctx context.Context, req SendMessageRequest) *Message {
	// Final safety net: a plain message must never exceed Telegram's
	// limit. HTML messages are pre-bounded by sendCodeOrDocument, so we
	// only clamp plain text (truncating HTML could orphan a tag/entity).
	if req.ParseMode == "" {
		req.Text = clampPlain(req.Text, telegramMaxMessageChars)
	}
	cctx, cancel := context.WithTimeout(ctx, actionDeadline)
	defer cancel()
	m, err := b.bot.SendMessage(cctx, req)
	if err == nil {
		return m
	}
	// Rich formatting degrades gracefully: if Telegram rejects our
	// entities, resend as clean plain text (tags stripped) so the
	// message still lands. Never let formatting eat content.
	if IsParseError(err) && req.ParseMode != "" {
		b.logf("telegram: parse error, retrying as plain text: %v", err)
		plain := req
		plain.ParseMode = ""
		plain.Text = clampPlain(stripHTML(req.Text), telegramMaxMessageChars)
		cctx2, cancel2 := context.WithTimeout(ctx, actionDeadline)
		defer cancel2()
		if m2, err2 := b.bot.SendMessage(cctx2, plain); err2 == nil {
			return m2
		} else {
			b.logf("telegram: plain-text retry also failed: %v", err2)
		}
		return nil
	}
	b.logf("telegram: sendMessage: %v", err)
	return nil
}

// --- pairing ---------------------------------------------------------

// NewPairingCode mints a single-use code that the next `/start <code>`
// enrolls. Called by the daemon's pair endpoint / CLI.
func (b *Bridge) NewPairingCode() string {
	return b.pairing.mint(pairingCodeTTL)
}

func isStartCommand(text string) bool {
	t := strings.TrimSpace(text)
	return t == "/start" || strings.HasPrefix(t, "/start ")
}

func (b *Bridge) handleStart(ctx context.Context, m *Message) {
	chatID := m.Chat.ID
	_, code := splitCommand(m.Text)
	code = strings.TrimSpace(code)

	if b.allowed(chatID) {
		// Already paired — treat /start as a greeting.
		b.reply(ctx, chatID, b.welcomeText())
		return
	}
	if code == "" {
		b.reply(ctx, chatID, "To control ccmux from here, run `ccmux telegram pair` on your machine and send me /start <code>.")
		return
	}
	if !b.pairing.consume(code) {
		b.reply(ctx, chatID, "That pairing code is invalid or expired. Run `ccmux telegram pair` for a fresh one.")
		return
	}
	if b.opts.Enroll != nil {
		if err := b.opts.Enroll(chatID); err != nil {
			b.logf("telegram: enroll chat %d: %v", chatID, err)
			b.reply(ctx, chatID, "Paired the code but couldn't save it. Check the daemon logs.")
			return
		}
	}
	b.logf("telegram: enrolled chat %d", chatID)
	b.reply(ctx, chatID, "✅ Paired. "+b.welcomeText())
}

func (b *Bridge) welcomeText() string {
	return "You can now drive your ccmux sessions from here.\n" +
		"Try /sessions, /preview, /agent, or /notes. /help lists everything."
}

// pairingStore holds single-use, expiring pairing codes.
type pairingStore struct {
	mu    sync.Mutex
	codes map[string]time.Time // code -> expiry
	now   func() time.Time
	seq   int64
}

func newPairingStore(now func() time.Time) *pairingStore {
	if now == nil {
		now = time.Now
	}
	return &pairingStore{codes: map[string]time.Time{}, now: now}
}

func (p *pairingStore) mint(ttl time.Duration) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gcLocked()
	p.seq++
	// 6-char code from the clock + sequence; not security-critical (the
	// allowlist is the real gate) but unguessable enough for a 10-min TTL.
	code := pairCode(p.now(), p.seq)
	p.codes[code] = p.now().Add(ttl)
	return code
}

func (p *pairingStore) consume(code string) bool {
	code = strings.TrimSpace(code)
	p.mu.Lock()
	defer p.mu.Unlock()
	exp, ok := p.codes[code]
	if !ok {
		return false
	}
	delete(p.codes, code) // single-use regardless of outcome
	return p.now().Before(exp)
}

func (p *pairingStore) gcLocked() {
	now := p.now()
	for c, exp := range p.codes {
		if !now.Before(exp) {
			delete(p.codes, c)
		}
	}
}

// pairCode derives a short alphanumeric code from a timestamp + sequence.
// Deterministic given inputs (so tests with an injected clock are stable).
func pairCode(t time.Time, seq int64) string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no ambiguous chars
	n := uint64(t.UnixNano()) ^ (uint64(seq) * 0x9E3779B97F4A7C15)
	var sb strings.Builder
	for i := 0; i < 6; i++ {
		sb.WriteByte(alphabet[n%uint64(len(alphabet))])
		n /= uint64(len(alphabet))
	}
	return sb.String()
}

// --- per-chat state --------------------------------------------------

// pendingArg records that a chat was asked to supply an argument for an
// agent command (e.g. the value for /model). The next plain message
// becomes the argument.
type pendingArg struct {
	Target  Target
	Command string
}

// noteRef identifies one markdown file by project + relative path.
// Referenced from inline buttons by index (callback_data is 64-byte
// capped, and a project+path pair can exceed that).
type noteRef struct {
	Project string
	Rel     string
}

// chatState tracks each chat's "current" session target so a bare
// prompt or a no-target command knows where to go, plus any pending
// argument capture and the last-listed notes (for index callbacks).
type chatState struct {
	mu      sync.Mutex
	current map[int64]Target
	pending map[int64]pendingArg
	notes   map[int64][]noteRef
}

func newChatState() *chatState {
	return &chatState{
		current: map[int64]Target{},
		pending: map[int64]pendingArg{},
		notes:   map[int64][]noteRef{},
	}
}

func (c *chatState) setNotes(chatID int64, refs []noteRef) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notes[chatID] = refs
}

func (c *chatState) note(chatID int64, idx int) (noteRef, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	list := c.notes[chatID]
	if idx < 0 || idx >= len(list) {
		return noteRef{}, false
	}
	return list[idx], true
}

func (c *chatState) setPending(chatID int64, p pendingArg) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending[chatID] = p
}

// takePending returns and clears any pending arg capture for a chat.
func (c *chatState) takePending(chatID int64) (pendingArg, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.pending[chatID]
	if ok {
		delete(c.pending, chatID)
	}
	return p, ok
}

func (c *chatState) setCurrent(chatID int64, t Target) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.current[chatID] = t
}

func (c *chatState) getCurrent(chatID int64) (Target, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.current[chatID]
	return t, ok
}

// --- callback-data codec ---------------------------------------------
//
// Telegram caps callback_data at 64 bytes. We pack "action|target|payload"
// and keep each field short (action codes are 3 chars; agent command
// names are short slash-commands). decodeCB tolerates missing fields.

func encodeCB(action, target, payload string) string {
	return action + "|" + target + "|" + payload
}

func decodeCB(data string) (action, target, payload string) {
	parts := strings.SplitN(data, "|", 3)
	switch len(parts) {
	case 3:
		return parts[0], parts[1], parts[2]
	case 2:
		return parts[0], parts[1], ""
	case 1:
		return parts[0], "", ""
	}
	return "", "", ""
}

// splitCommand splits "/cmd rest of line" into ("/cmd", "rest of line").
// The command token is lowercased; a trailing "@botname" is stripped so
// "/sessions@ccmuxbot" works in groups.
func splitCommand(text string) (cmd, rest string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", ""
	}
	sp := strings.IndexFunc(text, func(r rune) bool { return r == ' ' || r == '\n' || r == '\t' })
	if sp < 0 {
		cmd, rest = text, ""
	} else {
		cmd, rest = text[:sp], strings.TrimSpace(text[sp+1:])
	}
	if at := strings.IndexByte(cmd, '@'); at >= 0 {
		cmd = cmd[:at]
	}
	return strings.ToLower(cmd), rest
}
