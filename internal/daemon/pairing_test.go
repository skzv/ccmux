package daemon

import (
	"encoding/hex"
	"sync"
	"testing"
	"time"
)

// TestTokenStore_CreateConsume — a freshly issued token is a 128-bit hex
// string and consumes successfully exactly once.
func TestTokenStore_CreateConsume(t *testing.T) {
	s := NewTokenStore()
	tok, err := s.Create(time.Minute)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(tok) != 32 {
		t.Fatalf("token %q length %d, want 32 hex chars (16 bytes)", tok, len(tok))
	}
	if _, err := hex.DecodeString(tok); err != nil {
		t.Fatalf("token %q is not hex: %v", tok, err)
	}
	if !s.Consume(tok) {
		t.Fatal("Consume of a fresh token returned false")
	}
}

// TestTokenStore_ConsumeIsOneTime — Consume burns the token; a second
// Consume of the same value fails. This is the replay defence.
func TestTokenStore_ConsumeIsOneTime(t *testing.T) {
	s := NewTokenStore()
	tok, _ := s.Create(time.Minute)
	if !s.Consume(tok) {
		t.Fatal("first Consume returned false")
	}
	if s.Consume(tok) {
		t.Fatal("second Consume returned true — the token was not burned")
	}
}

// TestTokenStore_ConsumeUnknown — tokens that were never issued (and the
// empty string) never validate.
func TestTokenStore_ConsumeUnknown(t *testing.T) {
	s := NewTokenStore()
	if s.Consume("") {
		t.Error("Consume of the empty token returned true")
	}
	if s.Consume("deadbeefdeadbeefdeadbeefdeadbeef") {
		t.Error("Consume of an unissued token returned true")
	}
}

// TestTokenStore_ConsumeExpired — a token past its TTL fails Consume even
// though it was legitimately issued.
func TestTokenStore_ConsumeExpired(t *testing.T) {
	s := NewTokenStore()
	tok, _ := s.Create(-time.Second) // already expired on creation
	if s.Consume(tok) {
		t.Fatal("Consume of an expired token returned true")
	}
}

// TestTokenStore_CreateUnique — Create never repeats a token.
func TestTokenStore_CreateUnique(t *testing.T) {
	s := NewTokenStore()
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		tok, err := s.Create(time.Minute)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if seen[tok] {
			t.Fatalf("Create returned a duplicate token: %s", tok)
		}
		seen[tok] = true
	}
}

// TestTokenStore_PurgeDropsExpired — Create purges expired entries before
// inserting, so a store churned with dead tokens doesn't leak memory.
func TestTokenStore_PurgeDropsExpired(t *testing.T) {
	s := NewTokenStore()
	if _, err := s.Create(-time.Second); err != nil { // expired
		t.Fatal(err)
	}
	if _, err := s.Create(-time.Second); err != nil { // expired
		t.Fatal(err)
	}
	fresh, err := s.Create(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	n := len(s.tokens)
	s.mu.Unlock()
	if n != 1 {
		t.Fatalf("after purge the store holds %d tokens, want 1", n)
	}
	if !s.Consume(fresh) {
		t.Fatal("the fresh token was purged along with the expired ones")
	}
}

// TestTokenStore_Concurrent — Create/Consume are safe under concurrent
// use (meaningful under `go test -race`).
func TestTokenStore_Concurrent(t *testing.T) {
	s := NewTokenStore()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tok, err := s.Create(time.Minute)
			if err != nil {
				t.Errorf("Create: %v", err)
				return
			}
			if !s.Consume(tok) {
				t.Error("Consume of an own freshly-created token returned false")
			}
		}()
	}
	wg.Wait()
}
