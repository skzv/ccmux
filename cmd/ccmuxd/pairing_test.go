package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
)

// pairTestServer builds the minimal server the pairing handlers touch:
// just a token store and the daemon config they read host/user/port from.
func pairTestServer() *server {
	return &server{
		tokens: daemon.NewTokenStore(),
		cfg: config.Config{
			Daemon: config.DaemonConfig{TailnetPort: 7474, SSHUser: "alice"},
		},
	}
}

// TestHandlePairToken_POST — POST /v1/pair-token issues a hex token and a
// ccmux:// deep-link carrying that token, the configured user, and port;
// the issued token is live in the store.
func TestHandlePairToken_POST(t *testing.T) {
	s := pairTestServer()
	rec := httptest.NewRecorder()
	s.handlePairToken(rec, httptest.NewRequest(http.MethodPost, "/v1/pair-token", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var resp daemon.PairTokenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Token) != 32 {
		t.Fatalf("token %q length %d, want 32 hex chars", resp.Token, len(resp.Token))
	}
	if _, err := hex.DecodeString(resp.Token); err != nil {
		t.Fatalf("token is not hex: %v", err)
	}
	for _, want := range []string{
		"ccmux://pair?host=",
		"&user=alice",
		"&port=7474",
		"&token=" + resp.Token,
	} {
		if !strings.Contains(resp.URL, want) {
			t.Errorf("deep-link %q missing %q", resp.URL, want)
		}
	}
	// The issued token must be registered — consumable exactly once.
	if !s.tokens.Consume(resp.Token) {
		t.Error("the issued token was not registered in the store")
	}
}

// TestHandlePairToken_RejectsGET — the endpoint is POST-only.
func TestHandlePairToken_RejectsGET(t *testing.T) {
	s := pairTestServer()
	rec := httptest.NewRecorder()
	s.handlePairToken(rec, httptest.NewRequest(http.MethodGet, "/v1/pair-token", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", rec.Code)
	}
}

// TestHandlePair_InvalidToken — POST /v1/pair with a token that was never
// issued is rejected and writes no key.
func TestHandlePair_InvalidToken(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := pairTestServer()
	body, _ := json.Marshal(daemon.PairRequest{
		Token:     "bogus",
		PublicKey: "ssh-ed25519 AAAATEST pair@ccmux",
	})
	rec := httptest.NewRecorder()
	s.handlePair(rec, httptest.NewRequest(http.MethodPost, "/v1/pair", bytes.NewReader(body)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for an invalid token", rec.Code)
	}
}

// TestHandlePair_ValidTokenAppendsKey — a valid token pairs successfully:
// the public key is appended to authorized_keys, and the token can't be
// replayed afterward.
func TestHandlePair_ValidTokenAppendsKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	s := pairTestServer()
	tok, err := s.tokens.Create(time.Minute)
	if err != nil {
		t.Fatalf("Create token: %v", err)
	}
	const pubKey = "ssh-ed25519 AAAATESTKEY pair@ccmux"
	reqBody, _ := json.Marshal(daemon.PairRequest{Token: tok, PublicKey: pubKey})

	rec := httptest.NewRecorder()
	s.handlePair(rec, httptest.NewRequest(http.MethodPost, "/v1/pair", bytes.NewReader(reqBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	got, err := os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys"))
	if err != nil {
		t.Fatalf("read authorized_keys: %v", err)
	}
	if !strings.Contains(string(got), pubKey) {
		t.Errorf("authorized_keys does not contain the paired key:\n%s", got)
	}

	// One-time: replaying the same token must now be rejected.
	replay, _ := json.Marshal(daemon.PairRequest{Token: tok, PublicKey: pubKey})
	rec2 := httptest.NewRecorder()
	s.handlePair(rec2, httptest.NewRequest(http.MethodPost, "/v1/pair", bytes.NewReader(replay)))
	if rec2.Code != http.StatusUnauthorized {
		t.Errorf("token replay status = %d, want 401", rec2.Code)
	}
}
