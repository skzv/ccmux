package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestClient_NoWholeRequestTimeout — regression for the bug where the
// memoized http.Client set a hard 5s Timeout that silently truncated
// long-running remote creates (createProject budgets up to 90s) and
// orphaned sessions. The per-call context must be the only budget, so
// the client carries no whole-request Timeout.
func TestClient_NoWholeRequestTimeout(t *testing.T) {
	resetClientCacheForTest()
	remote := RemoteClient("example:7474")
	if remote.hc.Timeout != 0 {
		t.Errorf("RemoteClient http.Client.Timeout = %v, want 0 (per-call ctx is the budget)", remote.hc.Timeout)
	}
	local, err := LocalClient()
	if err == nil && local != nil && local.hc.Timeout != 0 {
		t.Errorf("LocalClient http.Client.Timeout = %v, want 0", local.hc.Timeout)
	}
}

// TestClient_HonorsCallerDeadline — with no global Timeout, the call
// must still be bounded: a short per-call context must cancel a slow
// request (proving the ctx is wired through, not silently ignored).
func TestClient_HonorsCallerDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	resetClientCacheForTest()
	cli := RemoteClient(strings.TrimPrefix(srv.URL, "http://"))

	// 50ms context against a 300ms server → must fail with a deadline,
	// not hang and not silently succeed.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	var out map[string]any
	err := cli.getJSON(ctx, "/v1/health", &out)
	if err == nil {
		t.Fatal("expected a deadline error on a 50ms ctx vs 300ms server")
	}

	// A generous context against the same server succeeds — proving the
	// old fixed cap no longer truncates legitimate slow calls.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	if err := cli.getJSON(ctx2, "/v1/health", &out); err != nil {
		t.Fatalf("generous ctx should succeed, got: %v", err)
	}
	if out["ok"] != true {
		t.Errorf("decoded body wrong: %+v", out)
	}
}

// TestDecodeCapped_RejectsOversizedBody — the client must not decode an
// unbounded response from a (possibly malicious or buggy) remote daemon.
// A body past maxResponseBytes is truncated by the io.LimitReader, so
// the JSON decode fails rather than allocating proportional memory.
func TestDecodeCapped_RejectsOversizedBody(t *testing.T) {
	// A valid small array decodes fine (didn't break the happy path).
	var small []int
	if err := decodeCapped(strings.NewReader("[1,2,3]"), &small); err != nil {
		t.Fatalf("small body should decode: %v", err)
	}
	if len(small) != 3 {
		t.Errorf("small decode wrong: %v", small)
	}

	// A body larger than the cap: a giant int array. The LimitReader
	// cuts it off mid-stream, so the decoder hits an unexpected EOF.
	var big []int
	huge := "[" + strings.Repeat("0,", (maxResponseBytes/2)+1_000_000) + "0]"
	if err := decodeCapped(strings.NewReader(huge), &big); err == nil {
		t.Error("oversized body should fail to decode (LimitReader truncation), got nil error")
	}
}

// TestEnsureDeadline_OnlyBackstopsWhenMissing — a context that already
// carries a deadline is returned unchanged (its budget honored); a
// deadline-less one gets the defensive backstop.
func TestEnsureDeadline_OnlyBackstopsWhenMissing(t *testing.T) {
	// Caller deadline preserved.
	want := time.Now().Add(7 * time.Second)
	base, cancel := context.WithDeadline(context.Background(), want)
	defer cancel()
	got, c1 := ensureDeadline(base)
	defer c1()
	if dl, ok := got.Deadline(); !ok || dl != want {
		t.Errorf("ensureDeadline altered a caller deadline: got %v ok=%v, want %v", dl, ok, want)
	}

	// Deadline-less context gets a backstop.
	got2, c2 := ensureDeadline(context.Background())
	defer c2()
	if _, ok := got2.Deadline(); !ok {
		t.Error("ensureDeadline should add a backstop to a deadline-less context")
	}
}
