// Package claudemodels discovers the Claude model catalog at runtime.
//
// ccmux historically showed a hand-written list of three aliases
// (opus/sonnet/haiku) in the model picker. The list went stale every
// time Anthropic shipped a new model — users had to wait for a ccmux
// release just to see the new family in the picker.
//
// This package replaces that with live discovery: hit Anthropic's
// Models API when an API key is present, cache the result for 24h on
// disk, and fall back to a curated in-binary list otherwise. The
// daemon refreshes the cache every 24h in the background; callers
// read the merged result via Service.Catalog.
//
// Auth model: ANTHROPIC_API_KEY only. The vast majority of ccmux
// users authenticate to Claude Code via `claude auth login` and
// don't have an API key set — that's fine, they get the curated
// fallback list (which ships updated with every ccmux release).
// Users who want live discovery set ANTHROPIC_API_KEY in their
// shell. There is no OAuth path: internal/claudeauth deliberately
// doesn't expose tokens, and rebuilding that boundary just for
// this feature wasn't worth the surface area.
package claudemodels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Source tells the caller whether a given model row came from a live
// API fetch or the in-binary fallback list. Surfaced in the TUI so
// users can tell at a glance which surface is informing the picker.
type Source string

const (
	// SourceClaudeCLI — discovered by shelling out to the user's
	// `claude` CLI. Works for subscription users (no API key
	// required) since claude handles its own auth.
	SourceClaudeCLI Source = "claude-cli"
	// SourceAPI — direct call to GET /v1/models with ANTHROPIC_API_KEY.
	// Only available to users with an API key set on the daemon's env.
	SourceAPI Source = "api"
	// SourceFallback — the curated in-binary list that ships with
	// every ccmux release. Always available; the floor of the
	// discovery chain.
	SourceFallback Source = "fallback"
)

// Model is one row in the picker. Fields beyond ID are best-effort —
// the Models API populates them when available, the fallback list
// fills them for the curated set. Capabilities is a flattened bool
// map (vision, thinking_adaptive, structured_outputs, effort_max)
// rather than the full nested API tree so consumers don't have to
// walk a dict-of-dicts to render badges.
type Model struct {
	ID           string          `json:"id"`
	DisplayName  string          `json:"display_name,omitempty"`
	Family       string          `json:"family,omitempty"` // opus|sonnet|haiku, derived from ID
	MaxInput     int             `json:"max_input_tokens,omitempty"`
	MaxOutput    int             `json:"max_tokens,omitempty"`
	Capabilities map[string]bool `json:"capabilities,omitempty"`
	Source       Source          `json:"source"`
}

// Catalog is what the daemon hands out — the merged, cached snapshot
// with a timestamp callers can render ("fetched 4h ago").
type Catalog struct {
	Models    []Model   `json:"models"`
	FetchedAt time.Time `json:"fetched_at"`
	Source    Source    `json:"source"` // api if any model came from the API, else fallback
}

// DefaultBaseURL is the production Anthropic API host. Override on
// Fetcher for tests.
const DefaultBaseURL = "https://api.anthropic.com"

// anthropicVersion is the version header the Models API requires.
// Pin a known-good value; bump deliberately when the schema actually
// changes (the response shape rarely moves).
const anthropicVersion = "2023-06-01"

// ErrNoAPIKey signals that live discovery via the Anthropic Models
// API isn't available because no credential was supplied. Callers
// fall back to the CLI or curated list and continue silently.
var ErrNoAPIKey = errors.New("claudemodels: no ANTHROPIC_API_KEY set")

// ErrClaudeCLIUnavailable signals that the `claude` CLI either isn't
// installed, the user isn't logged in, or the call otherwise failed
// in a way that means the next source in the discovery chain should
// be tried. Distinct from generic errors so Service.Refresh can fall
// through cleanly without logging a scary error for the common
// "claude not on PATH" case.
var ErrClaudeCLIUnavailable = errors.New("claudemodels: claude CLI unavailable")

// Fetcher hits Anthropic's GET /v1/models. Swap HTTPDo in tests to
// drive responses without a network round-trip. BaseURL is exposed
// for the same reason (and so we can point at a staging host one day
// without code edits).
type Fetcher struct {
	APIKey  string
	BaseURL string
	HTTPDo  func(*http.Request) (*http.Response, error)
}

// Fetch lists every model the configured API key can see. Walks
// pagination via the cursor the Models API returns; in practice
// there's only one page today but the loop is cheap and futureproof.
func (f Fetcher) Fetch(ctx context.Context) ([]Model, error) {
	if strings.TrimSpace(f.APIKey) == "" {
		return nil, ErrNoAPIKey
	}
	base := f.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	do := f.HTTPDo
	if do == nil {
		do = http.DefaultClient.Do
	}

	var out []Model
	url := base + "/v1/models?limit=100"
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("build models request: %w", err)
		}
		req.Header.Set("x-api-key", f.APIKey)
		req.Header.Set("anthropic-version", anthropicVersion)

		resp, err := do(req)
		if err != nil {
			return nil, fmt.Errorf("call models api: %w", err)
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read models response: %w", readErr)
		}
		if resp.StatusCode/100 != 2 {
			// Surface the API's own error envelope when possible — it
			// usually carries a more actionable message than the bare
			// status line (e.g. "invalid x-api-key").
			return nil, fmt.Errorf("models api %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}

		var page modelsPage
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("decode models response: %w", err)
		}
		for _, raw := range page.Data {
			out = append(out, raw.toModel())
		}
		if !page.HasMore || page.LastID == "" {
			break
		}
		url = base + "/v1/models?limit=100&after_id=" + page.LastID
	}
	return out, nil
}

// modelsPage mirrors the Models API JSON shape. Kept close to the
// wire so a misnamed field is easy to spot; toModel() flattens to
// the package-facing struct.
type modelsPage struct {
	Data    []modelRow `json:"data"`
	HasMore bool       `json:"has_more"`
	LastID  string     `json:"last_id"`
}

type modelRow struct {
	ID             string                 `json:"id"`
	DisplayName    string                 `json:"display_name"`
	MaxInputTokens int                    `json:"max_input_tokens"`
	MaxTokens      int                    `json:"max_tokens"`
	Capabilities   map[string]interface{} `json:"capabilities"`
}

func (r modelRow) toModel() Model {
	return Model{
		ID:           r.ID,
		DisplayName:  r.DisplayName,
		Family:       familyOf(r.ID),
		MaxInput:     r.MaxInputTokens,
		MaxOutput:    r.MaxTokens,
		Capabilities: flattenCapabilities(r.Capabilities),
		Source:       SourceAPI,
	}
}

// familyOf returns "opus"/"sonnet"/"haiku" for a Claude model ID, or
// "" for anything we don't recognise. Used purely for grouping in
// the picker; never load-bearing.
func familyOf(id string) string {
	switch {
	case strings.Contains(id, "opus"):
		return "opus"
	case strings.Contains(id, "sonnet"):
		return "sonnet"
	case strings.Contains(id, "haiku"):
		return "haiku"
	}
	return ""
}

// flattenCapabilities walks the nested capability tree from the API
// and emits the handful of booleans the picker actually renders. The
// tree's full shape is verbose ("capabilities.thinking.types.adaptive.supported"),
// so we surface just the leaves that drive UX decisions.
//
// Missing keys → false rather than absent so consumers can treat the
// map as a flat predicate table.
func flattenCapabilities(raw map[string]interface{}) map[string]bool {
	if raw == nil {
		return nil
	}
	caps := map[string]bool{
		"vision":             supported(raw, "image_input"),
		"structured_outputs": supported(raw, "structured_outputs"),
		"thinking_adaptive":  nestedSupported(raw, "thinking", "types", "adaptive"),
		"effort_max":         nestedSupported(raw, "effort", "max"),
	}
	return caps
}

// supported reports whether raw[key].supported is true. Returns false
// for any missing/wrong-type path — we'd rather show no badge than
// crash on a schema drift.
func supported(raw map[string]interface{}, key string) bool {
	node, ok := raw[key].(map[string]interface{})
	if !ok {
		return false
	}
	b, _ := node["supported"].(bool)
	return b
}

// nestedSupported walks a chain of nested map keys to a final
// {supported: bool} leaf. Same defensive shape as supported().
func nestedSupported(raw map[string]interface{}, keys ...string) bool {
	cur := raw
	for i, k := range keys {
		next, ok := cur[k].(map[string]interface{})
		if !ok {
			return false
		}
		if i == len(keys)-1 {
			b, _ := next["supported"].(bool)
			return b
		}
		cur = next
	}
	return false
}

// Fallback returns the curated, in-binary model list. This is what
// every ccmux release ships with, and what users without an API key
// see. Update with each Anthropic launch — one PR per release. Order
// here doesn't matter; Sort() in the Service normalises it.
//
// Keep the set small and current: the goal is "what you'd want to
// pick today", not an archive. Retired models don't belong here.
func Fallback() []Model {
	return []Model{
		{
			ID:          "claude-opus-4-8",
			DisplayName: "Claude Opus 4.8",
			Family:      "opus",
			MaxInput:    1_000_000,
			MaxOutput:   128_000,
			Capabilities: map[string]bool{
				"vision": true, "thinking_adaptive": true,
				"structured_outputs": true, "effort_max": true,
			},
			Source: SourceFallback,
		},
		{
			ID:          "claude-opus-4-7",
			DisplayName: "Claude Opus 4.7",
			Family:      "opus",
			MaxInput:    1_000_000,
			MaxOutput:   128_000,
			Capabilities: map[string]bool{
				"vision": true, "thinking_adaptive": true,
				"structured_outputs": true, "effort_max": true,
			},
			Source: SourceFallback,
		},
		{
			ID:          "claude-opus-4-6",
			DisplayName: "Claude Opus 4.6",
			Family:      "opus",
			MaxInput:    1_000_000,
			MaxOutput:   128_000,
			Capabilities: map[string]bool{
				"vision": true, "thinking_adaptive": true,
				"structured_outputs": true, "effort_max": true,
			},
			Source: SourceFallback,
		},
		{
			ID:          "claude-sonnet-4-6",
			DisplayName: "Claude Sonnet 4.6",
			Family:      "sonnet",
			MaxInput:    1_000_000,
			MaxOutput:   64_000,
			Capabilities: map[string]bool{
				"vision": true, "thinking_adaptive": true,
				"structured_outputs": true,
			},
			Source: SourceFallback,
		},
		{
			ID:          "claude-haiku-4-5",
			DisplayName: "Claude Haiku 4.5",
			Family:      "haiku",
			MaxInput:    200_000,
			MaxOutput:   64_000,
			Capabilities: map[string]bool{
				"vision": true, "structured_outputs": true,
			},
			Source: SourceFallback,
		},
	}
}

// Sort orders models for display: family group order opus → sonnet
// → haiku → other, then within each group by ID descending (so the
// newest entry — biggest version number — surfaces first). Stable
// against equal IDs.
func Sort(models []Model) {
	familyRank := map[string]int{"opus": 0, "sonnet": 1, "haiku": 2}
	sort.SliceStable(models, func(i, j int) bool {
		ri, oki := familyRank[models[i].Family]
		rj, okj := familyRank[models[j].Family]
		if !oki {
			ri = 99
		}
		if !okj {
			rj = 99
		}
		if ri != rj {
			return ri < rj
		}
		// Within a family, newest ID first. Lexicographic descending
		// on the ID puts "opus-4-8" ahead of "opus-4-7", which is
		// what we want for every Anthropic ID format shipped so far.
		return models[i].ID > models[j].ID
	})
}

// Merge layers `live` over `fallback`: anything that came from the
// API wins (it's the authoritative source for capability/window),
// and fallback entries fill in gaps for models the API didn't return.
//
// Why merge instead of replacing: if the API call succeeded but the
// account doesn't have access to (say) Opus 4.8 yet, we'd rather
// still show it in the picker (sourced from fallback) than hide it
// entirely. Worst case the user picks a model they can't use and
// gets a clear error from Claude Code at launch time.
func Merge(live, fallback []Model) []Model {
	seen := make(map[string]bool, len(live))
	out := make([]Model, 0, len(live)+len(fallback))
	for _, m := range live {
		seen[m.ID] = true
		out = append(out, m)
	}
	for _, m := range fallback {
		if seen[m.ID] {
			continue
		}
		out = append(out, m)
	}
	return out
}

// Cache reads and writes the on-disk Catalog snapshot. JSON file at
// a caller-provided path (typically ~/.local/state/ccmux/models.json).
// File mode is the conventional 0644 — nothing sensitive in here.
type Cache struct {
	Path string
}

// Read returns the cached catalog. A missing file is not an error —
// it returns an empty Catalog so first-boot callers can no-op until
// the daemon refreshes. Other I/O errors propagate.
func (c Cache) Read() (Catalog, error) {
	data, err := os.ReadFile(c.Path)
	if errors.Is(err, os.ErrNotExist) {
		return Catalog{}, nil
	}
	if err != nil {
		return Catalog{}, fmt.Errorf("read model cache %s: %w", c.Path, err)
	}
	var cat Catalog
	if err := json.Unmarshal(data, &cat); err != nil {
		return Catalog{}, fmt.Errorf("parse model cache %s: %w", c.Path, err)
	}
	return cat, nil
}

// Write atomically replaces the cache file (write-then-rename) so a
// crashed daemon never leaves a half-truncated JSON blob behind.
// Creates the parent directory if needed.
func (c Cache) Write(cat Catalog) error {
	if c.Path == "" {
		return errors.New("claudemodels: empty cache path")
	}
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o755); err != nil {
		return fmt.Errorf("mkdir cache parent: %w", err)
	}
	data, err := json.MarshalIndent(cat, "", "  ")
	if err != nil {
		return fmt.Errorf("encode model cache: %w", err)
	}
	tmp := c.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write tmp cache: %w", err)
	}
	if err := os.Rename(tmp, c.Path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename cache: %w", err)
	}
	return nil
}

// Service is the daemon's facade over the package. It owns a Cache
// and a Fetcher and arbitrates between them — callers don't need to
// know which surface answered.
type Service struct {
	cache Cache
	// Fetcher is the Anthropic Models API path. Used as the second
	// step in the discovery chain — only relevant when the user has
	// an API key set. Exposed so tests can swap BaseURL/HTTPDo.
	Fetcher Fetcher
	// CLIFetcher is the `claude -p` path, tried first in the chain
	// because it works for everyone (subscription + API). Set to a
	// zero-value struct to use the default `claude` on PATH; set
	// Binary or Run for tests / non-standard installs.
	CLIFetcher ClaudeCLIFetcher
	// MaxAge is how stale the cache can be before Catalog auto-refreshes.
	// Defaults to 7 days because the LLM-backed CLI fetch is the
	// primary source and a daily refresh would cost ~$0.70/month
	// per user — the model catalog only changes a handful of times
	// a year, so weekly is plenty.
	MaxAge time.Duration
}

// New wires a Service to the daemon's cache path and an API key
// (typically os.Getenv("ANTHROPIC_API_KEY")). An empty key is fine —
// Refresh will return ErrNoAPIKey and Catalog falls back to the
// curated list.
func New(cachePath, apiKey string) *Service {
	return &Service{
		cache:      Cache{Path: cachePath},
		Fetcher:    Fetcher{APIKey: apiKey},
		CLIFetcher: ClaudeCLIFetcher{}, // resolves `claude` on PATH at exec time
		MaxAge:     7 * 24 * time.Hour,
	}
}

// Catalog returns the current snapshot. Reads the cache first; if
// the cache is missing or older than MaxAge, refreshes synchronously
// before returning. A network failure during the refresh is logged
// (by the caller — Service doesn't import a logger) and falls back to
// whatever's in the cache, or the curated list if nothing's cached.
func (s *Service) Catalog(ctx context.Context) (Catalog, error) {
	cached, err := s.cache.Read()
	if err != nil {
		// Don't bail — a corrupt cache shouldn't kill the picker.
		// Press on as if there was no cache.
		cached = Catalog{}
	}
	if !cached.FetchedAt.IsZero() && time.Since(cached.FetchedAt) < s.MaxAge {
		return s.withFallback(cached), nil
	}
	fresh, refreshErr := s.Refresh(ctx)
	if refreshErr != nil {
		// On ErrNoAPIKey, Refresh still produces and persists a valid
		// fallback-source Catalog (with a real FetchedAt) — return
		// that so subsequent reads through the cache match this
		// response byte-for-byte. Synthesising a zero-time Catalog
		// here used to break /v1/models parity (the second call read
		// the freshly-written cache and saw a different timestamp).
		if !fresh.FetchedAt.IsZero() {
			return s.withFallback(fresh), refreshErr
		}
		// Real network failure with no usable cache — return the
		// curated list under a zero timestamp; callers can detect
		// "never fetched" via FetchedAt.IsZero().
		if !cached.FetchedAt.IsZero() {
			return s.withFallback(cached), refreshErr
		}
		return Catalog{
			Models:    Fallback(),
			FetchedAt: time.Time{},
			Source:    SourceFallback,
		}, refreshErr
	}
	return s.withFallback(fresh), nil
}

// Refresh walks the discovery chain in order — claude CLI, then the
// Anthropic Models API, then the curated in-binary list — and writes
// whichever wins to the cache. Returns the source-tagged catalog so
// the caller can render the badge ("api" vs "claude-cli" vs
// "fallback") and decide whether to log a degraded-mode warning.
//
// Why this order:
//  1. Claude CLI works for every authenticated user — both
//     subscription (OAuth via `claude auth login`) and API (env
//     var). No per-user setup on ccmux's side.
//  2. Anthropic Models API is a clean structured query, but only
//     when the daemon's environment has ANTHROPIC_API_KEY.
//  3. Curated fallback ships with every ccmux release. Never empty.
//
// Each fetcher returns its own sentinel error (ErrClaudeCLIUnavailable,
// ErrNoAPIKey) when the user can't use that source, so the chain
// falls through silently for the common cases. Non-sentinel errors
// (network blip, parse failure) also fall through, but get returned
// up the stack so the caller can log them.
func (s *Service) Refresh(ctx context.Context) (Catalog, error) {
	// 1. CLI — best for subscription users.
	if models, err := s.CLIFetcher.Fetch(ctx); err == nil && len(models) > 0 {
		return s.writeAndReturn(Catalog{
			Models:    models,
			FetchedAt: time.Now().UTC(),
			Source:    SourceClaudeCLI,
		})
	} else if err != nil && !errors.Is(err, ErrClaudeCLIUnavailable) {
		// Not the silent-fallthrough sentinel — keep walking but
		// hold onto the error so the eventual caller can surface it.
		// (Today we just continue; future work could collect a list.)
		_ = err
	}

	// 2. Anthropic Models API — for API-key users.
	apiModels, apiErr := s.Fetcher.Fetch(ctx)
	if apiErr == nil {
		return s.writeAndReturn(Catalog{
			Models:    apiModels,
			FetchedAt: time.Now().UTC(),
			Source:    SourceAPI,
		})
	}
	if errors.Is(apiErr, ErrNoAPIKey) {
		// Both live sources unavailable — write a fallback-only
		// stamp so the cache file exists and the caller can see how
		// stale "live" is from the FetchedAt. The Models slice is
		// left nil; withFallback fills it in for the response.
		return s.writeAndReturn(Catalog{
			Models:    nil,
			FetchedAt: time.Now().UTC(),
			Source:    SourceFallback,
		})
	}
	// Real failure on the API side (5xx, network) — bubble up so
	// the caller can log it. Caller is expected to fall back to a
	// cached/curated response in this branch.
	return Catalog{}, apiErr
}

// writeAndReturn persists a freshly-fetched catalog to disk and
// returns it. Cache write failures are non-fatal — we still return
// the catalog so the in-memory result of this Refresh is usable,
// just with a non-nil error so the caller can log.
func (s *Service) writeAndReturn(cat Catalog) (Catalog, error) {
	if err := s.cache.Write(cat); err != nil {
		return cat, fmt.Errorf("cache write (catalog still returned): %w", err)
	}
	return cat, nil
}

// withFallback merges curated fallbacks in and sorts for display.
// Centralised here so Catalog and Refresh callers get the same shape.
func (s *Service) withFallback(cat Catalog) Catalog {
	merged := Merge(cat.Models, Fallback())
	Sort(merged)
	out := cat
	out.Models = merged
	return out
}

// CachePath returns the conventional cache file location for the
// daemon. Mirrors the rest of ccmuxd's state-dir layout
// (~/.local/state/ccmux/...). Exposed so cmd/ccmuxd can construct
// the Service without re-deriving the path itself.
func CachePath() (string, error) {
	state, err := stateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(state, "models.json"), nil
}

func stateDir() (string, error) {
	if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		return filepath.Join(v, "ccmux"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "ccmux"), nil
}
