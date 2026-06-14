package telegram

import (
	"context"
	"strconv"
	"strings"
)

const maxInlineResults = 50

// botCommands is the "/" command menu, reflecting the active tiers — the
// exec command is only advertised when allow_exec is on.
func (b *Bridge) botCommands() []BotCommand {
	cmds := []BotCommand{
		{Command: "sessions", Description: "List sessions across the tailnet"},
		{Command: "preview", Description: "Peek at a session's pane"},
		{Command: "agent", Description: "Drive the agent CLI's commands"},
		{Command: "say", Description: "Send a prompt to the agent"},
		{Command: "new", Description: "Start a session in a project"},
		{Command: "kill", Description: "End a session"},
		{Command: "send", Description: "Send raw input to a session"},
		{Command: "projects", Description: "List projects"},
		{Command: "usage", Description: "Token / cost summary"},
		{Command: "notes", Description: "Browse project notes"},
		{Command: "menu", Description: "Quick action menu"},
		{Command: "help", Description: "Show help"},
	}
	if b.opts.AllowExec {
		cmds = append(cmds, BotCommand{Command: "run", Description: "Run a raw command (exec tier)"})
	}
	return cmds
}

func (b *Bridge) installCommandMenu(ctx context.Context) error {
	cctx, cancel := context.WithTimeout(ctx, actionDeadline)
	defer cancel()
	return b.bot.SetMyCommands(cctx, b.botCommands())
}

// handleInline answers an inline query ("@bot <partial>") with matching
// commands. The agent's own slash-commands for the chat's current
// session come first (the primary "available CLI commands" surface),
// then ccmux's bot commands. Each result's description is its preview;
// choosing one inserts the command, which the bot then routes.
func (b *Bridge) handleInline(ctx context.Context, iq *InlineQuery) {
	q := strings.TrimSpace(iq.Query)
	var results []InlineQueryResultArticle
	add := func(title, desc, msg string) {
		if len(results) >= maxInlineResults {
			return
		}
		results = append(results, NewInlineArticle(strconv.Itoa(len(results)), title, desc, msg))
	}

	// Agent commands for the chat's current session.
	if t, ok := b.chats.getCurrent(iq.From.ID); ok {
		if cli, ok := b.router.ClientFor(t); ok {
			cctx, cancel := context.WithTimeout(ctx, actionDeadline)
			cat, err := cli.AgentCommands(cctx, t.Session)
			cancel()
			if err == nil {
				for _, c := range cat.Commands {
					if matchesQuery(c.Name, q) {
						add(c.Name+" → "+t.String(), c.Description, c.Name)
					}
				}
			}
		}
	}

	// ccmux bot commands.
	for _, bc := range b.botCommands() {
		slash := "/" + bc.Command
		if matchesQuery(slash, q) {
			add(slash, bc.Description, slash)
		}
	}

	cctx, cancel := context.WithTimeout(ctx, actionDeadline)
	defer cancel()
	if err := b.bot.AnswerInlineQuery(cctx, iq.ID, results, 0); err != nil {
		b.logf("telegram: answerInlineQuery: %v", err)
	}
}

// matchesQuery reports whether a command name matches a (possibly empty)
// query, ignoring a leading slash on either side. Empty query matches
// everything (the picker shows the full catalog).
func matchesQuery(name, query string) bool {
	name = strings.ToLower(strings.TrimPrefix(name, "/"))
	query = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(query), "/"))
	if query == "" {
		return true
	}
	return strings.Contains(name, query)
}
