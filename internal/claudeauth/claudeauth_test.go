package claudeauth

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestTier_NormalizesAnthropicLabels(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// Pro.
		{"pro", "pro"},
		{"PRO", "pro"},
		{"  pro  ", "pro"},
		// Max5x has multiple forms in the wild.
		{"max", "max5x"},
		{"max-5x", "max5x"},
		{"max_5x", "max5x"},
		{"MAX5X", "max5x"},
		// Max20x.
		{"max-20x", "max20x"},
		{"max20x", "max20x"},
		{"max_20x", "max20x"},
		// Everything else.
		{"", "api"},
		{"api", "api"},
		{"api-key", "api"},
		{"future-tier-we-dont-know", "api"},
	}
	for _, tc := range cases {
		got := (Status{SubscriptionType: tc.in}).Tier()
		if got != tc.want {
			t.Errorf("Tier(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestStatus_JSONShapeRoundTrip(t *testing.T) {
	body := `{
  "loggedIn": true,
  "authMethod": "claude.ai",
  "apiProvider": "firstParty",
  "email": "x@y.z",
  "orgId": "org-123",
  "orgName": "Acme",
  "subscriptionType": "max-20x"
}`
	var s Status
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatal(err)
	}
	if !s.LoggedIn || s.AuthMethod != "claude.ai" || s.Email != "x@y.z" || s.OrgName != "Acme" {
		t.Errorf("unmarshalled wrong: %+v", s)
	}
	if s.Tier() != "max20x" {
		t.Errorf("Tier from JSON: %q, want max20x", s.Tier())
	}
}

func TestStatus_MissingFieldsTolerated(t *testing.T) {
	var s Status
	if err := json.Unmarshal([]byte(`{}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.LoggedIn || s.Email != "" || s.SubscriptionType != "" {
		t.Errorf("zero-value: %+v", s)
	}
	// Empty SubscriptionType maps to api.
	if s.Tier() != "api" {
		t.Errorf("Tier for empty subscription = %q, want api", s.Tier())
	}
}

// resetCache clears the package-level cache (and restores the real
// fetcher) so tests don't bleed into each other. Mirrors what test
// fixtures normally do via t.Cleanup.
func resetCache(t *testing.T) {
	t.Helper()
	clear := func() {
		cacheMu.Lock()
		cached = nil
		cachedAt = time.Time{}
		cachedErr = nil
		lastAttempt = time.Time{}
		fetchFn = fetch
		cacheMu.Unlock()
	}
	clear()
	t.Cleanup(clear)
}

// TestCache_RemembersBetweenCalls smoke-tests that `Get` reuses a cached
// Status without re-shelling. We can't easily stub the `claude` binary,
// but we can populate the cache directly and verify a quick re-read
// returns the same value (no error from a missing binary).
func TestCache_RemembersBetweenCalls(t *testing.T) {
	resetCache(t)

	cacheMu.Lock()
	cached = &Status{LoggedIn: true, SubscriptionType: "max-20x", Email: "x@y"}
	cachedAt = time.Now()
	cachedErr = nil
	cacheMu.Unlock()

	got, err := Get(t.Context())
	if err != nil {
		t.Fatalf("Get inside TTL should not error: %v", err)
	}
	if !got.LoggedIn || got.Tier() != "max20x" {
		t.Fatalf("unexpected cached value: %+v", got)
	}
}

func TestCache_ExpiresAfterTTL(t *testing.T) {
	resetCache(t)
	// Force a stale cache; a successful re-fetch must replace it.
	cacheMu.Lock()
	cached = &Status{LoggedIn: true, SubscriptionType: "pro"}
	cachedAt = time.Now().Add(-2 * cacheTTL)
	fetchFn = func(context.Context) (Status, error) {
		return Status{LoggedIn: true, SubscriptionType: "max-20x"}, nil
	}
	cacheMu.Unlock()

	got, err := Get(t.Context())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Tier() != "max20x" {
		t.Fatalf("expired cache must be re-fetched: got %+v", got)
	}
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if cached == nil || cached.SubscriptionType != "max-20x" {
		t.Fatalf("cache slot not refreshed: %+v", cached)
	}
	if time.Since(cachedAt) > cacheTTL {
		t.Errorf("cachedAt not refreshed: %v", cachedAt)
	}
}

// TestGet_ServesStaleOnFetchFailure — regression for the negative-cache
// clobber: a single transient fetch failure (cold `claude` boot blowing
// the 3s timeout) used to overwrite a good cached Status with
// Status{}+error for the full 5-minute TTL, sizing the quota bar as
// "api". A failure must instead keep serving the last good value.
func TestGet_ServesStaleOnFetchFailure(t *testing.T) {
	resetCache(t)

	calls := 0
	cacheMu.Lock()
	fetchFn = func(context.Context) (Status, error) {
		calls++
		if calls == 1 {
			return Status{LoggedIn: true, SubscriptionType: "max-20x"}, nil
		}
		return Status{}, errors.New("claude boot too slow")
	}
	cacheMu.Unlock()

	// First call: good result cached.
	got, err := Get(t.Context())
	if err != nil || got.Tier() != "max20x" {
		t.Fatalf("first Get = %+v, %v", got, err)
	}

	// Expire the good cache, then fail the re-fetch: the stale good
	// value must still be served, with no error.
	cacheMu.Lock()
	cachedAt = time.Now().Add(-2 * cacheTTL)
	cacheMu.Unlock()
	got, err = Get(t.Context())
	if err != nil {
		t.Fatalf("Get after failed re-fetch must serve stale without error, got %v", err)
	}
	if got.Tier() != "max20x" {
		t.Fatalf("Get after failed re-fetch = %+v, want the previous good status", got)
	}
	if calls != 2 {
		t.Fatalf("fetcher calls = %d, want 2", calls)
	}
}

// TestGet_RetriesAfterNegativeTTL — a failure is negatively cached for
// ~10s (not the full 5min), and within that window Get doesn't hammer
// the CLI.
func TestGet_RetriesAfterNegativeTTL(t *testing.T) {
	resetCache(t)

	calls := 0
	cacheMu.Lock()
	fetchFn = func(context.Context) (Status, error) {
		calls++
		return Status{}, errors.New("claude not on PATH")
	}
	cacheMu.Unlock()

	// No previous good value: the error surfaces.
	if _, err := Get(t.Context()); err == nil {
		t.Fatal("Get with no cache and a failing fetch must return the error")
	}
	// Immediately again: negative cache suppresses the retry.
	if _, err := Get(t.Context()); err == nil {
		t.Fatal("Get inside the negative TTL must still report the error")
	}
	if calls != 1 {
		t.Fatalf("fetcher calls = %d, want 1 (negative cache must suppress immediate retries)", calls)
	}

	// After the (short) negative TTL, the fetcher is retried.
	cacheMu.Lock()
	lastAttempt = time.Now().Add(-2 * negativeTTL)
	cacheMu.Unlock()
	_, _ = Get(t.Context())
	if calls != 2 {
		t.Fatalf("fetcher calls = %d, want 2 (negative TTL expiry must retry)", calls)
	}
}
