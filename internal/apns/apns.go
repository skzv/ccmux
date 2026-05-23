// Package apns sends ccmux push notifications to paired iPhones.
//
// This package is dormant by default — Sender.Send is a no-op until
// the daemon's [apns] config provides a key path, key id, team id,
// and topic, AND the matching dev.skz.ccmux push entitlement is in
// the shipped iOS build. Once those land it's a thin shim over
// github.com/sideshow/apns2: parse the .p8 key once, hold one HTTP/2
// client per environment, and POST a tiny JSON payload per event.
package apns

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sideshow/apns2"
	"github.com/sideshow/apns2/token"
)

// Config carries everything the daemon needs to talk to APNs. All
// fields are required when Enabled=true; Enabled=false short-
// circuits Send to a no-op without ever touching disk or network.
type Config struct {
	Enabled     bool
	KeyPath     string // absolute path to AuthKey_XXXXXXXXXX.p8
	KeyID       string // 10-char Key ID from Apple Developer
	TeamID      string // 10-char Team ID
	Topic       string // iOS bundle identifier, e.g. "dev.skz.ccmux"
	Environment string // "production" (default) | "development"
}

// Sender wraps one APNs HTTP/2 client per environment. The client is
// goroutine-safe per the sideshow/apns2 docs, so the daemon shares
// one instance across pollLoop and the request handlers.
type Sender struct {
	cfg Config

	mu     sync.Mutex
	dev    *apns2.Client // sandbox client (development env tokens)
	prod   *apns2.Client // production client
	apnsTk *token.Token
}

// Notification is the daemon-side view of one push: a title/body
// plus the session id, which the iOS app uses to thread together
// repeated notifications for the same session.
type Notification struct {
	Title     string
	Body      string
	SessionID string
}

// New builds a Sender. Errors only when Enabled=true and the keypair
// can't be loaded; a disabled config returns a Sender whose Send is
// a no-op so callers don't have to nil-check.
func New(cfg Config) (*Sender, error) {
	if !cfg.Enabled {
		return &Sender{cfg: cfg}, nil
	}
	keyPath := expandHome(cfg.KeyPath)
	raw, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("apns: read key %q: %w", keyPath, err)
	}
	authKey, err := token.AuthKeyFromBytes(raw)
	if err != nil {
		return nil, fmt.Errorf("apns: parse key: %w", err)
	}
	if strings.TrimSpace(cfg.KeyID) == "" || strings.TrimSpace(cfg.TeamID) == "" {
		return nil, errors.New("apns: KeyID and TeamID required when Enabled=true")
	}
	if strings.TrimSpace(cfg.Topic) == "" {
		return nil, errors.New("apns: Topic required when Enabled=true")
	}
	tk := &token.Token{AuthKey: authKey, KeyID: cfg.KeyID, TeamID: cfg.TeamID}
	return &Sender{
		cfg:    cfg,
		apnsTk: tk,
		prod:   apns2.NewTokenClient(tk).Production(),
		dev:    apns2.NewTokenClient(tk).Development(),
	}, nil
}

// Send pushes one notification to the supplied device token using
// the environment the client registered with. Returns a wrapped
// error from APNs on rejection — the daemon logs but doesn't crash.
func (s *Sender) Send(deviceToken, environment string, n Notification) error {
	if s == nil || !s.cfg.Enabled {
		return nil
	}
	payload, err := json.Marshal(map[string]any{
		"aps": map[string]any{
			"alert": map[string]string{
				"title": n.Title,
				"body":  n.Body,
			},
			"sound":              "default",
			"thread-id":          n.SessionID,
			"interruption-level": "active",
		},
		"session_id": n.SessionID,
	})
	if err != nil {
		return err
	}
	client := s.clientFor(environment)
	resp, err := client.Push(&apns2.Notification{
		DeviceToken: deviceToken,
		Topic:       s.cfg.Topic,
		Payload:     payload,
	})
	if err != nil {
		return fmt.Errorf("apns: push: %w", err)
	}
	if !resp.Sent() {
		return fmt.Errorf("apns: rejected %d %s", resp.StatusCode, resp.Reason)
	}
	return nil
}

// Enabled is a cheap predicate the daemon uses to skip the work of
// building a payload + iterating registrations when push is off.
func (s *Sender) Enabled() bool {
	return s != nil && s.cfg.Enabled
}

func (s *Sender) clientFor(env string) *apns2.Client {
	if env == "development" {
		return s.dev
	}
	return s.prod
}

// expandHome handles "~/path" in the key_path config. The daemon
// runs under launchd in production, where $HOME may be set but
// shells don't run, so we expand it ourselves.
func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
