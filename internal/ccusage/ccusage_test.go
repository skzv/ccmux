package ccusage

import (
	"context"
	"testing"
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
	orig := runCmd
	t.Cleanup(func() { runCmd = orig })
	runCmd = func(_ context.Context, _ ...string) ([]byte, error) {
		return payload, cmdErr
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
