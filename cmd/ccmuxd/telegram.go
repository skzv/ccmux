package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/telegram"
)

// tgAllowlist is the live Telegram chat allowlist the bridge's auth
// closures read. Seeded from config at startup and extended on pairing,
// under a mutex because the bridge consults it from its poll goroutine.
type tgAllowlist struct {
	mu  sync.Mutex
	set map[int64]bool
}

func (a *tgAllowlist) allowed(id int64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.set[id]
}

func (a *tgAllowlist) list() []int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]int64, 0, len(a.set))
	for id := range a.set {
		out = append(out, id)
	}
	return out
}

func (a *tgAllowlist) add(id int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.set == nil {
		a.set = map[int64]bool{}
	}
	a.set[id] = true
}

// startTelegram builds and launches the Telegram bridge when configured.
// No-op (one debug line) when disabled or unconfigured, so an ordinary
// daemon is unaffected.
func (s *server) startTelegram(ctx context.Context) {
	tg := s.cfg.Telegram
	if !tg.Enabled || strings.TrimSpace(tg.BotToken) == "" {
		log.Printf("ccmuxd: telegram bridge disabled (enabled=%v, token set=%v)", tg.Enabled, tg.BotToken != "")
		return
	}

	s.tgAllow.set = tg.AllowedChatIDSet()
	if s.tgAllow.set == nil {
		s.tgAllow.set = map[int64]bool{}
	}

	local, err := daemon.LocalClient()
	if err != nil {
		log.Printf("ccmuxd: telegram: local client unavailable: %v", err)
		return
	}
	peers := map[string]telegram.DaemonClient{}
	for _, h := range s.cfg.Hosts {
		if strings.TrimSpace(h.Name) == "" || strings.TrimSpace(h.Address) == "" {
			continue
		}
		peers[h.Name] = daemon.RemoteClient(hostAddr(h))
	}

	// CCMUX_TELEGRAM_API_BASE points the bot at a mock Bot API (e2e
	// tests) instead of api.telegram.org. Unset in normal use.
	var botOpts []telegram.Option
	if base := strings.TrimSpace(os.Getenv("CCMUX_TELEGRAM_API_BASE")); base != "" {
		botOpts = append(botOpts, telegram.WithBaseURL(base))
	}

	muted := tg.MuteAlerts
	bridge := telegram.New(
		telegram.NewClient(tg.BotToken, botOpts...),
		telegram.NewRouter(local, peers),
		telegram.Options{
			AllowExec:     tg.AllowExec,
			PaneTailLines: tg.EffectivePaneTailLines(),
			WebViewerURL:  s.telegramViewerURL(),
			Allowed:       s.tgAllow.allowed,
			Recipients:    s.tgAllow.list,
			Muted:         func() bool { return muted },
			Enroll:        s.enrollTelegramChat,
			Log:           func(f string, a ...any) { log.Printf("ccmuxd: "+f, a...) },
		},
	)
	s.tgBridge = bridge
	go func() {
		if err := bridge.Start(ctx); err != nil && ctx.Err() == nil {
			log.Printf("ccmuxd: telegram bridge stopped: %v", err)
		}
	}()
}

// enrollTelegramChat persists a newly paired chat to config and adds it
// to the live allowlist, so it takes effect immediately and survives a
// restart.
func (s *server) enrollTelegramChat(chatID int64) error {
	s.tgAllow.add(chatID)
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	for _, id := range cfg.Telegram.AllowedChatIDs {
		if id == chatID {
			return nil // already persisted
		}
	}
	cfg.Telegram.AllowedChatIDs = append(cfg.Telegram.AllowedChatIDs, chatID)
	cfg.Telegram.Enabled = true
	return config.Save(cfg)
}

// telegramViewerURL is the base URL of the optional tailnet markdown
// viewer (set by startWebViewer), or "" when the viewer is off.
func (s *server) telegramViewerURL() string {
	return s.viewerBase
}

// handleTelegramPairCode mints a single-use pairing code (unix-socket
// only — it's a pairing secret). 503 when the bridge isn't running.
func (s *server) handleTelegramPairCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.tgBridge == nil {
		http.Error(w, "telegram bridge not enabled", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, daemon.TelegramPairCodeResponse{
		Code:        s.tgBridge.NewPairingCode(),
		BotUsername: s.tgBridge.Username(),
	})
}

// hostAddr builds the "host:port" a peer's ccmuxd listens on (default
// 7474), matching the TUI's RemoteClient addressing.
func hostAddr(h config.Host) string {
	port := h.Port
	if port == 0 {
		port = 7474
	}
	return fmt.Sprintf("%s:%d", h.Address, port)
}
