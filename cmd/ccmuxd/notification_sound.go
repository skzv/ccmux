package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

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
		if err := playMacSound(ctx, sound); err != nil {
			return tmux.RingBell(ctx, name)
		}
		return nil
	}
}

func playMacSound(ctx context.Context, sound string) error {
	path, ok := resolveMacSoundPath(sound, macSystemSoundsDir, fileExists)
	if !ok {
		return fmt.Errorf("mac notification sound %q not found", sound)
	}
	cmd := exec.CommandContext(ctx, "afplay", path)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
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
