package telegram

import (
	"context"
	"fmt"
	"sync"

	"github.com/skzv/ccmux/internal/daemon"
)

// fakeDaemon is a configurable DaemonClient for bridge/router tests. All
// methods are safe for concurrent use so fan-out tests don't race.
type fakeDaemon struct {
	mu sync.Mutex

	sessions    []daemon.SessionState
	sessionsErr error
	previews    map[string]string // session name -> pane content
	agentCmds   map[string]daemon.AgentCommandsResponse
	projects    []daemon.ProjectInfo
	notes       map[string][]daemon.NoteEntry // project -> entries
	noteBodies  map[string]string             // project+"/"+rel -> content
	searchHits  map[string][]daemon.SearchHit // project -> hits
	usage       daemon.AgentUsage
	health      daemon.HealthInfo

	// Recorded mutations, for assertions.
	sentKeys []keyEvent
	killed   []string
	newReqs  []daemon.NewSessionRequest
}

type keyEvent struct{ Name, Keys string }

func (f *fakeDaemon) Sessions(ctx context.Context) ([]daemon.SessionState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sessionsErr != nil {
		return nil, f.sessionsErr
	}
	// Return a copy so callers re-tagging Host don't mutate our store.
	out := make([]daemon.SessionState, len(f.sessions))
	copy(out, f.sessions)
	return out, nil
}

func (f *fakeDaemon) Projects(ctx context.Context) ([]daemon.ProjectInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.projects, nil
}

func (f *fakeDaemon) Preview(ctx context.Context, name string, lines int) (daemon.PreviewResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.previews[name]
	if !ok {
		return daemon.PreviewResponse{}, fmt.Errorf("can't find session %s", name)
	}
	return daemon.PreviewResponse{Lines: lines, Content: c}, nil
}

func (f *fakeDaemon) SendKeys(ctx context.Context, name, keys string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sentKeys = append(f.sentKeys, keyEvent{Name: name, Keys: keys})
	return nil
}

func (f *fakeDaemon) Kill(ctx context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.killed = append(f.killed, name)
	return nil
}

func (f *fakeDaemon) NewSession(ctx context.Context, req daemon.NewSessionRequest) (daemon.SessionState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.newReqs = append(f.newReqs, req)
	return daemon.SessionState{Name: "c-" + req.Project, Host: LocalHost, Project: req.Project, Agent: req.Agent}, nil
}

func (f *fakeDaemon) Usage(ctx context.Context) (daemon.AgentUsage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.usage, nil
}

func (f *fakeDaemon) Notes(ctx context.Context, project string) ([]daemon.NoteEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.notes[project], nil
}

func (f *fakeDaemon) NoteContent(ctx context.Context, project, rel string) (daemon.NoteContent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, ok := f.noteBodies[project+"/"+rel]
	if !ok {
		return daemon.NoteContent{}, fmt.Errorf("file not found")
	}
	return daemon.NoteContent{Rel: rel, Content: body}, nil
}

func (f *fakeDaemon) SearchNotes(ctx context.Context, project, query string) ([]daemon.SearchHit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.searchHits[project], nil
}

func (f *fakeDaemon) AgentCommands(ctx context.Context, name string) (daemon.AgentCommandsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.agentCmds[name], nil
}

func (f *fakeDaemon) Health(ctx context.Context) (daemon.HealthInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.health, nil
}

// recordedKeys returns a copy of the send-keys log.
func (f *fakeDaemon) recordedKeys() []keyEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]keyEvent, len(f.sentKeys))
	copy(out, f.sentKeys)
	return out
}

// fakeBot is a BotAPI that records outgoing calls and serves queued
// updates to the long-poll loop.
type fakeBot struct {
	mu sync.Mutex

	me       *User
	meErr    error
	updates  [][]Update // successive getUpdates batches
	updErr   error
	updIndex int

	sent     []SendMessageRequest
	edits    []EditMessageTextRequest
	answers  []callbackAnswer
	inline   []inlineAnswer
	commands [][]BotCommand
	docs     []sentDoc

	nextMsgID int64
}

type callbackAnswer struct {
	ID, Text  string
	ShowAlert bool
}
type inlineAnswer struct {
	ID      string
	Results []InlineQueryResultArticle
}
type sentDoc struct {
	ChatID   int64
	Filename string
	Content  []byte
	Caption  string
}

func (b *fakeBot) GetMe(ctx context.Context) (*User, error) {
	if b.meErr != nil {
		return nil, b.meErr
	}
	if b.me != nil {
		return b.me, nil
	}
	return &User{ID: 1, IsBot: true, Username: "ccmuxbot"}, nil
}

func (b *fakeBot) GetUpdates(ctx context.Context, offset int64, timeoutSecs int) ([]Update, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.updErr != nil {
		return nil, b.updErr
	}
	if b.updIndex >= len(b.updates) {
		return nil, nil
	}
	batch := b.updates[b.updIndex]
	b.updIndex++
	return batch, nil
}

func (b *fakeBot) SendMessage(ctx context.Context, req SendMessageRequest) (*Message, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sent = append(b.sent, req)
	b.nextMsgID++
	return &Message{MessageID: b.nextMsgID, Chat: Chat{ID: req.ChatID}, Text: req.Text}, nil
}

func (b *fakeBot) EditMessageText(ctx context.Context, req EditMessageTextRequest) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.edits = append(b.edits, req)
	return nil
}

func (b *fakeBot) AnswerCallbackQuery(ctx context.Context, id, text string, showAlert bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.answers = append(b.answers, callbackAnswer{ID: id, Text: text, ShowAlert: showAlert})
	return nil
}

func (b *fakeBot) AnswerInlineQuery(ctx context.Context, id string, results []InlineQueryResultArticle, cacheTimeSecs int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.inline = append(b.inline, inlineAnswer{ID: id, Results: results})
	return nil
}

func (b *fakeBot) SetMyCommands(ctx context.Context, cmds []BotCommand) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.commands = append(b.commands, cmds)
	return nil
}

func (b *fakeBot) SendDocument(ctx context.Context, chatID int64, filename string, content []byte, caption string) (*Message, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.docs = append(b.docs, sentDoc{ChatID: chatID, Filename: filename, Content: content, Caption: caption})
	b.nextMsgID++
	return &Message{MessageID: b.nextMsgID, Chat: Chat{ID: chatID}}, nil
}

// lastSent returns the most recent outgoing message, or false if none.
func (b *fakeBot) lastSent() (SendMessageRequest, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.sent) == 0 {
		return SendMessageRequest{}, false
	}
	return b.sent[len(b.sent)-1], true
}

// sentTexts joins every outgoing message body, for substring assertions.
func (b *fakeBot) sentTexts() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var sb []string
	for _, s := range b.sent {
		sb = append(sb, s.Text)
	}
	return joinLines(sb)
}

func joinLines(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += "\n----\n"
		}
		out += s
	}
	return out
}
