package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/tmux"
)

const macSystemSoundsDir = "/System/Library/Sounds"

func notificationBell(cfg config.NotificationsConfig) func(context.Context, string) error {
	sound := strings.TrimSpace(cfg.Sound)
	if sound == "" || strings.EqualFold(sound, "terminal") || strings.EqualFold(sound, "bell") {
		return tmux.RingBell
	}
	if runtime.GOOS != "darwin" {
		return tmux.RingBell
	}
	return func(ctx context.Context, name string) error {
		if err := playMacSound(sound); err != nil {
			return tmux.RingBell(ctx, name)
		}
		return nil
	}
}

// maxSoundDuration bounds a notification sound's player process.
const maxSoundDuration = 10 * time.Second

// soundCommand builds the player process; a seam for tests.
var soundCommand = func(ctx context.Context, path string) *exec.Cmd {
	return exec.CommandContext(ctx, "afplay", path)
}

// playMacSound starts the sound and returns without waiting for it.
//
// Deliberately not tied to the caller's context: the bell is rung from
// a poll tick whose context is cancelled the moment the tick returns,
// which killed afplay a few milliseconds in — custom sounds were silent
// or clipped. The player gets its own bounded lifetime instead.
func playMacSound(sound string) error {
	path, ok := resolveMacSoundPath(sound, macSystemSoundsDir, fileExists)
	if !ok {
		return fmt.Errorf("mac notification sound %q not found", sound)
	}
	return startDetached(path)
}

func startDetached(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), maxSoundDuration)
	cmd := soundCommand(ctx, path)
	if err := cmd.Start(); err != nil {
		cancel()
		return err
	}
	go func() {
		defer cancel()
		_ = cmd.Wait()
	}()
	return nil
}

func resolveMacSoundPath(sound, base string, exists func(string) bool) (string, bool) {
	sound = strings.TrimSpace(sound)
	if sound == "" {
		return "", false
	}
	candidates := []string{}
	if filepath.IsAbs(sound) || strings.ContainsRune(sound, filepath.Separator) {
		candidates = append(candidates, sound)
	} else {
		candidates = append(candidates,
			filepath.Join(base, sound),
			filepath.Join(base, sound+".aiff"),
			filepath.Join(base, titleSoundName(sound)+".aiff"),
		)
	}
	for _, candidate := range candidates {
		if exists(candidate) {
			return candidate, true
		}
	}
	return "", false
}

func titleSoundName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return ""
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
