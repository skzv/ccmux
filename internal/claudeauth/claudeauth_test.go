package claudeauth

import (
	"encoding/json"
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

// resetCache clears the package-level cache so tests don't bleed into
// each other. Mirrors what test fixtures normally do via t.Cleanup.
func resetCache(t *testing.T) {
	t.Helper()
	cacheMu.Lock()
	cached = nil
	cachedAt = time.Time{}
	cachedErr = nil
	cacheMu.Unlock()
	t.Cleanup(func() {
		cacheMu.Lock()
		cached = nil
		cachedAt = time.Time{}
		cachedErr = nil
		cacheMu.Unlock()
	})
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
	// Force a stale cache.
	cacheMu.Lock()
	cached = &Status{LoggedIn: true, SubscriptionType: "pro"}
	cachedAt = time.Now().Add(-2 * cacheTTL)
	cacheMu.Unlock()

	// The Get call will see the cache expired and try fetch(), which
	// calls exec.LookPath("claude"). On a host without claude, this
	// returns ("claude not on PATH"). We accept either: claude is
	// installed (no error, real result) OR it isn't (an error). Either
	// way, the *cached old value* must be replaced, not returned.
	_, _ = Get(t.Context())
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if cached == nil {
		t.Fatal("cache slot should now have a fresh entry, even if it's empty")
	}
	// New cachedAt must be set to ~now, not the old stale time.
	if time.Since(cachedAt) > cacheTTL {
		t.Errorf("cachedAt not refreshed: %v", cachedAt)
	}
}
