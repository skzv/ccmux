package telegram

import (
	"context"
	"path"
	"strconv"
	"strings"
)

const maxNoteButtons = 30

// cmdNotes lists a project's markdown vault (or searches it) as a set of
// tappable buttons; choosing one sends the file as a document, which the
// Telegram in-app browser renders as formatted markdown.
func (b *Bridge) cmdNotes(ctx context.Context, chatID int64, rest string) {
	project, query := firstField(rest)
	if project == "" {
		b.reply(ctx, chatID, "Usage: /notes <project> [search query]")
		return
	}
	cli, ok := b.router.Client(LocalHost)
	if !ok {
		b.reply(ctx, chatID, "No local daemon.")
		return
	}
	cctx, cancel := context.WithTimeout(ctx, actionDeadline)
	defer cancel()

	var refs []noteRef
	var rows [][]InlineKeyboardButton
	if query != "" {
		hits, err := cli.SearchNotes(cctx, project, query)
		if err != nil {
			b.reply(ctx, chatID, "Search failed: "+err.Error())
			return
		}
		if len(hits) == 0 {
			b.reply(ctx, chatID, "No matches for “"+query+"” in "+project+".")
			return
		}
		for _, h := range hits {
			if len(refs) >= maxNoteButtons {
				break
			}
			idx := len(refs)
			refs = append(refs, noteRef{Project: project, Rel: h.Rel})
			rows = append(rows, []InlineKeyboardButton{{
				Text:         h.Rel,
				CallbackData: encodeCB("note", strconv.Itoa(idx), ""),
			}})
		}
	} else {
		entries, err := cli.Notes(cctx, project)
		if err != nil {
			b.reply(ctx, chatID, "Couldn't list notes: "+err.Error())
			return
		}
		if len(entries) == 0 {
			b.reply(ctx, chatID, "No notes in "+project+".")
			return
		}
		for _, e := range entries {
			if len(refs) >= maxNoteButtons {
				break
			}
			idx := len(refs)
			refs = append(refs, noteRef{Project: project, Rel: e.Rel})
			label := e.Display
			if label == "" {
				label = e.Rel
			}
			rows = append(rows, []InlineKeyboardButton{{
				Text:         label,
				CallbackData: encodeCB("note", strconv.Itoa(idx), ""),
			}})
		}
	}
	b.chats.setNotes(chatID, refs)

	header := "Notes in " + project
	if query != "" {
		header = "Matches for “" + query + "” in " + project
	}
	if len(refs) == maxNoteButtons {
		header += " (first " + strconv.Itoa(maxNoteButtons) + ")"
	}
	if url, ok := b.webViewerURL(project); ok {
		rows = append(rows, []InlineKeyboardButton{{Text: "🌐 Open vault in browser", URL: url}})
	}
	b.send(ctx, SendMessageRequest{
		ChatID:      chatID,
		Text:        header,
		ReplyMarkup: &InlineKeyboardMarkup{InlineKeyboard: rows},
	})
}

// sendNoteDoc looks up a previously-listed note by index and sends it as
// a rendered .md document.
func (b *Bridge) sendNoteDocByIndex(ctx context.Context, chatID int64, idxStr string) {
	idx, err := strconv.Atoi(idxStr)
	if err != nil {
		return
	}
	ref, ok := b.chats.note(chatID, idx)
	if !ok {
		b.reply(ctx, chatID, "That note list expired — run /notes again.")
		return
	}
	if !safeNotePath(ref.Rel) {
		b.reply(ctx, chatID, "Refusing that path.")
		return
	}
	cli, ok := b.router.Client(LocalHost)
	if !ok {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, actionDeadline)
	defer cancel()
	nc, err := cli.NoteContent(cctx, ref.Project, ref.Rel)
	if err != nil {
		b.reply(ctx, chatID, "Couldn't read "+ref.Rel+": "+err.Error())
		return
	}
	cctx2, cancel2 := context.WithTimeout(ctx, actionDeadline)
	defer cancel2()
	if _, err := b.bot.SendDocument(cctx2, chatID, path.Base(ref.Rel), []byte(nc.Content), ref.Rel); err != nil {
		b.logf("telegram: sendDocument: %v", err)
		b.reply(ctx, chatID, "Couldn't send the file.")
	}
}

// webViewerURL builds the optional tailnet viewer URL for a project, or
// reports false when the viewer is disabled.
func (b *Bridge) webViewerURL(project string) (string, bool) {
	base := strings.TrimRight(b.opts.WebViewerURL, "/")
	if base == "" {
		return "", false
	}
	return base + "/notes/" + project, true
}

// safeNotePath rejects traversal and absolute paths, and non-.md files —
// belt-and-suspenders over the daemon's own validation.
func safeNotePath(rel string) bool {
	if rel == "" || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) {
		return false
	}
	cleaned := path.Clean(strings.ReplaceAll(rel, `\`, "/"))
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return false
	}
	return strings.HasSuffix(strings.ToLower(cleaned), ".md")
}
