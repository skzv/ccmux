//go:build !windows

package tmux

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// optionTmux puts a tmux on PATH that records each invocation's argv
// (args joined by "|", one line per call) and succeeds, and returns a
// reader for the log.
func optionTmux(t *testing.T) func() []string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "tmux.log")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s|' \"$a\"; done >> '" + log + "'\necho >> '" + log + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("TMUX", "")
	return func() []string {
		b, err := os.ReadFile(log)
		if err != nil {
			return nil
		}
		return strings.Split(strings.TrimSpace(string(b)), "\n")
	}
}

// TestSetOptionPaths_RefuseUntargetableNames — the session option
// writers (SetSessionAgent, SetSessionSpinner, SetSessionReview, and
// NewWithAgent's tag) wrote on "=<name>:", which for "$1" tmux reads as
// the session whose ID is $1: the option landed on some other session.
// Every name CheckTarget refuses is refused here too, before tmux is
// asked anything; a valid name still gets its exact target.
func TestSetOptionPaths_RefuseUntargetableNames(t *testing.T) {
	calls := optionTmux(t)
	ctx := context.Background()
	writers := map[string]func(name string) error{
		"SetSessionAgent":   func(n string) error { return SetSessionAgent(ctx, n, "codex") },
		"SetSessionSpinner": func(n string) error { return SetSessionSpinner(ctx, n, "claude") },
		"SetSessionReview":  func(n string) error { return SetSessionReview(ctx, n, Review{Recorded: true, Seen: true}) },
		"NewWithAgent":      func(n string) error { return NewWithAgent(ctx, n, "", "", "claude") },
	}
	for fn, write := range writers {
		for _, name := range []string{"$1", "$x", "a:b", "a.b", "a/b", "", "x\ny"} {
			err := write(name)
			if !errors.Is(err, ErrUntargetable) {
				t.Errorf("%s(%q) = %v, want ErrUntargetable", fn, name, err)
			}
			if strings.HasPrefix(name, "$") && !errors.Is(err, ErrSessionIDTarget) {
				t.Errorf("%s(%q) = %v, want it to say tmux reads it as a session ID", fn, name, err)
			}
		}
		if got := calls(); len(got) != 0 {
			t.Fatalf("%s sent untargetable names to tmux: %v", fn, got)
		}
	}

	for fn, write := range writers {
		if err := write("c-ok"); err != nil {
			t.Errorf("%s(c-ok) = %v", fn, err)
		}
	}
	for _, c := range calls() {
		if !strings.Contains(c, "set-option|-t|=c-ok:|") {
			t.Errorf("tmux call %q should set an option on =c-ok:", c)
		}
	}

	// Plain New (no tag) still creates any name tmux takes, dotted ones
	// included (TestIntegration_DottedSessionNames).
	if err := New(ctx, "api.v2", "", ""); err != nil {
		t.Errorf("New(api.v2) = %v", err)
	}
}
