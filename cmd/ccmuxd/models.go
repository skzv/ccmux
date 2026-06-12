package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/skzv/ccmux/internal/claudemodels"
)

// modelRefreshInterval is how often the daemon re-fetches the model
// catalog. The primary discovery path is `claude -p` (an LLM call,
// ~$0.024 per refresh), and the catalog only meaningfully changes a
// handful of times per year, so weekly is the right cadence: ~$0.10/
// month per user, with new models landing in the picker within a
// week of Anthropic shipping them. Users who want it now run
// `ccmux agents models --refresh`.
const modelRefreshInterval = 7 * 24 * time.Hour

// modelRefreshLoop runs an immediate startup refresh, then re-fetches
// the catalog on a 24h interval until ctx is cancelled. Runs in its
// own goroutine so a slow API call can't stall the poll loop.
//
// Startup behavior: kick a refresh on boot so first-attach users
// don't see a stale or empty cache. If the cache is fresh enough to
// skip, Service.Catalog short-circuits and no HTTP call goes out;
// this loop just times the next forced Refresh.
//
// Errors are logged and dropped — the next tick retries. A persistent
// failure (no API key, bad key, network down) silently degrades to
// the curated fallback list, which is what we want.
func (s *server) modelRefreshLoop(ctx context.Context) {
	if s.models == nil {
		return
	}
	// Boot kick: if the cache is missing or older than the interval,
	// fetch synchronously inside this goroutine (still doesn't block
	// the daemon's main loop because we're already off the main path).
	// Catalog handles the staleness check, so we just call it and
	// log any error that surfaces.
	if _, err := s.models.Catalog(ctx); err != nil && !errors.Is(err, claudemodels.ErrNoAPIKey) {
		log.Printf("ccmuxd: model catalog boot refresh: %v", err)
	}

	t := time.NewTicker(modelRefreshInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := s.models.Refresh(ctx); err != nil && !errors.Is(err, claudemodels.ErrNoAPIKey) {
				log.Printf("ccmuxd: model catalog refresh: %v", err)
			}
		}
	}
}

// handleModels serves GET /v1/models — the discovered + curated
// model catalog. ?refresh=true forces a synchronous re-fetch before
// responding; without it the response comes from the cached catalog
// (which Service.Catalog refreshes opportunistically when stale).
//
// Returns claudemodels.Catalog verbatim — that's the public shape
// integrators key off, documented in docs/02_Architecture/05_HTTP_API.md.
// A failed refresh still returns 200 with whatever the cache had,
// plus the error surfaced through the response Source: a client that
// cares can distinguish "live" from "fallback" via cat.Source.
//
// No authentication beyond the standard tailnet-is-the-boundary
// model — the catalog is public information.
func (s *server) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.models == nil {
		// Defensive: newServer always populates this, but a future
		// constructor variant might not. Fall back to the bare
		// in-binary list so a downstream client never sees an empty
		// response that it might interpret as "no models available".
		writeJSON(w, claudemodels.Catalog{
			Models: claudemodels.Fallback(),
			Source: claudemodels.SourceFallback,
		})
		return
	}

	ctx := r.Context()
	var (
		cat claudemodels.Catalog
		err error
	)
	if r.URL.Query().Get("refresh") == "true" {
		cat, err = s.models.Refresh(ctx)
		if err != nil && !errors.Is(err, claudemodels.ErrNoAPIKey) {
			// Surface the refresh error but still return 200 with the
			// cached catalog if we have one — that's more useful to a
			// caller than a 500. The client can spot the degraded
			// source via cat.Source if it cares.
			log.Printf("ccmuxd: /v1/models forced refresh: %v", err)
			cat, _ = s.models.Catalog(ctx)
		}
	} else {
		cat, _ = s.models.Catalog(ctx)
	}
	writeJSON(w, cat)
}
