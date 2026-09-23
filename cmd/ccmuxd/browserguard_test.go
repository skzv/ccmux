package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRejectBrowserRequests(t *testing.T) {
	var reached bool
	h := rejectBrowserRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	}))

	cases := []struct {
		name    string
		method  string
		headers map[string]string
		want    int
	}{
		{"native POST", http.MethodPost, map[string]string{"Content-Type": "application/json"}, http.StatusOK},
		{"native GET", http.MethodGet, nil, http.StatusOK},
		{"cross-origin simple POST", http.MethodPost, map[string]string{"Origin": "http://evil.example", "Content-Type": "text/plain"}, http.StatusForbidden},
		{"websocket upgrade from page", http.MethodGet, map[string]string{"Origin": "https://evil.example", "Upgrade": "websocket"}, http.StatusForbidden},
		{"rebinding same-origin GET", http.MethodGet, map[string]string{"Sec-Fetch-Mode": "cors", "Sec-Fetch-Site": "same-origin"}, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached = false
			req := httptest.NewRequest(tc.method, "/v1/sessions/c-x/send-keys", strings.NewReader(`{"keys":"x"}`))
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
			if reached != (tc.want == http.StatusOK) {
				t.Fatalf("handler reached = %v, want %v", reached, tc.want == http.StatusOK)
			}
		})
	}
}
