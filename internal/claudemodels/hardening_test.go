package claudemodels

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestService_CanceledCallerDoesNotPoisonCooldownOrCoWaiter(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	srv := New(filepath.Join(t.TempDir(), "models.json"), "test-key")
	srv.CLIFetcher = ClaudeCLIFetcher{Binary: filepath.Join(t.TempDir(), "missing-claude")}
	srv.Fetcher.HTTPDo = func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		if err := req.Context().Err(); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"data":[{"id":"claude-opus-4-7"}],"has_more":false}`)),
		}, nil
	}

	ctxA, cancelA := context.WithCancel(context.Background())
	errA := make(chan error, 1)
	go func() {
		_, err := srv.Refresh(ctxA)
		errA <- err
	}()
	<-started

	// Co-waiter B joins while A cancels.
	catB := make(chan Catalog, 1)
	errB := make(chan error, 1)
	go func() {
		c, err := srv.Refresh(context.Background())
		catB <- c
		errB <- err
	}()
	cancelA()
	if err := <-errA; !errors.Is(err, context.Canceled) {
		t.Fatalf("caller A err = %v, want context.Canceled", err)
	}
	close(release)
	if err := <-errB; err != nil {
		t.Fatalf("co-waiter B failed after A canceled: %v", err)
	}
	if got := <-catB; len(got.Models) == 0 || got.Models[0].ID != "claude-opus-4-7" {
		t.Fatalf("co-waiter B catalog = %+v", got)
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1", calls.Load())
	}
}

func TestService_RefreshPanicDoesNotDeadlock(t *testing.T) {
	var calls atomic.Int32
	srv := New(filepath.Join(t.TempDir(), "models.json"), "test-key")
	srv.CLIFetcher = ClaudeCLIFetcher{Binary: filepath.Join(t.TempDir(), "missing-claude")}
	srv.Fetcher.HTTPDo = func(req *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			panic("boom")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"data":[{"id":"claude-sonnet-4-6"}],"has_more":false}`)),
		}, nil
	}
	if _, err := srv.Refresh(context.Background()); err == nil {
		t.Fatal("first call should return recovered panic error")
	}
	cat, err := srv.Refresh(context.Background())
	if err != nil {
		t.Fatalf("second call after panic should succeed, got %v", err)
	}
	if len(cat.Models) == 0 {
		t.Fatalf("expected models after panic recovery, got %+v", cat)
	}
}

func TestFetcher_StopsOnRepeatedCursorAndEscapesLastID(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if after := r.URL.Query().Get("after_id"); after != "" && after != "id&evil=1" {
			t.Errorf("after_id = %q, want escaped id&evil=1", after)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-opus-4-7"}],"has_more":true,"last_id":"id&evil=1"}`))
	}))
	defer ts.Close()

	f := Fetcher{APIKey: "k", BaseURL: ts.URL, HTTPDo: ts.Client().Do}
	models, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if hits.Load() != 2 {
		t.Errorf("hits = %d, want 2 (initial page + repeated cursor break)", hits.Load())
	}
	if len(models) != 2 {
		t.Errorf("models = %d, want 2", len(models))
	}
}

func TestValidModelID(t *testing.T) {
	for _, good := range []string{"claude-opus-4-7", "anthropic/claude-3.5-sonnet:20241022"} {
		if !validModelID(good) {
			t.Errorf("validModelID(%q) = false, want true", good)
		}
	}
	for _, bad := range []string{"", " ", "-flag", "claude\x1b[31m", "claude opus"} {
		if validModelID(bad) {
			t.Errorf("validModelID(%q) = true, want false", bad)
		}
	}
}

var _ = time.Second
