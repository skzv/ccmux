package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/telegram"
)

// TestTelegram_AgentCommandsEndpointE2E drives the agent-commands path
// the Telegram bridge uses, all the way through a real daemon HTTP
// server and a real daemon.Client — no fakes on the wire. Without a tmux
// session the agent resolves to the default (claude), so the catalog is
// claude's built-ins.
func TestTelegram_AgentCommandsEndpointE2E(t *testing.T) {
	cfg := config.Defaults()
	cfg.Projects.Root = t.TempDir()
	s := newServer(cfg)

	mux := http.NewServeMux()
	s.routes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	cli := daemon.RemoteClient(strings.TrimPrefix(ts.URL, "http://"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := cli.AgentCommands(ctx, "anysession")
	if err != nil {
		t.Fatalf("AgentCommands over HTTP: %v", err)
	}
	if resp.Agent != "claude" {
		t.Errorf("agent = %q, want claude (default without a live session)", resp.Agent)
	}
	var sawModel bool
	for _, c := range resp.Commands {
		if c.Name == "/model" {
			sawModel = true
		}
	}
	if !sawModel {
		t.Errorf("claude catalog over the wire should include /model: %+v", resp.Commands)
	}
}

// TestTelegram_NotifyOverRealClientE2E exercises the alert path through
// the real telegram.Client (real HTTP marshaling) against a mock Bot
// API, confirming a needs-input alert reaches the configured chat.
func TestTelegram_NotifyOverRealClientE2E(t *testing.T) {
	var (
		mu       sync.Mutex
		gotChat  int64
		gotText  string
		gotReply bool
	)
	tgmock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			raw, _ := io.ReadAll(r.Body)
			var body struct {
				ChatID      int64           `json:"chat_id"`
				Text        string          `json:"text"`
				ReplyMarkup json.RawMessage `json:"reply_markup"`
			}
			_ = json.Unmarshal(raw, &body)
			mu.Lock()
			gotChat = body.ChatID
			gotText = body.Text
			gotReply = len(body.ReplyMarkup) > 0
			mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1,"chat":{"id":7,"type":"private"}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer tgmock.Close()

	bot := telegram.NewClient("tok", telegram.WithBaseURL(tgmock.URL))
	bridge := telegram.New(bot, telegram.NewRouter(nil, nil), telegram.Options{
		Allowed:    func(int64) bool { return true },
		Recipients: func() []int64 { return []int64{7} },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	bridge.Notify(ctx, "local", "build", "Allow edit? (y/n)", "c1")

	mu.Lock()
	defer mu.Unlock()
	if gotChat != 7 {
		t.Errorf("alert chat = %d, want 7", gotChat)
	}
	if !strings.Contains(gotText, "build") {
		t.Errorf("alert text should name the session: %q", gotText)
	}
	if !gotReply {
		t.Errorf("alert should carry an approve/deny keyboard")
	}
}
