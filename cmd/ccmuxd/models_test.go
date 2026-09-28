package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/claudemodels"
)

// TestHandleModels_NoAPIKey_ReturnsFallback — the GET /v1/models
// happy-path when no ANTHROPIC_API_KEY is set: the handler must still
// return 200 with the curated fallback list. This is the path the
// dominant ccmux user (subscription, no API key) hits every time.
func TestHandleModels_NoAPIKey_ReturnsFallback(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir) // sandboxes the cache file
	svc := claudemodels.New(filepath.Join(dir, "models.json"), "")
	disableTestCLIFetcher(svc)
	srv := &server{models: svc}

	r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()
	srv.handleModels(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var cat claudemodels.Catalog
	if err := json.Unmarshal(w.Body.Bytes(), &cat); err != nil {
		t.Fatalf("body is not JSON: %v\n%s", err, w.Body.String())
	}
	if cat.Source != claudemodels.SourceFallback {
		t.Errorf("source = %q, want fallback", cat.Source)
	}
	if len(cat.Models) == 0 {
		t.Errorf("fallback catalog should be non-empty")
	}
	// Spot-check a curated entry — keeps a future fallback rewrite
	// from silently dropping the headline model.
	foundOpus := false
	for _, m := range cat.Models {
		if strings.HasPrefix(m.ID, "claude-opus-") {
			foundOpus = true
			break
		}
	}
	if !foundOpus {
		t.Errorf("fallback catalog missing an opus entry: %+v", cat.Models)
	}
}

// TestHandleModels_RejectsNonGET — the daemon's other endpoints all
// 405 on the wrong method; /v1/models should too. Hardened against a
// future accidental refactor that drops the method guard.
func TestHandleModels_RejectsNonGET(t *testing.T) {
	srv := &server{models: claudemodels.New("", "")}
	r := httptest.NewRequest(http.MethodPost, "/v1/models", nil)
	w := httptest.NewRecorder()
	srv.handleModels(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /v1/models = %d, want 405", w.Code)
	}
}

// TestHandleModels_NilService_DegradesGracefully — defensive: a future
// constructor variant that forgets to populate srv.models must not 500.
// The handler returns the in-binary fallback list synthesised inline.
func TestHandleModels_NilService_DegradesGracefully(t *testing.T) {
	srv := &server{}
	r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()
	srv.handleModels(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("nil-models status = %d, want 200", w.Code)
	}
	var cat claudemodels.Catalog
	if err := json.Unmarshal(w.Body.Bytes(), &cat); err != nil {
		t.Fatalf("nil-models body not JSON: %v", err)
	}
	if cat.Source != claudemodels.SourceFallback || len(cat.Models) == 0 {
		t.Errorf("nil-models response should be the bare fallback list: %+v", cat)
	}
}

// TestHandleModels_RefreshQuery_HitsAPI — when ?refresh=true is set,
// the handler must call the API surface, not just read the cache.
// Pointed at a fake upstream so the test stays offline.
func TestHandleModels_RefreshQuery_HitsAPI(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-opus-9-9","display_name":"Test Opus","max_input_tokens":1000000,"max_tokens":128000}],"has_more":false}`))
	}))
	defer upstream.Close()

	dir := t.TempDir()
	svc := claudemodels.New(filepath.Join(dir, "models.json"), "k")
	disableTestCLIFetcher(svc)
	// This test is about ?refresh=true reaching upstream at all; the
	// rate limit has its own test below.
	svc.MinRefreshInterval = 0
	// Reach in to swap the BaseURL on the embedded Fetcher. Tests in
	// the claudemodels package do the same thing; package-internal
	// access is fine because both files share the `claudemodels` import.
	// This is hacky-feeling but cleaner than adding a SetBaseURL
	// setter on Service just for tests; see the package's own test
	// file for the same pattern.
	swapFetcherBaseURL(t, svc, upstream.URL)

	srv := &server{models: svc}

	// Cache miss → first GET (no refresh param) already triggers a
	// fetch via Service.Catalog. Burn that one off so the count below
	// reflects the explicit refresh.
	r0 := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w0 := httptest.NewRecorder()
	srv.handleModels(w0, r0)
	if w0.Code != http.StatusOK {
		t.Fatalf("first GET status = %d", w0.Code)
	}
	if calls != 1 {
		t.Fatalf("first GET should have hit upstream once, got %d", calls)
	}

	// Now force a refresh — must hit upstream again even though the
	// cache is fresh.
	r := httptest.NewRequest(http.MethodGet, "/v1/models?refresh=true", nil)
	w := httptest.NewRecorder()
	srv.handleModels(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("refresh=true status = %d", w.Code)
	}
	if calls != 2 {
		t.Errorf("?refresh=true should have hit upstream a second time, got %d total calls", calls)
	}

	var cat claudemodels.Catalog
	if err := json.Unmarshal(w.Body.Bytes(), &cat); err != nil {
		t.Fatalf("response not JSON: %v", err)
	}
	if cat.Source != claudemodels.SourceAPI {
		t.Errorf("source = %q, want api", cat.Source)
	}
	// The live API row should be present alongside the curated fallback
	// (Merge keeps both).
	foundTest := false
	for _, m := range cat.Models {
		if m.ID == "claude-opus-9-9" {
			foundTest = true
		}
	}
	if !foundTest {
		t.Errorf("refreshed catalog missing upstream model: %+v", cat.Models)
	}

	_ = context.Background
}

// swapFetcherBaseURL points a Service's Fetcher at a stub upstream
// for tests. claudemodels.Service.Fetcher is exposed (public field) so
// this is a one-line poke rather than reflection.
func swapFetcherBaseURL(t *testing.T, s *claudemodels.Service, baseURL string) {
	t.Helper()
	s.Fetcher.BaseURL = baseURL
}

// disableTestCLIFetcher prevents the CLI tier of the discovery chain
// from accidentally running during tests. The developer's machine
// likely has `claude` installed and logged in, which would mean the
// chain answers via a real LLM call (billable, slow, non-deterministic).
// A Binary that doesn't exist (and no Run override) makes the tier
// report "claude isn't installed", so the chain falls through to the
// API tier the tests actually want to exercise.
func disableTestCLIFetcher(s *claudemodels.Service) {
	s.CLIFetcher = claudemodels.ClaudeCLIFetcher{Binary: "/nonexistent/claude-disabled-in-test"}
}

// countingUpstream is a fake Anthropic Models API that counts requests.
// status != 200 makes every request fail.
func countingUpstream(t *testing.T, status int, delay time.Duration) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(delay)
		if status != http.StatusOK {
			http.Error(w, `{"error":{"message":"upstream down"}}`, status)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-opus-9-9","display_name":"Test Opus"}],"has_more":false}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// TestHandleModels_ForcedRefreshBurstFetchesOnce — any tailnet peer can
// send ?refresh=true, and each one ran the whole discovery chain (for
// most users a paid `claude -p` call). A burst of them now shares one
// fetch.
func TestHandleModels_ForcedRefreshBurstFetchesOnce(t *testing.T) {
	upstream, hits := countingUpstream(t, http.StatusOK, 100*time.Millisecond)
	svc := claudemodels.New(filepath.Join(t.TempDir(), "models.json"), "k")
	disableTestCLIFetcher(svc)
	swapFetcherBaseURL(t, svc, upstream.URL)
	srv := &server{models: svc}

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			srv.handleModels(w, httptest.NewRequest(http.MethodGet, "/v1/models?refresh=true", nil))
			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want 200", w.Code)
			}
		}()
	}
	wg.Wait()
	if got := hits.Load(); got != 1 {
		t.Errorf("10 concurrent forced refreshes fetched upstream %d times, want 1", got)
	}
}

// TestHandleModels_FailedForcedRefreshDoesNotRetry — when a forced
// refresh failed, the handler fell back to Catalog, which (with a stale
// cache) ran the whole chain again: two paid calls per request.
func TestHandleModels_FailedForcedRefreshDoesNotRetry(t *testing.T) {
	upstream, hits := countingUpstream(t, http.StatusInternalServerError, 0)
	dir := t.TempDir()
	cache := claudemodels.Cache{Path: filepath.Join(dir, "models.json")}
	if err := cache.Write(claudemodels.Catalog{
		Models:    []claudemodels.Model{{ID: "claude-opus-7-7", Family: "opus"}},
		FetchedAt: time.Now().Add(-30 * 24 * time.Hour),
		Source:    claudemodels.SourceAPI,
	}); err != nil {
		t.Fatal(err)
	}
	svc := claudemodels.New(cache.Path, "k")
	disableTestCLIFetcher(svc)
	swapFetcherBaseURL(t, svc, upstream.URL)
	srv := &server{models: svc}

	w := httptest.NewRecorder()
	srv.handleModels(w, httptest.NewRequest(http.MethodGet, "/v1/models?refresh=true", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with the cached catalog", w.Code)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("one failed forced refresh fetched upstream %d times, want 1", got)
	}
	var cat claudemodels.Catalog
	if err := json.Unmarshal(w.Body.Bytes(), &cat); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range cat.Models {
		found = found || m.ID == "claude-opus-7-7"
	}
	if !found {
		t.Errorf("response lost the cached model: %+v", cat.Models)
	}
}
