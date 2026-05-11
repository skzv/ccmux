// Package claudeauth introspects Claude Code's auth/subscription state
// by parsing `claude auth status` JSON. Cached so the dashboard doesn't
// invoke the CLI every tick.
//
// We deliberately don't read ~/.claude/credentials.* or any other
// auth-secret file. `claude auth status` produces a stable JSON shape
// with the public, non-sensitive identity fields (email, org, plan)
// and is the supported way to interrogate the user's plan.
package claudeauth

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Status is the parsed shape of `claude auth status`.
type Status struct {
	LoggedIn         bool   `json:"loggedIn"`
	AuthMethod       string `json:"authMethod"`  // "claude.ai" | "api-key" | …
	APIProvider      string `json:"apiProvider"` // "firstParty" | …
	Email            string `json:"email"`
	OrgID            string `json:"orgId"`
	OrgName          string `json:"orgName"`
	SubscriptionType string `json:"subscriptionType"` // "pro" | "max" | "max-5x" | "max-20x" | ""
}

// Tier returns the normalized ccmux tier name corresponding to the
// auth-status SubscriptionType, suitable for SubscriptionConfig.Tier:
//   - "pro"     → "pro"
//   - "max"     → "max5x" (Anthropic's plain "max" matches the 5x tier)
//   - "max5x"   → "max5x"
//   - "max20x"  → "max20x"
//   - anything else (api key, unknown) → "api"
func (s Status) Tier() string {
	t := strings.ToLower(strings.TrimSpace(s.SubscriptionType))
	t = strings.ReplaceAll(t, "-", "")
	t = strings.ReplaceAll(t, "_", "")
	switch t {
	case "pro":
		return "pro"
	case "max", "max5x":
		return "max5x"
	case "max20x":
		return "max20x"
	}
	return "api"
}

var (
	cacheMu     sync.Mutex
	cached      *Status
	cachedAt    time.Time
	cacheTTL    = 5 * time.Minute
	cachedErr   error
)

// Get returns the current Claude auth status, caching the result for 5
// minutes. Safe to call from any goroutine. Returns an error only when
// `claude` is not on PATH or its JSON output is malformed — the caller
// can treat a missing tier as "api" via Status.Tier().
func Get(ctx context.Context) (Status, error) {
	cacheMu.Lock()
	if cached != nil && time.Since(cachedAt) < cacheTTL {
		s := *cached
		err := cachedErr
		cacheMu.Unlock()
		return s, err
	}
	cacheMu.Unlock()

	s, err := fetch(ctx)
	cacheMu.Lock()
	cached = &s
	cachedAt = time.Now()
	cachedErr = err
	cacheMu.Unlock()
	return s, err
}

// fetch shells out to `claude auth status` and parses its JSON output.
func fetch(ctx context.Context) (Status, error) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return Status{}, errors.New("claude not on PATH")
	}
	c, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(c, bin, "auth", "status")
	out, err := cmd.Output()
	if err != nil {
		return Status{}, err
	}
	var s Status
	if err := json.Unmarshal(out, &s); err != nil {
		return Status{}, err
	}
	return s, nil
}
