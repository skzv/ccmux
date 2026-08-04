package ccusage

import (
	"context"
	"errors"
	"testing"
	"time"
)

const twoBlocksJSON = `{
  "blocks": [
    {
      "id": "2026-05-19T17:00:00.000Z",
      "startTime": "2026-05-19T17:00:00.000Z",
      "endTime": "2026-05-19T22:00:00.000Z",
      "isActive": false,
      "isGap": false,
      "costUSD": 12.50,
      "totalTokens": 5000000,
      "models": ["claude-sonnet-4-6"],
      "tokenCounts": {
        "inputTokens": 100,
        "outputTokens": 200,
        "cacheCreationInputTokens": 300,
        "cacheReadInputTokens": 400
      },
      "burnRate": {"costPerHour": 2.50},
      "projection": {"totalCost": 15.00}
    },
    {
      "id": "2026-05-19T22:00:00.000Z",
      "startTime": "2026-05-19T22:00:00.000Z",
      "endTime": "2026-05-20T03:00:00.000Z",
      "isActive": true,
      "isGap": false,
      "costUSD": 48.21,
      "totalTokens": 92363539,
      "models": ["claude-opus-4-7", "claude-sonnet-4-6"],
      "tokenCounts": {
        "inputTokens": 363,
        "outputTokens": 84864,
        "cacheCreationInputTokens": 221045,
        "cacheReadInputTokens": 92057267
      },
      "burnRate": {"costPerHour": 25.11},
      "projection": {"totalCost": 125.22}
    }
  ]
}`

const noActiveJSON = `{
  "blocks": [
    {
      "id": "2026-05-19T12:00:00.000Z",
      "startTime": "2026-05-19T12:00:00.000Z",
      "endTime": "2026-05-19T17:00:00.000Z",
      "isActive": false,
      "isGap": false,
      "costUSD": 5.00,
      "totalTokens": 1000000,
      "models": ["claude-sonnet-4-6"],
      "tokenCounts": {
        "inputTokens": 10,
        "outputTokens": 20,
        "cacheCreationInputTokens": 30,
        "cacheReadInputTokens": 40
      },
      "burnRate": {"costPerHour": 1.00},
      "projection": {"totalCost": 5.00}
    },
    {
      "id": "2026-05-19T17:00:00.000Z",
      "startTime": "2026-05-19T17:00:00.000Z",
      "endTime": "2026-05-19T22:00:00.000Z",
      "isActive": false,
      "isGap": false,
      "costUSD": 9.99,
      "totalTokens": 3000000,
      "models": ["claude-opus-4-7"],
      "tokenCounts": {
        "inputTokens": 50,
        "outputTokens": 100,
        "cacheCreationInputTokens": 150,
        "cacheReadInputTokens": 200
      },
      "burnRate": {"costPerHour": 2.00},
      "projection": {"totalCost": 10.00}
    }
  ]
}`

const emptyBlocksJSON = `{"blocks": []}`

func fakeRunner(t *testing.T, payload []byte, cmdErr error) {
	t.Helper()
	resetCache(t)
	orig := runCmd
	t.Cleanup(func() { runCmd = orig })
	runCmd = func(_ context.Context, _ ...string) ([]byte, error) {
		return payload, cmdErr
	}
}

// resetCache clears the memoized block and restores the clock seam so
// each test starts from a cold cache regardless of test order.
func resetCache(t *testing.T) {
	t.Helper()
	InvalidateCache()
	origNow := nowFn
	t.Cleanup(func() {
		nowFn = origNow
		InvalidateCache()
	})
}

// countingRunner installs a fake executor that records how many times
// the subprocess would actually have been spawned.
func countingRunner(t *testing.T, payload []byte) *int {
	t.Helper()
	resetCache(t)
	orig := runCmd
	t.Cleanup(func() { runCmd = orig })
	calls := 0
	runCmd = func(_ context.Context, _ ...string) ([]byte, error) {
		calls++
		return payload, nil
	}
	return &calls
}

// TestCurrentBlock_CachesWithinTTL pins the fix for the npx poll storm.
// The dashboard calls CurrentBlock every 15s; each real call shells out
// to `npx ccusage`, which contacts the npm registry (and installs the
// package when it's not cached). Left open, a ccmux window generated one
// npm invocation — and one ~/.npm/_logs debug log — every 15 seconds.
//
// Repeated calls inside the TTL must reuse the memoized block and spawn
// nothing. Before the fix this counted one spawn per call.
func TestCurrentBlock_CachesWithinTTL(t *testing.T) {
	calls := countingRunner(t, []byte(twoBlocksJSON))

	// 20 calls stands in for five minutes of 15s dashboard ticks.
	var last Block
	for i := 0; i < 20; i++ {
		b, err := CurrentBlock(context.Background())
		if err != nil {
			t.Fatalf("CurrentBlock #%d: %v", i, err)
		}
		last = b
	}

	if *calls != 1 {
		t.Errorf("ccusage spawned %d times across 20 calls within the TTL; want exactly 1 "+
			"(each spawn is an npx invocation that hits the npm registry)", *calls)
	}
	// The cached value must be the real parsed block, not a zero value.
	if !last.IsActive || last.CostUSD != 48.21 {
		t.Errorf("cached block lost data: got IsActive=%v CostUSD=%v; want true/48.21",
			last.IsActive, last.CostUSD)
	}
}

// TestCurrentBlock_RefetchesAfterTTL — the cache must expire, or the
// burn-rate readout would freeze for the life of the process.
func TestCurrentBlock_RefetchesAfterTTL(t *testing.T) {
	calls := countingRunner(t, []byte(twoBlocksJSON))

	base := time.Now()
	nowFn = func() time.Time { return base }
	if _, err := CurrentBlock(context.Background()); err != nil {
		t.Fatalf("first CurrentBlock: %v", err)
	}
	// Just inside the TTL: still cached.
	nowFn = func() time.Time { return base.Add(cacheTTL - time.Second) }
	if _, err := CurrentBlock(context.Background()); err != nil {
		t.Fatalf("second CurrentBlock: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("spawned %d times before the TTL elapsed; want 1", *calls)
	}
	// Past the TTL: re-fetch.
	nowFn = func() time.Time { return base.Add(cacheTTL + time.Second) }
	if _, err := CurrentBlock(context.Background()); err != nil {
		t.Fatalf("third CurrentBlock: %v", err)
	}
	if *calls != 2 {
		t.Errorf("spawned %d times after the TTL elapsed; want 2 (cache must expire)", *calls)
	}
}

// TestCurrentBlock_FailureAlsoCached — the failure path is the one that
// actually needed fixing. "npx missing", "ccusage missing", and "no
// transcripts yet" all cost a full npx spawn to discover, and all were
// re-discovered every 15s. Caching successes alone left the storm
// running for exactly the users least likely to want it.
func TestCurrentBlock_FailureAlsoCached(t *testing.T) {
	resetCache(t)
	orig := runCmd
	t.Cleanup(func() { runCmd = orig })
	calls := 0
	runCmd = func(_ context.Context, _ ...string) ([]byte, error) {
		calls++
		return nil, errors.New("npx: command not found")
	}

	for i := 0; i < 10; i++ {
		if _, err := CurrentBlock(context.Background()); err == nil {
			t.Fatalf("call #%d: want error from failing ccusage", i)
		}
	}
	if calls != 1 {
		t.Errorf("failing ccusage spawned %d times across 10 calls; want 1 "+
			"(a machine without npx must not re-probe every tick)", calls)
	}
}

// TestCurrentBlock_EmptyBlocksCached — the sandbox case that exposed the
// gap: ccusage exits 0 with `{"blocks": []}` on a machine with no Claude
// transcripts, which CurrentBlock reports as an error. It must memoize
// like any other outcome.
func TestCurrentBlock_EmptyBlocksCached(t *testing.T) {
	calls := countingRunner(t, []byte(emptyBlocksJSON))

	for i := 0; i < 10; i++ {
		if _, err := CurrentBlock(context.Background()); err == nil {
			t.Fatalf("call #%d: want error for empty blocks", i)
		}
	}
	if *calls != 1 {
		t.Errorf("empty-blocks ccusage spawned %d times across 10 calls; want 1", *calls)
	}
}

// TestInvalidateCache_ForcesRefetch backs the explicit refresh key.
func TestInvalidateCache_ForcesRefetch(t *testing.T) {
	calls := countingRunner(t, []byte(twoBlocksJSON))

	if _, err := CurrentBlock(context.Background()); err != nil {
		t.Fatalf("first CurrentBlock: %v", err)
	}
	InvalidateCache()
	if _, err := CurrentBlock(context.Background()); err != nil {
		t.Fatalf("post-invalidate CurrentBlock: %v", err)
	}
	if calls := *calls; calls != 2 {
		t.Errorf("spawned %d times; want 2 (InvalidateCache must force a re-fetch)", calls)
	}
}

// TestCurrentBlock_ParsesActiveBlock — when one block is active, it
// should be returned even though it is not the first block in the array.
func TestCurrentBlock_ParsesActiveBlock(t *testing.T) {
	fakeRunner(t, []byte(twoBlocksJSON), nil)

	block, err := CurrentBlock(context.Background())
	if err != nil {
		t.Fatalf("CurrentBlock: %v", err)
	}
	if !block.IsActive {
		t.Error("expected IsActive=true")
	}
	if block.ID != "2026-05-19T22:00:00.000Z" {
		t.Errorf("ID = %q, want active block ID", block.ID)
	}
	if block.CostUSD != 48.21 {
		t.Errorf("CostUSD = %f, want 48.21", block.CostUSD)
	}
	if block.TotalTokens != 92363539 {
		t.Errorf("TotalTokens = %d, want 92363539", block.TotalTokens)
	}
	if block.BurnRateCostPerHour != 25.11 {
		t.Errorf("BurnRateCostPerHour = %f, want 25.11", block.BurnRateCostPerHour)
	}
	if block.ProjectedTotalCost != 125.22 {
		t.Errorf("ProjectedTotalCost = %f, want 125.22", block.ProjectedTotalCost)
	}
	if len(block.Models) != 2 {
		t.Errorf("len(Models) = %d, want 2", len(block.Models))
	}
	tc := block.TokenCounts
	if tc.InputTokens != 363 {
		t.Errorf("InputTokens = %d, want 363", tc.InputTokens)
	}
	if tc.OutputTokens != 84864 {
		t.Errorf("OutputTokens = %d, want 84864", tc.OutputTokens)
	}
	if tc.CacheCreateTokens != 221045 {
		t.Errorf("CacheCreateTokens = %d, want 221045", tc.CacheCreateTokens)
	}
	if tc.CacheReadTokens != 92057267 {
		t.Errorf("CacheReadTokens = %d, want 92057267", tc.CacheReadTokens)
	}
}

// TestCurrentBlock_FallsBackToLast — when no block is active, the last
// block in the array (highest ID) should be returned.
func TestCurrentBlock_FallsBackToLast(t *testing.T) {
	fakeRunner(t, []byte(noActiveJSON), nil)

	block, err := CurrentBlock(context.Background())
	if err != nil {
		t.Fatalf("CurrentBlock: %v", err)
	}
	if block.IsActive {
		t.Error("expected IsActive=false")
	}
	if block.ID != "2026-05-19T17:00:00.000Z" {
		t.Errorf("ID = %q, want last block ID", block.ID)
	}
	if block.CostUSD != 9.99 {
		t.Errorf("CostUSD = %f, want 9.99", block.CostUSD)
	}
}

// TestCurrentBlock_EmptyBlocks — an empty blocks array is a hard error,
// not a zero-value Block; callers must be able to distinguish "no data"
// from "block with zero cost."
func TestCurrentBlock_EmptyBlocks(t *testing.T) {
	fakeRunner(t, []byte(emptyBlocksJSON), nil)

	_, err := CurrentBlock(context.Background())
	if err == nil {
		t.Error("expected error for empty blocks array, got nil")
	}
}
