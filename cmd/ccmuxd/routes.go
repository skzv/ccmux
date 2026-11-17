package main

import (
	"net/http"
	"os"

	"github.com/skzv/ccmux/internal/daemon"
)

// routes registers every tailnet-safe endpoint. Anything that an
// unauthenticated tailnet peer is allowed to hit goes here. The Unix
// socket additionally registers localOnlyRoutes; the tailnet listener
// does not.
func (s *server) routes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/health", s.handleHealth)
	mux.HandleFunc("/v1/sessions", s.handleSessions)
	// /v1/sessions/bare lives BEFORE /v1/sessions/ so the more
	// specific route matches first. ServeMux's longest-prefix match
	// would handle this either way, but the explicit ordering makes
	// the relationship obvious to a reader.
	mux.HandleFunc("/v1/sessions/bare", s.createBareSession)
	mux.HandleFunc("/v1/sessions/", s.handleSessionsItem)
	mux.HandleFunc("/v1/projects", s.handleProjects)
	mux.HandleFunc("/v1/pair", s.handlePair)
	mux.HandleFunc("/v1/events", s.handleEvents)
	mux.HandleFunc("/v1/devices", s.handleRegisterDevice)
	mux.HandleFunc("/v1/devices/test", s.handleTestPush)
	mux.HandleFunc("/v1/peers", s.handlePeers)
	mux.HandleFunc("/v1/conversations", s.handleConversations)
	mux.HandleFunc("/v1/usage", s.handleUsage)
	mux.HandleFunc("/v1/notes/search", s.handleNotesSearch)
	mux.HandleFunc("/v1/notes", s.handleNotes)
	mux.HandleFunc("/v1/models", s.handleModels)
}

// localOnlyRoutes registers endpoints that must never be reachable from
// the tailnet. /v1/pair-token mints a one-time pairing token, so a
// tailnet peer that could hit it would just issue itself a token and
// then redeem it on /v1/pair — defeating the whole point of pairing.
// The function exists separately so the tailnet HTTP listener stays
// structurally unable to register these.
func (s *server) localOnlyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/pair-token", s.handlePairToken)
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	host, _ := os.Hostname()
	s.mu.Lock()
	n := len(s.seen)
	s.mu.Unlock()
	writeJSON(w, daemon.HealthInfo{
		OK: true, Hostname: host, Version: version, Sessions: n, SleepMode: string(s.sleeper.Effective()),
	})
}
