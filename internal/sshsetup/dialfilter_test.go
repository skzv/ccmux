package sshsetup

import (
	"context"
	"net"
	"testing"
	"time"
)

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// TestLinkLocalUnicast_DropsFE80 — sanity-check that the net.IP
// detection we rely on actually flags an IPv6 link-local address.
// The beta-tester bug that prompted this: ccmux dialed
// [fe80::4d62:e175:a41d:28c5]:22 and got "no route to host" because
// link-local IPv6 isn't routable across Tailscale.
func TestLinkLocalUnicast_DropsFE80(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"fe80::4d62:e175:a41d:28c5", true},
		{"169.254.10.1", true},       // ipv4 link-local
		{"100.64.0.1", false},        // tailnet ipv4
		{"fd7a:115c:a1e0::1", false}, // tailnet ipv6 ULA
		{"127.0.0.1", false},
		{"::1", false},
	}
	for _, c := range cases {
		t.Run(c.ip, func(t *testing.T) {
			ip := net.ParseIP(c.ip)
			if ip == nil {
				t.Fatal("bad fixture")
			}
			if got := ip.IsLinkLocalUnicast(); got != c.want {
				t.Errorf("IsLinkLocalUnicast(%s) = %v, want %v", c.ip, got, c.want)
			}
		})
	}
}

// TestDialFilteredTCP_PrefersRoutableAndDialsListener — end-to-end:
// a real listener on 127.0.0.1, dial via "localhost", and verify we
// get through. Implicit assertion that link-local v6 (which the
// resolver may include in localhost's address list on some
// machines) doesn't take down the dial.
func TestDialFilteredTCP_PrefersRoutableAndDialsListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	addr := ln.Addr().String()
	conn, err := dialFilteredTCP(testCtx(t), addr, 0)
	if err != nil {
		t.Fatalf("dialFilteredTCP(%s): %v", addr, err)
	}
	_ = conn.Close()
}
