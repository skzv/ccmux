package telegram

import (
	"context"
	"strings"
	"sync"
)

// alert is one outstanding needs-input notification. The same alert is
// delivered to every allowlisted chat, so it tracks the per-chat message
// ids in order to edit them all on resolution.
type alert struct {
	target   Target
	changeID string
	messages []alertMsg
	resolved bool
}

type alertMsg struct {
	chatID    int64
	messageID int64
}

// alertStore tracks outstanding alerts. Invariant: at most one
// unresolved alert per target, which is what makes dedup ("one block →
// one alert") and quick-reply attribution well-defined.
type alertStore struct {
	mu       sync.Mutex
	byChange map[string]*alert
	byTarget map[string]*alert // target string -> current unresolved alert
	byMsg    map[int64]*alert  // message id -> alert (quick-reply attribution)
}

func newAlertStore() *alertStore {
	return &alertStore{
		byChange: map[string]*alert{},
		byTarget: map[string]*alert{},
		byMsg:    map[int64]*alert{},
	}
}

// begin reserves an alert for a target if none is outstanding. Returns
// the alert and true when the caller should emit; false when an
// unresolved alert already exists (dedup).
func (s *alertStore) begin(target Target, changeID string) (*alert, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.byTarget[target.String()]; ok && !existing.resolved {
		return existing, false
	}
	a := &alert{target: target, changeID: changeID}
	s.byChange[changeID] = a
	s.byTarget[target.String()] = a
	return a, true
}

func (s *alertStore) addMessage(a *alert, chatID, messageID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a.messages = append(a.messages, alertMsg{chatID: chatID, messageID: messageID})
	s.byMsg[messageID] = a
}

func (s *alertStore) lookupChange(changeID string) (*alert, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.byChange[changeID]
	return a, ok
}

func (s *alertStore) lookupMessage(messageID int64) (*alert, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.byMsg[messageID]
	return a, ok
}

// outstanding returns every unresolved alert.
func (s *alertStore) outstanding() []*alert {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*alert
	for _, a := range s.byTarget {
		if !a.resolved {
			out = append(out, a)
		}
	}
	return out
}

// resolve marks an alert resolved and returns it. Idempotent.
func (s *alertStore) resolve(a *alert) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a.resolved = true
	if cur, ok := s.byTarget[a.target.String()]; ok && cur == a {
		delete(s.byTarget, a.target.String())
	}
}

// resolveTarget resolves any outstanding alert for a target (e.g. the
// session was seen/attached elsewhere) and returns it.
func (s *alertStore) resolveTarget(target Target) (*alert, bool) {
	s.mu.Lock()
	a, ok := s.byTarget[target.String()]
	s.mu.Unlock()
	if !ok || a.resolved {
		return nil, false
	}
	s.resolve(a)
	return a, true
}

// Notify is the poll-loop sink: it alerts every allowlisted chat that a
// session needs input, with Approve/Deny/Preview controls. Deduped per
// target (one outstanding alert per session block) and suppressed when
// alerts are muted.
func (b *Bridge) Notify(ctx context.Context, host, session, paneTail, changeID string) {
	if b.muted() {
		return
	}
	target := Target{Host: host, Session: session}
	recips := b.recipients()
	if len(recips) == 0 {
		return
	}
	a, emit := b.alerts.begin(target, changeID)
	if !emit {
		return // an unresolved alert for this session is already out
	}
	// Lead with the session + buttons; the pane tail is an expandable
	// blockquote (collapsed on the phone) so it's there if you want it
	// but never buries the Approve/Deny.
	text, mode := quoteMessage("🔔 "+htmlBold(target.String())+" needs input", paneTail)
	kb := approvalKeyboard(target, changeID)
	for _, chatID := range recips {
		m := b.send(ctx, SendMessageRequest{ChatID: chatID, Text: text, ParseMode: mode, ReplyMarkup: kb})
		if m != nil {
			b.alerts.addMessage(a, chatID, m.MessageID)
		}
	}
}

// MarkSeen resolves any outstanding alert for a session because it was
// seen/attached elsewhere, editing its messages to say so. Called by the
// daemon when a session is no longer waiting unattended.
func (b *Bridge) MarkSeen(ctx context.Context, host, session string) {
	target := Target{Host: host, Session: session}
	a, ok := b.alerts.resolveTarget(target)
	if !ok {
		return
	}
	b.editAlerts(ctx, a, "✓ "+target.String()+" — handled elsewhere")
}

func (b *Bridge) muted() bool {
	return b.opts.Muted != nil && b.opts.Muted()
}

func (b *Bridge) recipients() []int64 {
	if b.opts.Recipients == nil {
		return nil
	}
	return b.opts.Recipients()
}

func approvalKeyboard(target Target, changeID string) *InlineKeyboardMarkup {
	ts := target.String()
	return &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{
		{
			{Text: "✅ Approve", CallbackData: encodeCB("apr", ts, changeID)},
			{Text: "🚫 Deny", CallbackData: encodeCB("dny", ts, changeID)},
		},
		{
			{Text: "👁 Preview", CallbackData: encodeCB("prv", ts, "")},
		},
	}}
}

// handleApprovalCallback resolves an Approve/Deny button tap.
func (b *Bridge) handleApprovalCallback(ctx context.Context, cb *CallbackQuery, action, target, changeID string) {
	a, ok := b.alerts.lookupChange(changeID)
	if !ok || a.resolved {
		b.answerCB(ctx, cb.ID, "Already handled")
		return
	}
	approve := action == "apr"
	if err := b.applyDecision(ctx, ParseTarget(target), approve); err != nil {
		b.answerCB(ctx, cb.ID, "Failed: "+err.Error())
		return
	}
	b.alerts.resolve(a)
	if approve {
		b.answerCB(ctx, cb.ID, "Approved")
		b.editAlerts(ctx, a, "✅ "+a.target.String()+" — approved")
	} else {
		b.answerCB(ctx, cb.ID, "Denied")
		b.editAlerts(ctx, a, "🚫 "+a.target.String()+" — denied")
	}
}

// tryQuickReply handles a watch-friendly text reply (y/n/approve/deny).
// Returns true if the message was consumed as a quick-reply.
func (b *Bridge) tryQuickReply(ctx context.Context, m *Message) bool {
	v := strings.ToLower(strings.TrimSpace(m.Text))
	approve := isApprove(v)
	deny := isDeny(v)
	if !approve && !deny {
		return false
	}

	var a *alert
	if m.ReplyTo != nil {
		if found, ok := b.alerts.lookupMessage(m.ReplyTo.MessageID); ok {
			a = found
		}
	}
	if a == nil {
		outs := b.alerts.outstanding()
		switch len(outs) {
		case 0:
			// Nothing to approve — let the caller treat it as a prompt.
			return false
		case 1:
			a = outs[0]
		default:
			b.reply(ctx, m.Chat.ID, "Which session? Reply to its alert, or tap the button.")
			return true
		}
	}
	if a.resolved {
		b.reply(ctx, m.Chat.ID, "That one's already handled.")
		return true
	}
	if err := b.applyDecision(ctx, a.target, approve); err != nil {
		b.reply(ctx, m.Chat.ID, "Couldn't act on "+a.target.String()+": "+err.Error())
		return true
	}
	b.alerts.resolve(a)
	if approve {
		b.editAlerts(ctx, a, "✅ "+a.target.String()+" — approved")
		b.reply(ctx, m.Chat.ID, "Approved "+a.target.String())
	} else {
		b.editAlerts(ctx, a, "🚫 "+a.target.String()+" — denied")
		b.reply(ctx, m.Chat.ID, "Denied "+a.target.String())
	}
	return true
}

// applyDecision delivers the accept/decline keystroke to a session. The
// generic mapping (Enter accepts the highlighted default, Escape
// cancels) matches Claude's permission prompt; per-agent overrides can
// come later.
func (b *Bridge) applyDecision(ctx context.Context, t Target, approve bool) error {
	cli, ok := b.router.ClientFor(t)
	if !ok {
		return errUnknownHost(t.Host)
	}
	keys := "Enter"
	if !approve {
		keys = "Escape"
	}
	cctx, cancel := context.WithTimeout(ctx, actionDeadline)
	defer cancel()
	return cli.SendKeys(cctx, t.Session, keys)
}

// editAlerts rewrites every chat's copy of an alert to its outcome and
// drops the buttons (so they can't be tapped into a stale action).
func (b *Bridge) editAlerts(ctx context.Context, a *alert, text string) {
	for _, m := range a.messages {
		cctx, cancel := context.WithTimeout(ctx, actionDeadline)
		err := b.bot.EditMessageText(cctx, EditMessageTextRequest{
			ChatID:    m.chatID,
			MessageID: m.messageID,
			Text:      text,
		})
		cancel()
		if err != nil {
			b.logf("telegram: editMessageText: %v", err)
		}
	}
}

func isApprove(v string) bool {
	switch v {
	case "y", "yes", "approve", "ok", "ack", "👍":
		return true
	}
	return false
}

func isDeny(v string) bool {
	switch v {
	case "n", "no", "deny", "cancel", "stop", "👎":
		return true
	}
	return false
}
