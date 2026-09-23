package main

import (
	"log"
	"net/http"
)

// rejectBrowserRequests wraps the tailnet mux so a web page can't drive
// the daemon. The tailnet listener has no per-request auth — the tailnet
// is the trust boundary — which means any browser tab on any tailnet
// device could otherwise POST to /v1/sessions/<n>/send-keys (a "simple"
// cross-origin request needs no preflight) or open the attach WebSocket,
// i.e. type into a live shell.
//
// Native clients (the ccmux CLI/TUI, ccmux-mcp, the iOS and Android
// apps) never send Origin or Sec-Fetch-* headers. Browsers always send
// Origin on POST and WebSocket upgrades, and send Sec-Fetch-Mode on
// every request — which also covers DNS-rebinding reads, where the page
// is same-origin with the daemon and a GET would carry no Origin.
func rejectBrowserRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isBrowserRequest(r) {
			log.Printf("ccmuxd: rejected browser request %s %s from %s (origin %q)",
				r.Method, r.URL.Path, r.RemoteAddr, r.Header.Get("Origin"))
			http.Error(w, "browser requests are not accepted", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isBrowserRequest(r *http.Request) bool {
	return r.Header.Get("Origin") != "" ||
		r.Header.Get("Sec-Fetch-Mode") != "" ||
		r.Header.Get("Sec-Fetch-Site") != ""
}
