package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// TokenStore issues one-time pair tokens with a TTL.
// Used by `ccmux pair` (unix socket) to generate tokens that the iOS
// app consumes via POST /v1/pair on the tailnet.
type TokenStore struct {
	mu     sync.Mutex
	tokens map[string]time.Time // token → expiry
}

func NewTokenStore() *TokenStore {
	return &TokenStore{tokens: make(map[string]time.Time)}
}

// Create generates a new random 128-bit token with the given TTL.
func (s *TokenStore) Create(ttl time.Duration) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	s.mu.Lock()
	s.purge()
	s.tokens[token] = time.Now().Add(ttl)
	s.mu.Unlock()
	return token, nil
}

// Consume validates and burns the token. Returns true if valid and not expired.
func (s *TokenStore) Consume(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Purge here too: if a daemon mints one token and then runs for
	// weeks without minting another, the original Create-only sweep
	// never fires, and expired-but-not-purged entries pile up across
	// many failed pair attempts.
	s.purge()
	exp, ok := s.tokens[token]
	delete(s.tokens, token)
	return ok && time.Now().Before(exp)
}

// purge removes expired tokens (call with mu held).
func (s *TokenStore) purge() {
	now := time.Now()
	for t, exp := range s.tokens {
		if now.After(exp) {
			delete(s.tokens, t)
		}
	}
}
