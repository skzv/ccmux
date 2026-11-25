//go:build !windows

package tmux

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSessionTargetGuards_RejectDollarAndColon(t *testing.T) {
	calls := optionTmux(t)
	ctx := context.Background()

	ops := map[string]func(string) error{
		"Has": func(n string) error {
			_, err := Has(ctx, n)
			return err
		},
		"Kill":        func(n string) error { return Kill(ctx, n) },
		"RenameOld":   func(n string) error { return Rename(ctx, n, "ok") },
		"RenameNew":   func(n string) error { return Rename(ctx, "ok", n) },
		"CapturePane": func(n string) error { _, err := CapturePane(ctx, n, 10); return err },
		"SendKeys":    func(n string) error { return SendKeys(ctx, n, "Enter") },
		"SendText":    func(n string) error { return SendText(ctx, n, "hi") },
		"RingBell":    func(n string) error { return RingBell(ctx, n) },
		"ListPanes":   func(n string) error { _, err := ListPanes(ctx, n); return err },
		"New":         func(n string) error { return New(ctx, n, "", "") },
	}

	for opName, fn := range ops {
		for _, bad := range []string{"", "$1", "a:b", "x\ny"} {
			err := fn(bad)
			if err == nil {
				t.Errorf("%s(%q) = nil, want error", opName, bad)
			}
			if strings.HasPrefix(bad, "$") && !errors.Is(err, ErrSessionIDTarget) {
				t.Errorf("%s(%q) = %v, want ErrSessionIDTarget", opName, bad, err)
			}
		}
	}
	if got := calls(); len(got) != 0 {
		t.Fatalf("unsafe targets reached tmux: %v", got)
	}
}

func TestTmuxTrailingSemicolonAndHexSendText(t *testing.T) {
	calls := optionTmux(t)
	ctx := context.Background()

	if err := NewWithAgent(ctx, "c-demo", "/tmp/dir;", "-foo;", "claude"); err != nil {
		t.Fatalf("NewWithAgent: %v", err)
	}
	if err := SendKeys(ctx, "c-demo", ";"); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}
	if err := SendText(ctx, "c-demo", "a;"); err != nil {
		t.Fatalf("SendText: %v", err)
	}

	got := calls()
	if len(got) != 3 {
		t.Fatalf("got %d tmux calls, want 3: %v", len(got), got)
	}
	if !strings.Contains(got[0], `-c|/tmp/dir\;|--|-foo\;|;|set-option|`) {
		t.Errorf("NewWithAgent argv = %q, want escaped dir and -- before cmdline", got[0])
	}
	if !strings.Contains(got[1], `send-keys|-t|=c-demo:|--|\;|`) {
		t.Errorf("SendKeys argv = %q, want escaped trailing semicolon", got[1])
	}
	if !strings.Contains(got[2], `send-keys|-t|=c-demo:|-H|61|3b|`) {
		t.Errorf("SendText argv = %q, want -H hex bytes 61 3b", got[2])
	}
}
