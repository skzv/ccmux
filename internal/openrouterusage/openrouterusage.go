// Package openrouterusage queries OpenRouter's account and pricing
// APIs so ccmux can surface "how much have I spent on OpenRouter" in
// the same usage panel that shows per-agent token counts, and compute
// native per-model dollar cost for any agent routed through OpenRouter.
//
// Two endpoints, both on the standard OpenAI-compatible base
// (https://openrouter.ai/api/v1), authed with the same bearer key the
// agents use:
//
//   - GET /key    → the key's lifetime usage + credit limit (the spend
//     headline). Works with a normal inference key — no separate
//     "management" key needed (that's only for /credits).
//   - GET /models → every model's per-token prompt/completion price,
//     which powers native cost computation.
//
// No state, no background polling here — the daemon calls Spend() on the
// same cadence it walks the local transcript trees, and the result
// rides the existing /v1/usage response.
package openrouterusage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is OpenRouter's OpenAI-compatible API root. Overridable
// via config for self-hosted gateways or tests pointing at httptest.
const DefaultBaseURL = "https://openrouter.ai/api/v1"

// maxBody caps a response read so a misbehaving endpoint can't drive the
// daemon to OOM. The /models list is the big one (~hundreds of models);
// 8 MiB is comfortably above it.
const maxBody = 8 << 20

// Client talks to one OpenRouter account. Construct with New; the zero
// value is not usable (no key).
type Client struct {
	baseURL string
	apiKey  string
	hc      *http.Client
}

// New returns a Client for the given key. An empty baseURL falls back to
// DefaultBaseURL. The HTTP client carries a conservative timeout so a
// hung OpenRouter call can't stall the daemon's usage refresh.
func New(apiKey, baseURL string) *Client {
	base := strings.TrimRight(baseURL, "/")
	if base == "" {
		base = DefaultBaseURL
	}
	return &Client{
		baseURL: base,
		apiKey:  apiKey,
		hc:      &http.Client{Timeout: 10 * time.Second},
	}
}

// KeyInfo is the spend headline from GET /key. Usage and Limit are USD;
// Limit is nil when the key has no cap (pay-as-you-go / unlimited).
type KeyInfo struct {
	Label      string   `json:"label"`
	Usage      float64  `json:"usage"`        // total USD spent on this key
	Limit      *float64 `json:"limit"`        // credit cap, nil = unlimited
	IsFreeTier bool     `json:"is_free_tier"` // free-tier key
	RateLimit  *struct {
		Requests int    `json:"requests"`
		Interval string `json:"interval"`
	} `json:"rate_limit,omitempty"`
}

// Remaining returns the spend headroom (Limit-Usage), or -1 when the key
// is uncapped. Negative usage (shouldn't happen) clamps the result at 0.
func (k KeyInfo) Remaining() float64 {
	if k.Limit == nil {
		return -1
	}
	r := *k.Limit - k.Usage
	if r < 0 {
		return 0
	}
	return r
}

// Spend is the flat, render-ready view of an account's spend that both
// the daemon (/v1/usage) and the TUI dashboard consume. Limit is 0 when
// the key is uncapped; Remaining is -1 in that case (the "uncapped"
// sentinel) so a caller can show "$X spent" without a misleading cap.
type Spend struct {
	Usage      float64
	Limit      float64
	Remaining  float64
	IsFreeTier bool
}

// Spend fetches GET /key and flattens it into a Spend. One round trip;
// callers add their own timeout via ctx.
func (c *Client) Spend(ctx context.Context) (Spend, error) {
	k, err := c.Key(ctx)
	if err != nil {
		return Spend{}, err
	}
	limit := 0.0
	if k.Limit != nil {
		limit = *k.Limit
	}
	return Spend{
		Usage:      k.Usage,
		Limit:      limit,
		Remaining:  k.Remaining(),
		IsFreeTier: k.IsFreeTier,
	}, nil
}

// Key fetches GET /key — the current key's usage + limit. The response
// is wrapped in a top-level {"data": {...}} envelope, OpenRouter's
// convention for every account endpoint.
func (c *Client) Key(ctx context.Context) (KeyInfo, error) {
	var env struct {
		Data KeyInfo `json:"data"`
	}
	if err := c.getJSON(ctx, "/key", &env); err != nil {
		return KeyInfo{}, err
	}
	return env.Data, nil
}

// ModelPrice is one model's per-token pricing, parsed from GET /models.
// OpenRouter quotes prices as decimal strings in USD per token (e.g.
// "0.000008"); we parse them to float64 once so callers do plain
// arithmetic.
type ModelPrice struct {
	ID        string
	PromptUSD float64 // USD per prompt (input) token
	OutputUSD float64 // USD per completion (output) token
}

// CostOf returns the USD cost of a request with the given input/output
// token counts at this model's prices.
func (p ModelPrice) CostOf(inputTokens, outputTokens int) float64 {
	return float64(inputTokens)*p.PromptUSD + float64(outputTokens)*p.OutputUSD
}

// Pricing fetches GET /models and returns a map keyed by model ID. The
// raw `pricing.prompt` / `pricing.completion` fields are decimal
// strings; unparseable or absent prices yield 0 (treated as free) rather
// than failing the whole call — one weird model shouldn't blank the
// pricing table.
func (c *Client) Pricing(ctx context.Context) (map[string]ModelPrice, error) {
	var env struct {
		Data []struct {
			ID      string `json:"id"`
			Pricing struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := c.getJSON(ctx, "/models", &env); err != nil {
		return nil, err
	}
	out := make(map[string]ModelPrice, len(env.Data))
	for _, m := range env.Data {
		out[m.ID] = ModelPrice{
			ID:        m.ID,
			PromptUSD: parsePrice(m.Pricing.Prompt),
			OutputUSD: parsePrice(m.Pricing.Completion),
		}
	}
	return out, nil
}

// parsePrice converts OpenRouter's decimal-string price to a float.
// Empty or malformed → 0 (free), so a single odd entry can't break the
// table.
func parsePrice(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 {
		return 0
	}
	return v
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	// OpenRouter recommends these attribution headers; harmless and
	// keeps ccmux's calls identifiable in the user's OpenRouter activity.
	req.Header.Set("HTTP-Referer", "https://ccmux.ai")
	req.Header.Set("X-Title", "ccmux")
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("openrouter GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("openrouter GET %s: unauthorized (check api_key)", path)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("openrouter GET %s: status %d", path, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(out)
}
