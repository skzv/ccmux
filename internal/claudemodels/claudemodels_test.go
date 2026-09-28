package claudemodels

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubResponse is the smallest API shape the Fetcher cares about.
// Mirrors the real Anthropic Models API; lets us keep test payloads
// readable instead of pulling in a fixture file.
func stubResponse(t *testing.T, w http.ResponseWriter, models []modelRow, hasMore bool, lastID string) {
	t.Helper()
	body, err := json.Marshal(modelsPage{Data: models, HasMore: hasMore, LastID: lastID})
	if err != nil {
		t.Fatalf("marshal stub: %v", err)
	}
	w.Header().Set("content-type", "application/json")
	if _, err := w.Write(body); err != nil {
		t.Fatalf("write stub: %v", err)
	}
}

func TestFetcher_HappyPath_FlattensCapabilities(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Errorf("missing/wrong x-api-key: %q", got)
		}
		if got := r.Header.Get("anthropic-version"); got != anthropicVersion {
			t.Errorf("missing anthropic-version: %q", got)
		}
		stubResponse(t, w, []modelRow{{
			ID: "claude-opus-4-8", DisplayName: "Claude Opus 4.8",
			MaxInputTokens: 1_000_000, MaxTokens: 128_000,
			Capabilities: map[string]interface{}{
				"image_input":        map[string]interface{}{"supported": true},
				"structured_outputs": map[string]interface{}{"supported": true},
				"thinking": map[string]interface{}{
					"types": map[string]interface{}{
						"adaptive": map[string]interface{}{"supported": true},
					},
				},
				"effort": map[string]interface{}{
					"max": map[string]interface{}{"supported": true},
				},
			},
		}}, false, "")
	}))
	defer srv.Close()

	got, err := (Fetcher{APIKey: "test-key", BaseURL: srv.URL}).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 model, got %d", len(got))
	}
	m := got[0]
	if m.ID != "claude-opus-4-8" || m.Family != "opus" || m.Source != SourceAPI {
		t.Errorf("unexpected model: %+v", m)
	}
	for _, want := range []string{"vision", "structured_outputs", "thinking_adaptive", "effort_max"} {
		if !m.Capabilities[want] {
			t.Errorf("capability %q should be true, got map: %+v", want, m.Capabilities)
		}
	}
}

func TestFetcher_NoAPIKey_ReturnsSentinel(t *testing.T) {
	_, err := (Fetcher{}).Fetch(context.Background())
	if !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("want ErrNoAPIKey, got %v", err)
	}
}

func TestFetcher_NonSuccessIncludesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"type":"authentication_error","message":"invalid x-api-key"}}`))
	}))
	defer srv.Close()

	_, err := (Fetcher{APIKey: "bad", BaseURL: srv.URL}).Fetch(context.Background())
	if err == nil {
		t.Fatal("expected error on 401")
	}
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "invalid x-api-key") {
		t.Errorf("error should surface status + body, got: %v", err)
	}
}

func TestFetcher_FollowsPagination(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch calls {
		case 1:
			if r.URL.Query().Get("after_id") != "" {
				t.Errorf("first page should not carry after_id, got %q", r.URL.Query().Get("after_id"))
			}
			stubResponse(t, w, []modelRow{{ID: "claude-opus-4-8"}}, true, "claude-opus-4-8")
		case 2:
			if got := r.URL.Query().Get("after_id"); got != "claude-opus-4-8" {
				t.Errorf("second page after_id = %q, want %q", got, "claude-opus-4-8")
			}
			stubResponse(t, w, []modelRow{{ID: "claude-sonnet-4-6"}}, false, "")
		default:
			t.Fatalf("unexpected call %d", calls)
		}
	}))
	defer srv.Close()

	got, err := (Fetcher{APIKey: "k", BaseURL: srv.URL}).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got) != 2 || got[0].ID != "claude-opus-4-8" || got[1].ID != "claude-sonnet-4-6" {
		t.Errorf("unexpected pagination result: %+v", got)
	}
}

func TestFamilyOf(t *testing.T) {
	for _, tc := range []struct{ id, want string }{
		{"claude-opus-4-8", "opus"},
		{"claude-sonnet-4-6", "sonnet"},
		{"claude-haiku-4-5", "haiku"},
		{"claude-3-5-sonnet-20241022", "sonnet"},
		{"frobnicator-7", ""},
		{"", ""},
	} {
		if got := familyOf(tc.id); got != tc.want {
			t.Errorf("familyOf(%q) = %q, want %q", tc.id, got, tc.want)
		}
	}
}

func TestSort_FamilyThenNewestID(t *testing.T) {
	in := []Model{
		{ID: "claude-haiku-4-5", Family: "haiku"},
		{ID: "claude-opus-4-7", Family: "opus"},
		{ID: "claude-sonnet-4-6", Family: "sonnet"},
		{ID: "claude-opus-4-8", Family: "opus"},
		{ID: "unknown-model-1", Family: ""},
	}
	Sort(in)
	wantOrder := []string{
		"claude-opus-4-8",
		"claude-opus-4-7",
		"claude-sonnet-4-6",
		"claude-haiku-4-5",
		"unknown-model-1",
	}
	for i, w := range wantOrder {
		if in[i].ID != w {
			t.Errorf("Sort[%d] = %q, want %q (full order: %+v)", i, in[i].ID, w, ids(in))
		}
	}
}

func TestMerge_LiveOverridesFallback_FallbackFillsGaps(t *testing.T) {
	live := []Model{
		{ID: "claude-opus-4-8", DisplayName: "Live Opus", Source: SourceAPI},
	}
	fb := []Model{
		{ID: "claude-opus-4-8", DisplayName: "Stale Opus", Source: SourceFallback},
		{ID: "claude-sonnet-4-6", DisplayName: "Fallback Sonnet", Source: SourceFallback},
	}
	got := Merge(live, fb)
	if len(got) != 2 {
		t.Fatalf("want 2 merged, got %d: %+v", len(got), got)
	}
	if got[0].DisplayName != "Live Opus" || got[0].Source != SourceAPI {
		t.Errorf("live should win for shared ID, got %+v", got[0])
	}
	if got[1].ID != "claude-sonnet-4-6" || got[1].Source != SourceFallback {
		t.Errorf("fallback should fill the gap, got %+v", got[1])
	}
}

func TestCache_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "models.json")
	cache := Cache{Path: path}

	// Initially empty.
	got, err := cache.Read()
	if err != nil {
		t.Fatalf("read empty: %v", err)
	}
	if len(got.Models) != 0 || !got.FetchedAt.IsZero() {
		t.Errorf("empty read should be zero-value, got %+v", got)
	}

	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	cat := Catalog{
		Models:    []Model{{ID: "claude-opus-4-8", Family: "opus", Source: SourceAPI}},
		FetchedAt: now,
		Source:    SourceAPI,
	}
	if err := cache.Write(cat); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("cache file not written: %v", err)
	}

	got, err = cache.Read()
	if err != nil {
		t.Fatalf("read after write: %v", err)
	}
	if !got.FetchedAt.Equal(now) || got.Source != SourceAPI || got.Models[0].ID != "claude-opus-4-8" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestService_NoAPIKey_FallsBackToCurated(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "models.json"), "")
	disableCLIFetcher(s) // CLI is the new first-class source; this test scopes to API
	cat, err := s.Catalog(context.Background())
	if err != nil {
		t.Fatalf("Catalog unexpected error: %v", err)
	}
	if cat.Source != SourceFallback {
		t.Errorf("source should be fallback, got %q", cat.Source)
	}
	gotIDs := ids(cat.Models)
	for _, id := range []string{"claude-opus-4-8", "claude-sonnet-4-6", "claude-haiku-4-5"} {
		if !contains(gotIDs, id) {
			t.Errorf("fallback catalog missing %q (got %v)", id, gotIDs)
		}
	}
	// Sort post-condition: first model is an opus.
	if cat.Models[0].Family != "opus" {
		t.Errorf("first model should be opus, got %q", cat.Models[0].Family)
	}
}

func TestService_LiveFetch_CachesAndMergesFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// API only knows about a brand-new model the curated list
		// doesn't have yet; ensures Merge() lets unknown-to-fallback
		// models surface from the live source.
		stubResponse(t, w, []modelRow{{
			ID: "claude-opus-5-0", DisplayName: "Claude Opus 5.0",
			MaxInputTokens: 2_000_000, MaxTokens: 256_000,
		}}, false, "")
	}))
	defer srv.Close()

	dir := t.TempDir()
	s := New(filepath.Join(dir, "models.json"), "k")
	s.Fetcher.BaseURL = srv.URL
	disableCLIFetcher(s)

	cat, err := s.Catalog(context.Background())
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if cat.Source != SourceAPI {
		t.Errorf("source should be api, got %q", cat.Source)
	}
	got := ids(cat.Models)
	if !contains(got, "claude-opus-5-0") {
		t.Errorf("merged catalog missing live model: %v", got)
	}
	if !contains(got, "claude-sonnet-4-6") {
		t.Errorf("merged catalog missing fallback model: %v", got)
	}

	// Cache hit: second call without re-Fetching should return the
	// same payload (we don't have a hit counter on the test server,
	// but the timestamp on disk is enough to prove we read it back).
	cached, err := s.cache.Read()
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	if cached.FetchedAt.IsZero() {
		t.Error("expected cache to have a non-zero FetchedAt")
	}
}

func TestService_StaleCache_TriggersRefresh(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		stubResponse(t, w, []modelRow{{ID: "claude-opus-4-8"}}, false, "")
	}))
	defer srv.Close()

	dir := t.TempDir()
	cache := Cache{Path: filepath.Join(dir, "models.json")}
	// Pre-seed with an "ancient" cache (8 days ago).
	if err := cache.Write(Catalog{
		Models:    []Model{{ID: "claude-opus-4-7"}},
		FetchedAt: time.Now().Add(-30 * 24 * time.Hour), // stale vs 7d MaxAge
		Source:    SourceAPI,
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	s := New(cache.Path, "k")
	s.Fetcher.BaseURL = srv.URL
	disableCLIFetcher(s)

	if _, err := s.Catalog(context.Background()); err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if calls != 1 {
		t.Errorf("stale cache should trigger 1 refresh, got %d calls", calls)
	}
}

func TestService_RefreshFailure_SurfacesCache(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream"}}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	cache := Cache{Path: filepath.Join(dir, "models.json")}
	seeded := Catalog{
		Models:    []Model{{ID: "claude-opus-4-7", Family: "opus"}},
		FetchedAt: time.Now().Add(-30 * 24 * time.Hour), // stale (>7d MaxAge) → triggers refresh
		Source:    SourceAPI,
	}
	if err := cache.Write(seeded); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s := New(cache.Path, "k")
	s.Fetcher.BaseURL = srv.URL
	disableCLIFetcher(s)

	cat, err := s.Catalog(context.Background())
	if err == nil {
		t.Error("expected the API error to bubble up alongside the fallback catalog")
	}
	if !contains(ids(cat.Models), "claude-opus-4-7") {
		t.Errorf("failed refresh should still surface cached IDs, got %v", ids(cat.Models))
	}
}

func TestFallback_StableShape(t *testing.T) {
	// Snapshot-style guard: every fallback entry has the basics
	// populated. If someone adds a new entry and forgets a field,
	// this catches it before it ships.
	for _, m := range Fallback() {
		if m.ID == "" || m.DisplayName == "" || m.Family == "" {
			t.Errorf("fallback entry missing core field: %+v", m)
		}
		if m.MaxInput == 0 || m.MaxOutput == 0 {
			t.Errorf("fallback entry missing token windows: %+v", m)
		}
		if m.Source != SourceFallback {
			t.Errorf("fallback entry must declare source=fallback: %+v", m)
		}
	}
}

func TestCachePath_RespectsXDG(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	got, err := CachePath()
	if err != nil {
		t.Fatalf("CachePath: %v", err)
	}
	want := filepath.Join(tmp, "ccmux", "models.json")
	if got != want {
		t.Errorf("CachePath = %q, want %q", got, want)
	}
}

// disableCLIFetcher steers a Service so its CLI fetcher is never the
// answer. Tests that scope to the API-or-fallback paths need this
// because the developer's own machine almost certainly has `claude`
// installed AND logged in — left enabled, the chain would happily
// hit the real LLM (billable!) during `go test`.
//
// We disable by pointing Binary at a path that doesn't exist, with no
// Run override: LookPath fails before anything is exec'd, so Fetch
// returns ErrClaudeCLIUnavailable — exactly "claude isn't installed".
// (A CLI that runs and fails is a different case: the chain keeps the
// cached catalog and reports the error; see failingCLIFetcher.)
func disableCLIFetcher(s *Service) {
	s.CLIFetcher = ClaudeCLIFetcher{Binary: "/nonexistent/claude-disabled-in-test"}
}

// failingCLIFetcher makes the CLI tier run and fail — `false` exits
// non-zero, like a `claude -p` that hit a network error or timed out.
func failingCLIFetcher(s *Service) {
	s.CLIFetcher = ClaudeCLIFetcher{
		Binary: "/nonexistent/claude-failing-in-test",
		Run: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "false")
		},
	}
}

// TestService_CLIFailureKeepsGoodCache — with no API key the CLI is
// the only live source. One failed `claude -p` used to stamp a
// fallback-only catalog over a good cached list, so the picker lost
// every discovered model for the next 7 days (MaxAge).
func TestService_CLIFailureKeepsGoodCache(t *testing.T) {
	dir := t.TempDir()
	cache := Cache{Path: filepath.Join(dir, "models.json")}
	good := Catalog{
		Models:    []Model{{ID: "claude-opus-9-9", Family: "opus", Source: SourceClaudeCLI}},
		FetchedAt: time.Now().Add(-8 * 24 * time.Hour).UTC(), // stale → Catalog refreshes
		Source:    SourceClaudeCLI,
	}
	if err := cache.Write(good); err != nil {
		t.Fatal(err)
	}
	s := New(cache.Path, "")
	failingCLIFetcher(s)

	if _, err := s.Refresh(context.Background()); err == nil {
		t.Error("Refresh swallowed the CLI failure")
	}
	got, err := cache.Read()
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != SourceClaudeCLI || !contains(ids(got.Models), "claude-opus-9-9") {
		t.Errorf("cache after a failed CLI refresh = %+v, want the good CLI catalog kept", got)
	}
	if cat := s.Cached(); !contains(ids(cat.Models), "claude-opus-9-9") {
		t.Errorf("Cached() lost the discovered model: %v", ids(cat.Models))
	}
}

// TestService_RefreshIsSingleFlightedAndRateLimited — every forced
// refresh ran the whole chain (a paid `claude -p` for most users), and
// /v1/models?refresh=true is open to any tailnet peer. Concurrent
// callers now share one run, and calls inside MinRefreshInterval reuse
// its result.
func TestService_RefreshIsSingleFlightedAndRateLimited(t *testing.T) {
	var mu sync.Mutex
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		time.Sleep(100 * time.Millisecond) // keep the run in flight while the others arrive
		stubResponse(t, w, []modelRow{{ID: "claude-opus-4-8"}}, false, "")
	}))
	defer srv.Close()

	s := New(filepath.Join(t.TempDir(), "models.json"), "k")
	s.Fetcher.BaseURL = srv.URL
	disableCLIFetcher(s)

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if cat, err := s.Refresh(context.Background()); err != nil || cat.Source != SourceAPI {
				t.Errorf("Refresh = %v source %q, want the shared API result", err, cat.Source)
			}
		}()
	}
	wg.Wait()
	if _, err := s.Refresh(context.Background()); err != nil { // straight after: rate-limited
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 1 {
		t.Errorf("upstream fetched %d times for 11 refreshes, want 1", hits)
	}
}

// TestService_CatalogDoesNotWaitForARunningRefresh — with an empty
// cache, Catalog joined the refresh already in flight (the daemon's
// boot refresh, a `claude -p` call of up to 90s) and blocked until it
// finished. It must answer from the curated list at once, and serve the
// run's result once it lands.
func TestService_CatalogDoesNotWaitForARunningRefresh(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(started) })
		<-release
		stubResponse(t, w, []modelRow{{ID: "claude-opus-9-9"}}, false, "")
	}))
	defer srv.Close()
	releaseOnce := sync.OnceFunc(func() { close(release) })
	defer releaseOnce() // before srv.Close, which waits for the handler

	s := New(filepath.Join(t.TempDir(), "models.json"), "k")
	s.Fetcher.BaseURL = srv.URL
	disableCLIFetcher(s)

	refreshed := make(chan Catalog, 1)
	go func() {
		cat, _ := s.Refresh(context.Background())
		refreshed <- cat
	}()
	<-started

	got := make(chan Catalog, 1)
	go func() {
		cat, _ := s.Catalog(context.Background())
		got <- cat
	}()
	select {
	case cat := <-got:
		if cat.Source != SourceFallback || !cat.FetchedAt.IsZero() || len(cat.Models) != len(Fallback()) {
			t.Errorf("Catalog during a refresh = %s, fetched %v, %d models; want the curated list", cat.Source, cat.FetchedAt, len(cat.Models))
		}
	case <-time.After(2 * time.Second):
		t.Error("Catalog blocked on the refresh already in flight")
	}

	releaseOnce()
	if cat := <-refreshed; cat.Source != SourceAPI {
		t.Fatalf("refresh source = %q, want api", cat.Source)
	}
	if cat, err := s.Catalog(context.Background()); err != nil || !contains(ids(cat.Models), "claude-opus-9-9") {
		t.Errorf("Catalog after the refresh = %v, %v; want the fetched model", ids(cat.Models), err)
	}
}

// TestService_RefreshAgainAfterInterval — the rate limit is a window,
// not a latch.
func TestService_RefreshAgainAfterInterval(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		stubResponse(t, w, []modelRow{{ID: "claude-opus-4-8"}}, false, "")
	}))
	defer srv.Close()
	s := New(filepath.Join(t.TempDir(), "models.json"), "k")
	s.Fetcher.BaseURL = srv.URL
	s.MinRefreshInterval = 20 * time.Millisecond
	disableCLIFetcher(s)

	_, _ = s.Refresh(context.Background())
	time.Sleep(40 * time.Millisecond)
	_, _ = s.Refresh(context.Background())
	if hits != 2 {
		t.Errorf("upstream fetched %d times, want 2 once the interval passed", hits)
	}
}

// helpers
func ids(models []Model) []string {
	out := make([]string, len(models))
	for i, m := range models {
		out[i] = m.ID
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
