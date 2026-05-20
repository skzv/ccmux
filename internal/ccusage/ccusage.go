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

// CurrentBlock runs `npx ccusage blocks --json` and returns the most
// recently active block. When multiple blocks are present, an active
// one (isActive=true) takes priority; if none are active the last
// block by position (which corresponds to the highest ID) is returned.
// A 10-second timeout is applied to the subprocess.
func CurrentBlock(ctx context.Context) (Block, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	out, err := runCmd(ctx, "npx", "ccusage", "blocks", "--json")
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return Block{}, fmt.Errorf("npx not found; ccusage unavailable: %w", err)
		}
		return Block{}, fmt.Errorf("run ccusage: %w", err)
	}

	var resp jsonResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return Block{}, fmt.Errorf("parse ccusage output: %w", err)
	}
	if len(resp.Blocks) == 0 {
		return Block{}, fmt.Errorf("ccusage returned no blocks")
	}

	raw := resp.Blocks[len(resp.Blocks)-1]
	for _, b := range resp.Blocks {
		if b.IsActive {
			raw = b
			break
		}
	}

	return Block{
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
	}, nil
}
