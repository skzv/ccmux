package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
)

// TestHandleKill_RejectsGetMethod — kill is POST-only; a stray GET
// (e.g. a browser opening the URL) must not kill a session.
func TestHandleKill_RejectsGetMethod(t *testing.T) {
	s := &server{cfg: config.Config{}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/mysession/kill", nil)
	s.handleKill(rec, req, "mysession")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET kill: status = %d, want 405", rec.Code)
	}
}

// TestHandleSendKeys_RejectsGetMethod — send-keys is POST-only.
func TestHandleSendKeys_RejectsGetMethod(t *testing.T) {
	s := &server{cfg: config.Config{}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/mysession/send-keys", nil)
	s.handleSendKeys(rec, req, "mysession")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET send-keys: status = %d, want 405", rec.Code)
	}
}

// TestHandleSendKeys_RejectsMissingKeys — the request body must include
// a non-empty "keys" field; without it there's nothing to send.
func TestHandleSendKeys_RejectsMissingKeys(t *testing.T) {
	s := &server{cfg: config.Config{}}
	body, _ := json.Marshal(daemon.SendKeysRequest{Keys: ""})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/mysession/send-keys", bytes.NewReader(body))
	s.handleSendKeys(rec, req, "mysession")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty keys: status = %d, want 400", rec.Code)
	}
}

// TestHandlePreview_RejectsPostMethod — preview is GET-only.
func TestHandlePreview_RejectsPostMethod(t *testing.T) {
	s := &server{cfg: config.Config{}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/mysession/preview", nil)
	s.handlePreview(rec, req, "mysession")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST preview: status = %d, want 405", rec.Code)
	}
}

// TestHandleSessionsItem_RoutingEdgeCases pins the router inside
// handleSessionsItem: empty session name → 400, missing subaction → 400,
// unknown subaction → 404.
func TestHandleSessionsItem_RoutingEdgeCases(t *testing.T) {
	s := &server{
		cfg:    config.Config{},
		events: daemon.NewEventBus(),
		seen:   map[string]*tracked{},
	}

	cases := []struct {
		path string
		want int
	}{
		{"/v1/sessions//kill", http.StatusBadRequest},   // empty name
		{"/v1/sessions/foo", http.StatusBadRequest},     // no subaction
		{"/v1/sessions/foo/bogus", http.StatusNotFound}, // unknown subaction
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			s.handleSessionsItem(rec, req)
			if rec.Code != tc.want {
				t.Errorf("%s: status = %d, want %d", tc.path, rec.Code, tc.want)
			}
		})
	}
}
