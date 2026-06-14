package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/skzv/ccmux/internal/daemon"
)

// handleMessage dispatches an authorized text message: a pending
// argument capture, a quick-reply to an alert, a slash-command, or a
// free-form prompt to the current session.
func (b *Bridge) handleMessage(ctx context.Context, m *Message) {
	text := strings.TrimSpace(m.Text)
	if text == "" {
		return
	}
	chatID := m.Chat.ID

	// A pending agent-command argument takes precedence: the user was
	// asked to supply a value for a command like /model.
	if pend, ok := b.chats.takePending(chatID); ok {
		if text == "/cancel" {
			b.reply(ctx, chatID, "Cancelled.")
			return
		}
		b.sendAgentCommand(ctx, chatID, pend.Target, pend.Command+" "+text)
		return
	}

	if !strings.HasPrefix(text, "/") {
		if b.tryQuickReply(ctx, m) {
			return
		}
		b.handlePrompt(ctx, chatID, Target{}, text)
		return
	}

	cmd, rest := splitCommand(text)
	switch cmd {
	case "/start":
		b.handleStart(ctx, m)
	case "/help":
		b.cmdHelp(ctx, chatID)
	case "/sessions":
		b.cmdSessions(ctx, chatID)
	case "/projects":
		b.cmdProjects(ctx, chatID)
	case "/usage":
		b.cmdUsage(ctx, chatID)
	case "/preview":
		b.cmdPreview(ctx, chatID, rest)
	case "/notes":
		b.cmdNotes(ctx, chatID, rest)
	case "/new":
		b.cmdNew(ctx, chatID, rest)
	case "/kill":
		b.cmdKill(ctx, chatID, rest)
	case "/send":
		b.cmdSend(ctx, chatID, rest)
	case "/say", "/prompt":
		b.cmdSay(ctx, chatID, rest)
	case "/agent":
		b.cmdAgent(ctx, chatID, rest)
	case "/run":
		b.cmdRun(ctx, chatID, rest)
	default:
		// Not a bot command. If the chat has a current session, treat it
		// as an agent slash-command (this is what makes inline-query
		// autocomplete of agent commands land — picking "/compact"
		// inserts "/compact", which routes to the current agent).
		if cur, ok := b.chats.getCurrent(chatID); ok {
			b.sendAgentCommand(ctx, chatID, cur, text)
			return
		}
		b.reply(ctx, chatID, "Unknown command "+cmd+". Open a session with /preview or /agent first, or see /help.")
	}
}

// --- read tier -------------------------------------------------------

func (b *Bridge) cmdSessions(ctx context.Context, chatID int64) {
	ss := b.router.AllSessions(ctx)
	if len(ss) == 0 {
		b.reply(ctx, chatID, "No sessions running.")
		return
	}
	text, mode := formatSessions(ss)
	b.send(ctx, SendMessageRequest{ChatID: chatID, Text: text, ParseMode: mode})
}

func (b *Bridge) cmdProjects(ctx context.Context, chatID int64) {
	cli, ok := b.router.Client(LocalHost)
	if !ok {
		b.reply(ctx, chatID, "No local daemon.")
		return
	}
	cctx, cancel := context.WithTimeout(ctx, actionDeadline)
	defer cancel()
	projs, err := cli.Projects(cctx)
	if err != nil {
		b.reply(ctx, chatID, "Couldn't list projects: "+err.Error())
		return
	}
	if len(projs) == 0 {
		b.reply(ctx, chatID, "No projects found.")
		return
	}
	var sb strings.Builder
	sb.WriteString("Projects:\n")
	for _, p := range projs {
		sb.WriteString("• " + p.Name)
		if p.Agent != "" {
			sb.WriteString(" (" + p.Agent + ")")
		}
		sb.WriteString("\n")
	}
	b.reply(ctx, chatID, strings.TrimRight(sb.String(), "\n"))
}

func (b *Bridge) cmdUsage(ctx context.Context, chatID int64) {
	cli, ok := b.router.Client(LocalHost)
	if !ok {
		b.reply(ctx, chatID, "No local daemon.")
		return
	}
	cctx, cancel := context.WithTimeout(ctx, actionDeadline)
	defer cancel()
	u, err := cli.Usage(cctx)
	if err != nil {
		b.reply(ctx, chatID, "Couldn't read usage: "+err.Error())
		return
	}
	text, mode := formatUsage(u)
	b.send(ctx, SendMessageRequest{ChatID: chatID, Text: text, ParseMode: mode})
}

func (b *Bridge) cmdPreview(ctx context.Context, chatID int64, rest string) {
	if rest == "" {
		// No target: offer a session picker (inline-keyboard arg nav).
		b.offerSessionPicker(ctx, chatID, "prv", "Preview which session?")
		return
	}
	targetStr, more := firstField(rest)
	t := ParseTarget(targetStr)
	lines := b.opts.paneTail()
	if more != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(more)); err == nil && n > 0 && n <= 200 {
			lines = n
		}
	}
	b.previewTo(ctx, chatID, t, lines)
}

func (b *Bridge) previewTo(ctx context.Context, chatID int64, t Target, lines int) {
	cli, ok := b.router.ClientFor(t)
	if !ok {
		b.reply(ctx, chatID, "Unknown host "+t.Host+".")
		return
	}
	cctx, cancel := context.WithTimeout(ctx, actionDeadline)
	defer cancel()
	pv, err := cli.Preview(cctx, t.Session, lines)
	if err != nil {
		b.reply(ctx, chatID, "Couldn't preview "+t.String()+": "+err.Error())
		return
	}
	b.chats.setCurrent(chatID, t)
	b.sendCodeOrDocument(ctx, chatID, "Preview "+t.String(), tailString(pv.Content, lines), "preview.txt")
}

// sendCodeOrDocument sends body as an expandable-blockquote message
// (collapsed by default, tap to expand), or — when it's too long for one
// message — as a .txt document. header is plain text; it's bolded for the
// inline path and used as-is for the document caption. This is the
// output-limit safety the spec requires: never send an over-limit
// request, attach instead.
func (b *Bridge) sendCodeOrDocument(ctx context.Context, chatID int64, header, body, filename string) {
	if len(body) > maxInlineCodeChars {
		cctx, cancel := context.WithTimeout(ctx, actionDeadline)
		defer cancel()
		if _, err := b.bot.SendDocument(cctx, chatID, filename, []byte(header+"\n\n"+body), header); err != nil {
			b.logf("telegram: sendDocument: %v", err)
		}
		return
	}
	text, mode := quoteMessage(htmlBold(header), body)
	b.send(ctx, SendMessageRequest{ChatID: chatID, Text: text, ParseMode: mode})
}

// --- control tier ----------------------------------------------------

func (b *Bridge) cmdNew(ctx context.Context, chatID int64, rest string) {
	project, agentID := firstField(rest)
	if project == "" {
		b.reply(ctx, chatID, "Usage: /new <project> [agent]")
		return
	}
	cli, ok := b.router.Client(LocalHost)
	if !ok {
		b.reply(ctx, chatID, "No local daemon.")
		return
	}
	cctx, cancel := context.WithTimeout(ctx, spawnDeadline) // spawn can be slow
	defer cancel()
	st, err := cli.NewSession(cctx, daemon.NewSessionRequest{Project: project, Agent: strings.TrimSpace(agentID)})
	if err != nil {
		b.reply(ctx, chatID, "Couldn't start session: "+err.Error())
		return
	}
	b.chats.setCurrent(chatID, Target{Host: LocalHost, Session: st.Name})
	b.reply(ctx, chatID, "Started "+st.Name+" in "+project+".")
}

func (b *Bridge) cmdKill(ctx context.Context, chatID int64, rest string) {
	targetStr, conf := firstField(rest)
	if targetStr == "" {
		b.offerSessionPicker(ctx, chatID, "kilq", "Kill which session?")
		return
	}
	t := ParseTarget(targetStr)
	if strings.TrimSpace(conf) == "confirm" {
		b.doKill(ctx, chatID, t)
		return
	}
	// Destructive — require an explicit confirm step.
	kb := &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{{
		{Text: "⚠️ Confirm kill " + t.String(), CallbackData: encodeCB("kill", t.String(), "confirm")},
	}}}
	b.send(ctx, SendMessageRequest{
		ChatID:      chatID,
		Text:        "Kill " + t.String() + "? This ends the tmux session.",
		ReplyMarkup: kb,
	})
}

func (b *Bridge) doKill(ctx context.Context, chatID int64, t Target) {
	cli, ok := b.router.ClientFor(t)
	if !ok {
		b.reply(ctx, chatID, "Unknown host "+t.Host+".")
		return
	}
	cctx, cancel := context.WithTimeout(ctx, actionDeadline)
	defer cancel()
	if err := cli.Kill(cctx, t.Session); err != nil {
		b.reply(ctx, chatID, "Couldn't kill "+t.String()+": "+err.Error())
		return
	}
	b.reply(ctx, chatID, "Killed "+t.String()+".")
}

// cmdSend is the raw control-tier text delivery: /send <target> <text>.
func (b *Bridge) cmdSend(ctx context.Context, chatID int64, rest string) {
	targetStr, text := firstField(rest)
	if targetStr == "" || text == "" {
		b.reply(ctx, chatID, "Usage: /send <host:session> <text>")
		return
	}
	b.handlePrompt(ctx, chatID, ParseTarget(targetStr), text)
}

// cmdSay is the friendly prompt: /say <target> <text> (alias /prompt).
func (b *Bridge) cmdSay(ctx context.Context, chatID int64, rest string) {
	targetStr, text := firstField(rest)
	if targetStr == "" || text == "" {
		b.reply(ctx, chatID, "Usage: /say <host:session> <text>")
		return
	}
	b.handlePrompt(ctx, chatID, ParseTarget(targetStr), text)
}

// handlePrompt delivers free-form text to a session's agent (text then
// Enter). An empty target falls back to the chat's current session.
func (b *Bridge) handlePrompt(ctx context.Context, chatID int64, t Target, text string) {
	if t.Session == "" {
		cur, ok := b.chats.getCurrent(chatID)
		if !ok {
			b.reply(ctx, chatID, "Which session? Use /say <host:session> <text>, or /preview a session first.")
			return
		}
		t = cur
	}
	cli, ok := b.router.ClientFor(t)
	if !ok {
		b.reply(ctx, chatID, "Unknown host "+t.Host+".")
		return
	}
	if err := b.sendKeysAndEnter(ctx, cli, t.Session, text); err != nil {
		b.reply(ctx, chatID, "Couldn't send to "+t.String()+": "+err.Error())
		return
	}
	b.chats.setCurrent(chatID, t)
	b.reply(ctx, chatID, "→ "+t.String())
}

// --- exec tier (opt-in) ---------------------------------------------

func (b *Bridge) cmdRun(ctx context.Context, chatID int64, rest string) {
	if !b.opts.AllowExec {
		b.reply(ctx, chatID, "The exec tier is disabled. Set [telegram].allow_exec = true to enable /run, or use /send for normal input.")
		return
	}
	targetStr, raw := firstField(rest)
	if targetStr == "" || raw == "" {
		b.reply(ctx, chatID, "Usage: /run <host:session> <raw keys or command>")
		return
	}
	t := ParseTarget(targetStr)
	cli, ok := b.router.ClientFor(t)
	if !ok {
		b.reply(ctx, chatID, "Unknown host "+t.Host+".")
		return
	}
	if err := b.sendKeysAndEnter(ctx, cli, t.Session, raw); err != nil {
		b.reply(ctx, chatID, "Couldn't run on "+t.String()+": "+err.Error())
		return
	}
	b.reply(ctx, chatID, "ran on "+t.String())
}

// --- agent CLI control ----------------------------------------------

func (b *Bridge) cmdAgent(ctx context.Context, chatID int64, rest string) {
	targetStr, _ := firstField(rest)
	var t Target
	if targetStr != "" {
		t = ParseTarget(targetStr)
	} else if cur, ok := b.chats.getCurrent(chatID); ok {
		t = cur
	} else {
		b.offerSessionPicker(ctx, chatID, "agnt", "Drive which session's agent?")
		return
	}
	b.showAgentCatalog(ctx, chatID, t)
}

func (b *Bridge) showAgentCatalog(ctx context.Context, chatID int64, t Target) {
	cli, ok := b.router.ClientFor(t)
	if !ok {
		b.reply(ctx, chatID, "Unknown host "+t.Host+".")
		return
	}
	cctx, cancel := context.WithTimeout(ctx, actionDeadline)
	defer cancel()
	cat, err := cli.AgentCommands(cctx, t.Session)
	if err != nil {
		b.reply(ctx, chatID, "Couldn't load commands for "+t.String()+": "+err.Error())
		return
	}
	b.chats.setCurrent(chatID, t)
	if len(cat.Commands) == 0 {
		b.reply(ctx, chatID, t.String()+" runs "+orDefault(cat.Agent, "an agent")+" — prompt-only. Just send text to talk to it.")
		return
	}
	var body strings.Builder
	fmt.Fprintf(&body, "%s — %s commands:\n", htmlBold(t.String()), escapeHTMLMin(orDefault(cat.Agent, "agent")))
	var rows [][]InlineKeyboardButton
	for _, c := range cat.Commands {
		body.WriteString(htmlCode(c.Name))
		if c.Description != "" {
			body.WriteString(" — " + escapeHTMLMin(c.Description))
		}
		body.WriteString("\n")
		action := "acmd"
		if c.TakesArg {
			action = "acmda"
		}
		rows = append(rows, []InlineKeyboardButton{{
			Text:         c.Name,
			CallbackData: encodeCB(action, t.String(), c.Name),
		}})
	}
	b.send(ctx, SendMessageRequest{
		ChatID:      chatID,
		Text:        strings.TrimRight(body.String(), "\n"),
		ParseMode:   "HTML",
		ReplyMarkup: &InlineKeyboardMarkup{InlineKeyboard: rows},
	})
}

// sendAgentCommand delivers a (possibly argument-completed) agent
// command to a session and confirms.
func (b *Bridge) sendAgentCommand(ctx context.Context, chatID int64, t Target, command string) {
	cli, ok := b.router.ClientFor(t)
	if !ok {
		b.reply(ctx, chatID, "Unknown host "+t.Host+".")
		return
	}
	if err := b.sendKeysAndEnter(ctx, cli, t.Session, command); err != nil {
		b.reply(ctx, chatID, "Couldn't send "+command+" to "+t.String()+": "+err.Error())
		return
	}
	b.chats.setCurrent(chatID, t)
	b.reply(ctx, chatID, "Sent "+command+" to "+t.String()+".")
}

// --- callbacks (button taps) ----------------------------------------

func (b *Bridge) handleCallback(ctx context.Context, cb *CallbackQuery) {
	action, target, payload := decodeCB(cb.Data)
	chatID := cb.From.ID
	if cb.Message != nil {
		chatID = cb.Message.Chat.ID
	}
	switch action {
	case "apr", "dny":
		b.handleApprovalCallback(ctx, cb, action, target, payload)
		return
	case "prv":
		b.answerCB(ctx, cb.ID, "")
		b.previewTo(ctx, chatID, ParseTarget(target), b.opts.paneTail())
	case "kilq":
		// session picked from a kill picker → ask to confirm
		b.answerCB(ctx, cb.ID, "")
		b.cmdKill(ctx, chatID, target)
	case "kill":
		b.answerCB(ctx, cb.ID, "Killing…")
		b.doKill(ctx, chatID, ParseTarget(target))
	case "agnt":
		b.answerCB(ctx, cb.ID, "")
		b.showAgentCatalog(ctx, chatID, ParseTarget(target))
	case "acmd":
		b.answerCB(ctx, cb.ID, "Sending "+payload)
		b.sendAgentCommand(ctx, chatID, ParseTarget(target), payload)
	case "acmda":
		b.answerCB(ctx, cb.ID, "")
		b.chats.setPending(chatID, pendingArg{Target: ParseTarget(target), Command: payload})
		b.reply(ctx, chatID, "Send the value for "+payload+" (or /cancel).")
	case "note":
		b.answerCB(ctx, cb.ID, "")
		b.sendNoteDocByIndex(ctx, chatID, target)
	default:
		b.answerCB(ctx, cb.ID, "")
	}
}

func (b *Bridge) answerCB(ctx context.Context, id, toast string) {
	cctx, cancel := context.WithTimeout(ctx, actionDeadline)
	defer cancel()
	if err := b.bot.AnswerCallbackQuery(cctx, id, toast, false); err != nil {
		b.logf("telegram: answerCallbackQuery: %v", err)
	}
}

// --- helpers ---------------------------------------------------------

func (b *Bridge) sendKeysAndEnter(ctx context.Context, cli DaemonClient, session, text string) error {
	cctx, cancel := context.WithTimeout(ctx, actionDeadline)
	defer cancel()
	if err := cli.SendKeys(cctx, session, text); err != nil {
		return err
	}
	return cli.SendKeys(cctx, session, "Enter")
}

// offerSessionPicker presents an inline keyboard of every session, each
// button carrying the given callback action. Used by argument-free
// /preview, /kill, /agent.
func (b *Bridge) offerSessionPicker(ctx context.Context, chatID int64, action, prompt string) {
	ss := b.router.AllSessions(ctx)
	if len(ss) == 0 {
		b.reply(ctx, chatID, "No sessions running.")
		return
	}
	var rows [][]InlineKeyboardButton
	for _, s := range ss {
		t := Target{Host: s.Host, Session: s.Name}
		rows = append(rows, []InlineKeyboardButton{{
			Text:         stateGlyph(s.State) + " " + t.String(),
			CallbackData: encodeCB(action, t.String(), ""),
		}})
	}
	b.send(ctx, SendMessageRequest{
		ChatID:      chatID,
		Text:        prompt,
		ReplyMarkup: &InlineKeyboardMarkup{InlineKeyboard: rows},
	})
}

func (b *Bridge) cmdHelp(ctx context.Context, chatID int64) {
	var sb strings.Builder
	sb.WriteString(htmlBold("ccmux bot") + " — what I can do:\n")
	sb.WriteString(htmlCode("/sessions") + " — list sessions (this host + tailnet peers)\n")
	sb.WriteString(htmlCode("/preview <host:session> [lines]") + " — peek at a pane\n")
	sb.WriteString(htmlCode("/agent [host:session]") + " — drive the agent CLI (its slash-commands)\n")
	sb.WriteString(htmlCode("/say <host:session> <text>") + " — send a prompt to the agent\n")
	sb.WriteString(htmlCode("/new <project> [agent]") + " — start a session\n")
	sb.WriteString(htmlCode("/kill <host:session>") + " — end a session (asks to confirm)\n")
	sb.WriteString(htmlCode("/send <host:session> <text>") + " — raw input\n")
	sb.WriteString(htmlCode("/projects") + ", " + htmlCode("/usage") + ", " + htmlCode("/notes <project> [query]") + "\n")
	if b.opts.AllowExec {
		sb.WriteString(htmlCode("/run <host:session> <raw>") + " — exec tier (enabled)\n")
	}
	sb.WriteString("\nWhen an agent needs you, I'll message you with " + htmlBold("Approve") + " / " + htmlBold("Deny") + " — or just reply y / n.")
	b.send(ctx, SendMessageRequest{ChatID: chatID, Text: sb.String(), ParseMode: "HTML"})
}

// firstField splits "first rest of the line" into ("first", "rest").
func firstField(s string) (first, rest string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	i := strings.IndexFunc(s, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' })
	if i < 0 {
		return s, ""
	}
	return s[:i], strings.TrimSpace(s[i+1:])
}

func orDefault(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}
