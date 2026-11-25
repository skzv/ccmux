package ccusage

import (
	"context"
	"errors"
	"testing"
)

func TestCurrentBlock_DoesNotCacheCanceledAndClonesModels(t *testing.T) {
	resetCache(t)
	orig := runCmd
	t.Cleanup(func() { runCmd = orig })
	calls := 0
	runCmd = func(ctx context.Context, args ...string) ([]byte, error) {
		calls++
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return []byte(twoBlocksJSON), nil
	}

	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CurrentBlock(cctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled CurrentBlock = %v, want context.Canceled", err)
	}
	if calls != 0 {
		t.Fatalf("canceled CurrentBlock invoked runCmd %d times", calls)
	}

	b1, err := CurrentBlock(context.Background())
	if err != nil {
		t.Fatalf("CurrentBlock after canceled call failed: %v", err)
	}
	b1.Models[0] = "MUTATED"

	b2, err := CurrentBlock(context.Background())
	if err != nil {
		t.Fatalf("second CurrentBlock: %v", err)
	}
	if b2.Models[0] == "MUTATED" {
		t.Fatal("cached Models slice was mutated through returned Block")
	}
}
