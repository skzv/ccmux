package sshsetup

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// TestClassifyProbeStderr_HitsEveryBranch ensures every documented
// failure mode maps to the expected ProbeResult. Adding a new
// failure shape later is a small append here + an explicit test
// case below — both will be loud if either gets out of step.
func TestClassifyProbeStderr_HitsEveryBranch(t *testing.T) {
	cases := []struct {
		name   string
		stderr string
		port   int
		want   ProbeResult
	}{
		{"auth-failed-permission", "Permission denied (publickey).", 22, ProbeAuthFailed},
		{"auth-failed-no-supported", "No supported authentication methods available", 22, ProbeAuthFailed},
		{"host-key-mismatch", "Host key verification failed.", 22, ProbeHostKeyMismatch},
		{"host-key-rotated", "REMOTE HOST IDENTIFICATION HAS CHANGED!", 22, ProbeHostKeyMismatch},
		{"timeout-connect", "ssh: connect to host x port 22: Connection timed out", 22, ProbeTimeout},
		{"timeout-operation", "Operation timed out", 22, ProbeTimeout},
		{"no-network-resolve", "ssh: Could not resolve hostname x: nodename nor servname provided", 22, ProbeNoNetwork},
		{"no-network-nss", "Name or service not known", 22, ProbeNoNetwork},
		{"refused-port-22", "ssh: connect to host x port 22: Connection refused", 22, ProbeSshdDisabled},
		{"refused-other-port", "ssh: connect to host x port 2222: Connection refused", 2222, ProbeRefused},
		{"unknown", "some weird unstructured error", 22, ProbeUnknown},
		{"empty", "", 22, ProbeUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classifyProbeStderr(c.stderr, c.port)
			if got != c.want {
				t.Errorf("classifyProbeStderr(%q, %d) = %v, want %v", c.stderr, c.port, got, c.want)
			}
		})
	}
}

// TestProbeTCP_RefusedOnRealPort — listener closes, follow-up dial
// hits ECONNREFUSED, and probeTCP maps that to ProbeRefused (or
// ProbeSshdDisabled if the port happens to be 22). The "lie about
// port 22 to force SshdDisabled" path is intentionally NOT tested
// hermetically — port 22 may be live on the dev's machine, which
// turns the test into a flake. The mapping itself is covered by
// classifyProbeStderr's table-driven test.
func TestProbeTCP_RefusedOnRealPort(t *testing.T) {
	addr := closedPort(t)
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	for _, r := range portStr {
		port = port*10 + int(r-'0')
	}
	got := probeTCP(context.Background(), Target{Host: host, Port: port})
	if got != ProbeRefused && got != ProbeSshdDisabled {
		t.Errorf("probeTCP(%s) = %v, want Refused or SshdDisabled", addr, got)
	}
}

// TestProbeTCP_OK — handshake completes against a real listener.
// Sanity check that we don't false-positive on the dial-only path
// for a TCP-accepting target that wouldn't actually pass SSH.
func TestProbeTCP_OK(t *testing.T) {
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
	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	var port int
	for _, r := range portStr {
		port = port*10 + int(r-'0')
	}
	got := probeTCP(context.Background(), Target{Host: host, Port: port})
	if got != ProbeOK {
		t.Errorf("probeTCP against live listener = %v, want OK", got)
	}
}

// TestProbeTCP_NoNetwork — DNS resolution failure. We can't easily
// force a DNSError in a hermetic test without overriding the
// resolver, so we use an obviously-bogus name. CI may flake here
// if a misconfigured resolver returns NXDOMAIN as a captured IP,
// so we assert on "not OK" rather than the specific bucket.
func TestProbeTCP_NoNetwork(t *testing.T) {
	got := probeTCP(context.Background(), Target{
		Host: "this-host-should-never-resolve-ccmux-test.invalid",
		Port: 22,
	})
	if got == ProbeOK {
		t.Errorf("probeTCP against bogus DNS = OK; want any failure result")
	}
}

// TestProbeTCP_TimeoutHonored — verify probeTCP returns within ~2.5s
// even when the destination is a blackhole. We dial 10.255.255.1 / 22
// which is RFC1918 unreachable on most CI runners. The exact bucket
// (Timeout vs NoNetwork) depends on the host's routing, so we
// just pin the wall-clock budget.
func TestProbeTCP_TimeoutHonored(t *testing.T) {
	if testing.Short() {
		t.Skip("network blackhole probe; skip in -short")
	}
	start := time.Now()
	probeTCP(context.Background(), Target{Host: "10.255.255.1", Port: 22})
	elapsed := time.Since(start)
	if elapsed > 4*time.Second {
		t.Errorf("probeTCP took %v, expected <4s", elapsed)
	}
}

// TestProbe_TopLevel_ReturnsUnknownOnMissingHost — the top-level
// Probe function must be defensive against zero-value Target.
func TestProbe_TopLevel_ReturnsUnknownOnMissingHost(t *testing.T) {
	got := Probe(context.Background(), Target{})
	if got != ProbeUnknown {
		t.Errorf("Probe(zero) = %v, want Unknown", got)
	}
}

// closedPort returns a "host:port" that has just been closed, so any
// dial against it gets ECONNREFUSED. The dial happens AFTER the
// listener is closed, but the port stays in TIME_WAIT briefly — we
// don't care because the kernel still rejects new SYNs cleanly.
func closedPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	// Sanity: a follow-up dial must refuse, not stall.
	d := net.Dialer{Timeout: 200 * time.Millisecond}
	conn, err := d.Dial("tcp", addr)
	if err == nil {
		_ = conn.Close()
		t.Fatalf("port %s did not refuse after close", addr)
	}
	if !strings.Contains(err.Error(), "refused") {
		t.Skipf("dial after listener close didn't return ECONNREFUSED on this host: %v", err)
	}
	return addr
}
