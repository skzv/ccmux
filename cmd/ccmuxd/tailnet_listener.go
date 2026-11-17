package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/exec"
	"time"
)

// tailnetRetryInterval is how often serveTailnet retries when Tailscale
// isn't up (or the bind fails).
const tailnetRetryInterval = 30 * time.Second

// serveTailnet binds srv to this machine's tailnet address and serves
// until ctx is cancelled. At login the daemon often starts before
// Tailscale has an IP; a one-shot bind left the listener off until the
// next daemon restart, while pairing kept handing out URLs for it. So
// a failed lookup or bind is retried every `retry`, and a serve error
// (e.g. the tailnet IP went away) goes back to looking up the address.
// s.tailnetLive tracks whether the listener is up right now.
func (s *server) serveTailnet(ctx context.Context, srv *http.Server, addrFor func(context.Context, int) (string, error), retry time.Duration) {
	port := s.cfg.Daemon.TailnetPort
	loggedDown := false
	for {
		addr, err := addrFor(ctx, port)
		var ln net.Listener
		if err == nil {
			ln, err = (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
		}
		if err != nil {
			if !loggedDown {
				log.Printf("ccmuxd: tailnet listener not up yet (retrying every %s): %v", retry, err)
				loggedDown = true
			}
		} else {
			loggedDown = false
			log.Printf("ccmuxd: tailnet listening on %s", addr)
			s.tailnetLive.Store(true)
			err = srv.Serve(ln)
			s.tailnetLive.Store(false)
			if errors.Is(err, http.ErrServerClosed) {
				return
			}
			log.Printf("ccmuxd: tailnet serve: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(retry):
		}
	}
}

// tailscaleAddr returns "<tailscale_ip>:<port>" if Tailscale is running, else error.
func tailscaleAddr(ctx context.Context, port int) (string, error) {
	// Bound the shell-out: CLAUDE.md requires every exec.Command to take
	// a context, and this is called from a request handler (handlePairToken)
	// with no other timeout. A 5s ceiling keeps a wedged `tailscale` CLI
	// from blocking the pairing request indefinitely.
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, "tailscale", "ip", "-4").Output()
	if err != nil {
		return "", err
	}
	ip := string(out)
	for _, c := range []byte{'\n', '\r', ' ', '\t'} {
		if i := indexByte(ip, c); i >= 0 {
			ip = ip[:i]
		}
	}
	if ip == "" {
		return "", fmt.Errorf("tailscale ip -4 returned empty")
	}
	return fmt.Sprintf("%s:%d", ip, port), nil
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
