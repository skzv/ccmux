package claudeauth

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func resetAuthCache() {
	cacheMu.Lock()
	cached = nil
	cachedAt = time.Time{}
	cachedErr = nil
	lastAttempt = time.Time{}
	inflight = nil
	cacheMu.Unlock()
}

func TestGet_SingleFlightsAndDoesNotCacheCanceledContext(t *testing.T) {
	resetAuthCache()
	t.Cleanup(resetAuthCache)

	// An already-canceled caller must not trigger a fetch or poison negativeTTL.
	orig := fetchFn
	t.Cleanup(func() { fetchFn = orig })
	var calls atomic.Int32
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	fetchFn = func(ctx context.Context) (Status, error) {
		calls.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		return Status{LoggedIn: true, SubscriptionType: "max"}, nil
	}

	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Get(cctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Get = %v, want context.Canceled", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("canceled Get invoked fetchFn %d times", calls.Load())
	}

	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, err := Get(context.Background())
			if err != nil {
				errs <- err
				return
			}
			if !st.LoggedIn || st.SubscriptionType != "max" {
				errs <- errors.New("unexpected status")
			}
		}()
	}
	<-started
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Get failed: %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("concurrent Get invoked fetchFn %d times, want 1", calls.Load())
	}
}
