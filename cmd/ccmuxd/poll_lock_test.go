package main

import (
	"sync"
	"testing"
)

// TestServerMutex_NoContention is a smoke-check that the server's mutex
// itself doesn't deadlock under concurrent acquire/release. The richer
// lock-contention property — that pollOnce releases s.mu during the
// capture phase so IPC handlers don't block on it — is exercised by
// the integration tests in poll_integration_test.go via a real tmux
// server. Without a tmux fake at this layer (tmux.List isn't a seam),
// a unit test here would either need a running tmux server (not
// portable) or skip the path entirely (not useful).
func TestServerMutex_NoContention(t *testing.T) {
	srv := &server{seen: map[string]*tracked{}}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			srv.mu.Lock()
			_ = len(srv.seen)
			srv.mu.Unlock()
		}()
	}
	wg.Wait()
}
