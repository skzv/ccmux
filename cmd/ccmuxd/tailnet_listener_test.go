package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/config"
)

// TestServeTailnet_RetriesUntilTailscaleUp — at login ccmuxd often
// starts before Tailscale has an address. The listener must come up
// once it does, not stay off until the next daemon restart.
func TestServeTailnet_RetriesUntilTailscaleUp(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	var lookups atomic.Int32
	addrFor := func(context.Context, int) (string, error) {
		if lookups.Add(1) < 3 {
			return "", errors.New("tailscale: not connected")
		}
		return addr, nil
	}
	s := &server{cfg: config.Config{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", func(w http.ResponseWriter, r *http.Request) {})
	srv := newHTTPServer(mux)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.serveTailnet(ctx, srv, addrFor, 10*time.Millisecond); close(done) }()

	deadline := time.Now().Add(3 * time.Second)
	for !s.tailnetLive.Load() {
		if time.Now().After(deadline) {
			t.Fatalf("listener never came up (lookups=%d)", lookups.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}
	resp, err := http.Get("http://" + addr + "/v1/health")
	if err != nil {
		t.Fatalf("listener up but not serving: %v", err)
	}
	_ = resp.Body.Close()

	cancel()
	_ = srv.Shutdown(context.Background())
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("serveTailnet did not return after shutdown")
	}
	if s.tailnetLive.Load() {
		t.Error("tailnetLive still true after shutdown")
	}
}
