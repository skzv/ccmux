package cmd

import (
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/sshsetup"
)

// TestAgentInstallHint_CoversAllShippedAgents — every agent in
// agent.All() must produce a non-empty install hint. Doctor surfaces
// the hint when the binary is missing; if a future agent gets added
// without a hint, this test flags it before users see a blank "install:
// " line.
func TestAgentInstallHint_CoversAllShippedAgents(t *testing.T) {
	for _, a := range agent.All() {
		t.Run(string(a.ID()), func(t *testing.T) {
			hint := agentInstallHint(a.ID())
			if hint == "" {
				t.Fatalf("no install hint for %s — every shipped agent needs one", a.ID())
			}
		})
	}
}

// TestAgentInstallHint_HasActionableCommand — each hint should include
// either an `npm i -g` snippet or a documentation URL. A hint of just
// "go check the docs" would be unhelpful when the user is stuck at
// `ccmux doctor` output.
func TestAgentInstallHint_HasActionableCommand(t *testing.T) {
	for _, a := range agent.All() {
		t.Run(string(a.ID()), func(t *testing.T) {
			hint := agentInstallHint(a.ID())
			if !strings.Contains(hint, "npm i -g") && !strings.Contains(hint, "http") {
				t.Errorf("%s install hint lacks a runnable command or URL: %q",
					a.ID(), hint)
			}
		})
	}
}

// TestAgentInstallHint_UnknownReturnsEmpty — the function falls back
// to "" for ids not in the switch. This is just a defensive code-path
// pin so a future ParseID-bypassing caller (shouldn't exist) doesn't
// crash on a typo'd id.
func TestAgentInstallHint_UnknownReturnsEmpty(t *testing.T) {
	if got := agentInstallHint(agent.ID("imaginary")); got != "" {
		t.Errorf("agentInstallHint(unknown) = %q, want empty", got)
	}
}

// TestProbeResultLine_NamesPortWhenPortIsSuspect — doctor's job on a
// failing host is to point at the cause. When the host has a custom
// ssh_port, "which port did you try?" is the first question, so every
// branch whose cause could be a wrong port must name it.
//
// The timeout branch used to be the exception: it said "timeout
// reaching mini" while refused/OK both reported the port — leaving the
// custom-port user without the one fact that distinguishes "host is
// down" from "ccmux probed the wrong port".
func TestProbeResultLine_NamesPortWhenPortIsSuspect(t *testing.T) {
	target := sshsetup.Target{User: "sasha", Host: "mini", Port: 2222}

	portBranches := []struct {
		name string
		res  sshsetup.ProbeResult
	}{
		{"refused", sshsetup.ProbeRefused},
		{"timeout", sshsetup.ProbeTimeout},
	}
	for _, tc := range portBranches {
		t.Run(tc.name, func(t *testing.T) {
			line, ok := probeResultLine(tc.res, "portbox", target)
			if ok {
				t.Errorf("%s should count as unhealthy", tc.name)
			}
			if !strings.Contains(line, "2222") {
				t.Errorf("%s line must name the probed port; got %q", tc.name, line)
			}
			if strings.Contains(line, "port 22 on") {
				t.Errorf("%s line names the default port despite ssh_port=2222; got %q", tc.name, line)
			}
		})
	}
}

// TestProbeResultLine_HealthyAndExitAccounting — the bool drives
// doctor's exit code, so it has to match the symbol in the line.
func TestProbeResultLine_HealthyAndExitAccounting(t *testing.T) {
	target := sshsetup.Target{Host: "mini", Port: 22}
	cases := []struct {
		res       sshsetup.ProbeResult
		wantOK    bool
		wantMarks string
	}{
		{sshsetup.ProbeOK, true, "✓"},
		{sshsetup.ProbeAuthFailed, false, "✗"},
		{sshsetup.ProbeSshdDisabled, false, "✗"},
		{sshsetup.ProbeRefused, false, "✗"},
		{sshsetup.ProbeTimeout, false, "·"},
		{sshsetup.ProbeNoNetwork, false, "·"},
		{sshsetup.ProbeHostKeyMismatch, false, "✗"},
	}
	for _, tc := range cases {
		line, ok := probeResultLine(tc.res, "mini", target)
		if ok != tc.wantOK {
			t.Errorf("%v: ok = %v, want %v (drives doctor's exit code)", tc.res, ok, tc.wantOK)
		}
		if !strings.Contains(line, tc.wantMarks) {
			t.Errorf("%v: line %q missing marker %q", tc.res, line, tc.wantMarks)
		}
	}
}

// TestProbeResultLine_NoNetworkOmitsPort — nothing was dialed when name
// resolution failed, so naming a port there would be misleading noise.
func TestProbeResultLine_NoNetworkOmitsPort(t *testing.T) {
	line, _ := probeResultLine(sshsetup.ProbeNoNetwork, "mini", sshsetup.Target{Host: "mini", Port: 2222})
	if strings.Contains(line, "2222") {
		t.Errorf("no-network line should not name a port (none was dialed); got %q", line)
	}
}
