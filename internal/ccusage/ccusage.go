// Package ccusage shells out to `npx ccusage blocks --json` and
// surfaces the current billing block — cost, token counts, burn rate,
// and projection — for dashboard display. If ccusage is not installed
// or the command fails, all callers treat the error as "no data
// available" rather than a hard failure.
package ccusage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"
)

// TokenCounts holds the four token categories reported by ccusage.
type TokenCounts struct {
	InputTokens       int64
	OutputTokens      int64
	CacheCreateTokens int64
	CacheReadTokens   int64
}

// Block is a single five-hour billing window returned by ccusage.
type Block struct {
	ID                  string
	StartTime           time.Time
	EndTime             time.Time
	IsActive            bool
	CostUSD             float64
	TotalTokens         int64
	BurnRateCostPerHour float64
	ProjectedTotalCost  float64
	Models              []string
	TokenCounts         TokenCounts
}

// runCmd is the injectable executor used by CurrentBlock. Tests replace
// it with a fake; production code calls realRunCmd.
var runCmd = realRunCmd

// cacheTTL bounds how often the npx subprocess actually runs. The
// dashboard refreshes usage every 15s, but a ccusage "block" is a
// FIVE-HOUR billing window whose cost/burn-rate move slowly — polling
// it four times a minute bought nothing and cost a great deal:
//
//   - `npx ccusage` contacts registry.npmjs.org on every invocation,
//     and installs the package when it isn't already in the npx cache.
//     A ccmux left open overnight made thousands of registry requests
//     and filled ~/.npm/_logs with debug logs (observed: one npm log
//     every 15s for the lifetime of the TUI).
//   - Each run spawns node (~0.3-0.7s CPU). On battery, for a tool
//     that also holds `caffeinate`, that adds up.
//
// 2 minutes keeps the burn-rate readout usefully fresh (it's a
// 5h-window aggregate) while cutting subprocess + network churn by 8x.
const cacheTTL = 2 * time.Minute

// Failures are cached for the same TTL as successes, and that is
// deliberate: the failure path IS the expensive one. The common cases —
// npx not installed, ccusage not installed, or a machine with no Claude
// transcripts yet (ccusage exits 0 with `{"blocks": []}`) — all cost a
// full npx spawn + registry round-trip to discover. Re-detecting them
// every 15s was the storm. None of those states change on a timescale
// that a 2-minute delay matters for.
var (
	cacheMu     sync.Mutex
	cachedBlock *Block
	cachedErr   error
	cachedAt    time.Time
	cacheValid  bool
	// nowFn is a seam so tests can advance the clock past cacheTTL
	// without sleeping.
	nowFn = time.Now
)

// InvalidateCache drops the memoized block so the next CurrentBlock
// re-runs ccusage. Exposed for the TUI's explicit refresh key ("r"),
// where the user is asking for fresh numbers on purpose.
func InvalidateCache() {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cachedBlock = nil
	cachedErr = nil
	cachedAt = time.Time{}
	cacheValid = false
}

// cachedResult returns the memoized outcome when it is still fresh.
func cachedResult() (Block, error, bool) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if !cacheValid || nowFn().Sub(cachedAt) >= cacheTTL {
		return Block{}, nil, false
	}
	if cachedErr != nil {
		return Block{}, cachedErr, true
	}
	return *cachedBlock, nil, true
}

// memoize stores an outcome (success or failure) as the current result.
func memoize(b Block, err error) (Block, error) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cachedAt = nowFn()
	cacheValid = true
	if err != nil {
		cachedBlock, cachedErr = nil, err
		return Block{}, err
	}
	blk := b
	cachedBlock, cachedErr = &blk, nil
	return b, nil
}

func realRunCmd(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("ccusage exited %d: %s", exitErr.ExitCode(), string(exitErr.Stderr))
		}
		return nil, err
	}
	return out, nil
}

// wire types that mirror the JSON structure from `npx ccusage blocks --json`.
type jsonTokenCounts struct {
	InputTokens              int64 `json:"inputTokens"`
	OutputTokens             int64 `json:"outputTokens"`
	CacheCreationInputTokens int64 `json:"cacheCreationInputTokens"`
	CacheReadInputTokens     int64 `json:"cacheReadInputTokens"`
}

type jsonBurnRate struct {
	CostPerHour float64 `json:"costPerHour"`
}

type jsonProjection struct {
	TotalCost float64 `json:"totalCost"`
}

type jsonBlock struct {
	ID          string          `json:"id"`
	StartTime   time.Time       `json:"startTime"`
	EndTime     time.Time       `json:"endTime"`
	IsActive    bool            `json:"isActive"`
	CostUSD     float64         `json:"costUSD"`
	TotalTokens int64           `json:"totalTokens"`
	Models      []string        `json:"models"`
	TokenCounts jsonTokenCounts `json:"tokenCounts"`
	BurnRate    jsonBurnRate    `json:"burnRate"`
	Projection  jsonProjection  `json:"projection"`
}

type jsonResponse struct {
	Blocks []jsonBlock `json:"blocks"`
}

// CurrentBlock returns the most recently active billing block. When
// multiple blocks are present, an active one (isActive=true) takes
// priority; if none are active the last block by position (which
// corresponds to the highest ID) is returned.
//
// The underlying `npx ccusage blocks --json` subprocess runs at most
// once per cacheTTL — see that constant for why. Callers may still
// call this as often as they like; within the TTL they get the
// memoized block. Use InvalidateCache for a user-requested refresh.
// A 10-second timeout is applied to the subprocess.
func CurrentBlock(ctx context.Context) (Block, error) {
	if b, err, ok := cachedResult(); ok {
		return b, err
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	out, err := runCmd(ctx, "npx", "ccusage", "blocks", "--json")
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return memoize(Block{}, fmt.Errorf("npx not found; ccusage unavailable: %w", err))
		}
		return memoize(Block{}, fmt.Errorf("run ccusage: %w", err))
	}

	var resp jsonResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return memoize(Block{}, fmt.Errorf("parse ccusage output: %w", err))
	}
	if len(resp.Blocks) == 0 {
		// Machine with no Claude transcripts yet — ccusage exits 0 with
		// an empty list. Memoized like any other outcome so a fresh
		// install doesn't spawn npx every 15s forever.
		return memoize(Block{}, fmt.Errorf("ccusage returned no blocks"))
	}

	raw := resp.Blocks[len(resp.Blocks)-1]
	for _, b := range resp.Blocks {
		if b.IsActive {
			raw = b
			break
		}
	}

	block := Block{
		ID:                  raw.ID,
		StartTime:           raw.StartTime,
		EndTime:             raw.EndTime,
		IsActive:            raw.IsActive,
		CostUSD:             raw.CostUSD,
		TotalTokens:         raw.TotalTokens,
		BurnRateCostPerHour: raw.BurnRate.CostPerHour,
		ProjectedTotalCost:  raw.Projection.TotalCost,
		Models:              raw.Models,
		TokenCounts: TokenCounts{
			InputTokens:       raw.TokenCounts.InputTokens,
			OutputTokens:      raw.TokenCounts.OutputTokens,
			CacheCreateTokens: raw.TokenCounts.CacheCreationInputTokens,
			CacheReadTokens:   raw.TokenCounts.CacheReadInputTokens,
		},
	}

	return memoize(block, nil)
}
