package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// TokenStore issues one-time pair tokens with a TTL.
// Used by `ccmux pair` (unix socket) to generate tokens that a mobile
// client consumes via POST /v1/pair on the tailnet.
type TokenStore struct {
	mu      sync.Mutex
	tokens  map[string]time.Time // token → expiry
	claimed map[string]bool      // tokens held by an unfinished Claim
}

func NewTokenStore() *TokenStore {
	return &TokenStore{tokens: make(map[string]time.Time), claimed: make(map[string]bool)}
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
	if s.claimed[token] {
		return false // an in-flight Claim holds it
	}
	exp, ok := s.tokens[token]
	delete(s.tokens, token)
	return ok && time.Now().Before(exp)
}

// Claim reserves a valid token for one pairing attempt, so the caller
// can do its side effects (write authorized_keys) before the token is
// spent. ok is false for an unknown, expired or already-claimed token,
// so a replay racing the holder is refused. The caller must call done
// once: done(true) burns the token; done(false) hands it back, because
// a failure on the daemon's side (a disk error) shouldn't cost the user
// their one-time token — it stays usable until it expires.
func (s *TokenStore) Claim(token string) (done func(used bool), ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purge()
	exp, ok := s.tokens[token]
	if !ok || !time.Now().Before(exp) || s.claimed[token] {
		return nil, false
	}
	s.claimed[token] = true
	var once sync.Once
	return func(used bool) {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			delete(s.claimed, token)
			if used {
				delete(s.tokens, token)
			}
		})
	}, true
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
