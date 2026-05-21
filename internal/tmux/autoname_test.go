package tmux

import (
	"sync"
	"testing"
)

// TestAutoSessionName_Unique pins that AutoSessionName never hands out
// a duplicate. A tight loop runs far faster than the millisecond
// resolution the old `c-shell-<UnixMilli>` scheme relied on, so a
// regression back to a timestamp-only name would collide here.
func TestAutoSessionName_Unique(t *testing.T) {
	const n = 2000
	seen := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		name := AutoSessionName("c-shell")
		if seen[name] {
			t.Fatalf("AutoSessionName returned a duplicate after %d calls: %q", i, name)
		}
		seen[name] = true
	}
}

// TestAutoSessionName_ConcurrentUnique pins distinctness under
// concurrent callers — the daemon serves bare-session requests from
// multiple goroutines.
func TestAutoSessionName_ConcurrentUnique(t *testing.T) {
	var mu sync.Mutex
	seen := make(map[string]bool)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				name := AutoSessionName("c-shell")
				mu.Lock()
				dup := seen[name]
				seen[name] = true
				mu.Unlock()
				if dup {
					t.Errorf("concurrent duplicate: %q", name)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// TestAutoSessionName_Prefix pins that the prefix is preserved verbatim.
func TestAutoSessionName_Prefix(t *testing.T) {
	for _, prefix := range []string{"c-shell", "c-resume", "x"} {
		name := AutoSessionName(prefix)
		if len(name) <= len(prefix)+1 || name[:len(prefix)] != prefix {
			t.Errorf("AutoSessionName(%q) = %q, want it to start with the prefix", prefix, name)
		}
	}
}
