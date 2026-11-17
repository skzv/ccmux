package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/skzv/ccmux/internal/daemon"
)

func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	ch := s.events.Subscribe()
	defer s.events.Unsubscribe(ch)
	enc := json.NewEncoder(w)
	// Push the headers + a comment frame immediately. Without this,
	// http.Get on the client side blocks until the handler writes
	// something — which doesn't happen until the first event arrives
	// (or the heartbeat fires 20s later).
	if _, err := fmt.Fprintf(w, ": connected\n\n"); err != nil {
		return
	}
	flusher.Flush()
	// Heartbeat keeps idle connections alive across NAT timeouts and
	// gives a fast path to detect a dead client. SSE comment lines
	// (starting with ":") are ignored by the EventSource spec.
	hb := time.NewTicker(20 * time.Second)
	defer hb.Stop()
	var lastDropsReported uint64
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.streamsDone: // daemon shutting down (nil: never)
			return
		case <-hb.C:
			if _, err := fmt.Fprintf(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case ev, open := <-ch:
			if !open {
				return
			}
			// If the subscriber missed events while we weren't reading
			// fast enough, surface it as a synthetic frame so clients
			// can refresh state instead of silently desyncing. Publish
			// drops the *newest* events on overflow, so everything still
			// buffered (this event included) predates the snapshot the
			// client fetches when it sees the frame; streaming it after
			// the frame replayed up to a buffer's worth of stale events
			// on top of that snapshot. Discard it and count it as missed.
			if s.events.Dropped(ch) > lastDropsReported {
				discarded := 1 + drainEvents(ch)
				d := s.events.Dropped(ch)
				if _, err := fmt.Fprintf(w, "event: drops\ndata: %d\n\n", d-lastDropsReported+uint64(discarded)); err != nil {
					return
				}
				lastDropsReported = d
				flusher.Flush()
				continue
			}
			if _, err := fmt.Fprintf(w, "data: "); err != nil {
				return
			}
			if err := enc.Encode(ev); err != nil {
				return
			}
			if _, err := fmt.Fprintf(w, "\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// drainEvents empties a subscriber channel without blocking and returns
// how many events it discarded.
func drainEvents(ch chan daemon.SessionEvent) int {
	n := 0
	for {
		select {
		case _, open := <-ch:
			if !open {
				return n
			}
			n++
		default:
			return n
		}
	}
}
