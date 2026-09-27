package main

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/config"
)

// TestPollLoop_HugeIntervalsAreClamped — poll_interval_seconds =
// 10000000000 overflowed once multiplied by time.Second, and the
// negative result made time.NewTicker panic: a crash loop under
// launchd. The settings are clamped to an hour.
func TestPollLoop_HugeIntervalsAreClamped(t *testing.T) {
	s := newPollTestServer(t)
	s.cfg.Daemon.PollIntervalSeconds = 10000000000
	s.cfg.Daemon.IdleSecondsForNeedsInput = 10000000000
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.pollLoop(ctx) // must return, not panic

	d := s.cfg.Daemon
	applyDaemonDefaults(&d)
	if d.PollIntervalSeconds != maxDaemonSeconds || d.IdleSecondsForNeedsInput != maxDaemonSeconds {
		t.Errorf("clamped = %d/%d, want %d/%d", d.PollIntervalSeconds, d.IdleSecondsForNeedsInput, maxDaemonSeconds, maxDaemonSeconds)
	}
	d = config.DaemonConfig{PollIntervalSeconds: 5, IdleSecondsForNeedsInput: 30}
	applyDaemonDefaults(&d)
	if d.PollIntervalSeconds != 5 || d.IdleSecondsForNeedsInput != 30 {
		t.Errorf("sane values changed to %d/%d", d.PollIntervalSeconds, d.IdleSecondsForNeedsInput)
	}
}

// TestLoadConfig_LogsAnInvalidFile — a config.toml with a syntax error
// or a wrong type was ignored without a word, silently dropping the
// user's sleep mode, notifications, push settings and projects root.
// It is now logged, and the daemon starts from the defaults (not from
// whatever part of the file decoded before the error).
func TestLoadConfig_LogsAnInvalidFile(t *testing.T) {
	for name, body := range map[string]string{
		"syntax":     "[daemon\npoll_interval_seconds = 2\n",
		"wrong type": "[projects]\nroot = \"/custom\"\n[daemon]\npoll_interval_seconds = \"fast\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			path := filepath.Join(home, ".config", "ccmux", "config.toml")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			log.SetOutput(&buf)
			t.Cleanup(func() { log.SetOutput(os.Stderr) })

			cfg := loadConfig()
			if !strings.Contains(buf.String(), "config.toml") {
				t.Errorf("invalid config not logged (log: %q)", buf.String())
			}
			if cfg.Projects.Root != config.Defaults().Projects.Root {
				t.Errorf("projects root = %q, want the default after a failed load", cfg.Projects.Root)
			}
		})
	}
}
