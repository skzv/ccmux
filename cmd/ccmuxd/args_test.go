package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestHandleArgs — ccmuxd used to ignore its arguments, so
// `ccmuxd --version` started a whole daemon. Every non-empty command
// line must now exit before run(), and only an empty one starts the
// daemon.
func TestHandleArgs(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		exit     bool
		code     int
		stdout   string
		stderrIn string
	}{
		{name: "no args starts the daemon", args: nil, exit: false, code: 0},
		{name: "--version", args: []string{"--version"}, exit: true, code: 0, stdout: "ccmuxd " + version + "\n"},
		{name: "-version", args: []string{"-version"}, exit: true, code: 0, stdout: "ccmuxd " + version + "\n"},
		{name: "--help", args: []string{"--help"}, exit: true, code: 0, stderrIn: "usage: ccmuxd"},
		{name: "unknown flag", args: []string{"--daemonize"}, exit: true, code: 2, stderrIn: "flag provided but not defined"},
		{name: "stray argument", args: []string{"start"}, exit: true, code: 2, stderrIn: `unexpected argument "start"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exit, code := handleArgs(tc.args, &stdout, &stderr)
			if exit != tc.exit || code != tc.code {
				t.Fatalf("handleArgs(%q) = (%v, %d), want (%v, %d)", tc.args, exit, code, tc.exit, tc.code)
			}
			if stdout.String() != tc.stdout {
				t.Errorf("stdout = %q, want %q", stdout.String(), tc.stdout)
			}
			if tc.stderrIn != "" && !strings.Contains(stderr.String(), tc.stderrIn) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tc.stderrIn)
			}
		})
	}
}
