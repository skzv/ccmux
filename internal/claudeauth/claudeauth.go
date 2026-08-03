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

	"github.com/skzv/ccmux/internal/config"
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
	cacheMu  sync.Mutex
	cached   *Status   // last successfully-fetched status (never an error result)
	cachedAt time.Time // when `cached` was fetched
	cacheTTL = 5 * time.Minute
	// cachedErr + lastAttempt implement a short negative cache: a fetch
	// failure (cold `claude` boot blowing the 3s timeout, CLI briefly
	// missing mid-upgrade) is transient, so it must not poison the full
	// 5-minute TTL — it only suppresses retries for negativeTTL.
	cachedErr   error
	lastAttempt time.Time
	negativeTTL = 10 * time.Second
	// fetchFn is a seam so tests can fake the `claude auth status`
	// subprocess.
	fetchFn = fetch
)

// Get returns the current Claude auth status, caching a successful
// result for 5 minutes. Safe to call from any goroutine.
//
// Failure handling: a fetch error never clobbers a previously good
// Status — the stale value keeps being served (with a nil error) so
// the dashboard's quota bar doesn't collapse to "api" over one slow
// CLI boot. The failure is negatively cached for only negativeTTL, so
// the next Get after that retries. An error is returned only when
// there has never been a good result to serve.
func Get(ctx context.Context) (Status, error) {
	cacheMu.Lock()
	if cached != nil && time.Since(cachedAt) < cacheTTL {
		s := *cached
		cacheMu.Unlock()
		return s, nil
	}
	if cachedErr != nil && time.Since(lastAttempt) < negativeTTL {
		// Recent failure — don't hammer the CLI. Serve stale if we can.
		if cached != nil {
			s := *cached
			cacheMu.Unlock()
			return s, nil
		}
		err := cachedErr
		cacheMu.Unlock()
		return Status{}, err
	}
	cacheMu.Unlock()

	s, err := fetchFn(ctx)
	cacheMu.Lock()
	defer cacheMu.Unlock()
	lastAttempt = time.Now()
	cachedErr = err
	if err == nil {
		cached = &s
		cachedAt = lastAttempt
		return s, nil
	}
	if cached != nil {
		return *cached, nil // serve-stale
	}
	return Status{}, err
}

// fetch shells out to `claude auth status` and parses its JSON output.
func fetch(ctx context.Context) (Status, error) {
	cfg, _ := config.Load()
	bin := strings.TrimSpace(cfg.Agents.Claude.Command)
	if bin == "" {
		var err error
		bin, err = exec.LookPath("claude")
		if err != nil {
			return Status{}, errors.New("claude not on PATH")
		}
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
