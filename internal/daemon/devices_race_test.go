package daemon

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

// TestDeviceStore_ConcurrentRegisterPersistsAll — finding 5b: flush
// snapshotted the map under the lock but ran marshal+write+rename
// outside any lock, so two concurrent Registers could interleave with
// the OLDER snapshot renaming last — silently dropping the newer
// registration from disk. With the flush mutex serializing
// snapshot→rename, every registration must survive a reload. Run with
// -race to also catch raw memory races on the flush path.
func TestDeviceStore_ConcurrentRegisterPersistsAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.json")
	s, err := OpenDeviceStore(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	const n = 32
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("ssh-ed25519 AAAAkey%02d phone-%02d", i, i)
			if err := s.Register(key, fmt.Sprintf("tok%02d", i), "production"); err != nil {
				t.Errorf("register %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	reloaded, err := OpenDeviceStore(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := len(reloaded.All()); got != n {
		t.Fatalf("reloaded store has %d registrations, want %d — a concurrent flush persisted a stale snapshot", got, n)
	}
}
