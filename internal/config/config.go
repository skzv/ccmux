// Package config loads and persists user configuration.
// Config lives at ~/.config/ccmux/config.toml.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config is the root user-configurable state.
type Config struct {
	Projects ProjectsConfig `toml:"projects"`
	Theme    string         `toml:"theme"` // catppuccin-mocha (default), dracula, nord, gruvbox, tokyo-night
	Editor   string         `toml:"editor"`
	Sleep    SleepConfig    `toml:"sleep"`
	Daemon   DaemonConfig   `toml:"daemon"`
	Notes    NotesConfig    `toml:"notes"`
	Hosts    []Host         `toml:"host"`
}

type ProjectsConfig struct {
	Root string `toml:"root"` // ~/Projects by default
}

type SleepConfig struct {
	// IdleReleaseMinutes — release the keep-awake lock when all sessions
	// have been idle for this long. Default 10.
	IdleReleaseMinutes int `toml:"idle_release_minutes"`

	// DangerousKeepAwakeOnBattery — Mode 2. Use caffeinate -d -i -m -u to
	// keep the system awake on battery too. Default false.
	DangerousKeepAwakeOnBattery bool `toml:"dangerous_keep_awake_on_battery"`

	// LowBatteryCutoff — in Mode 2, auto-release the lock when on battery
	// and below this percentage. Default 20.
	LowBatteryCutoff int `toml:"low_battery_cutoff"`
}

type DaemonConfig struct {
	// PollIntervalSeconds — how often to scrape tmux state. Default 2.
	PollIntervalSeconds int `toml:"poll_interval_seconds"`

	// IdleSecondsForNeedsInput — pane must be idle this long before we
	// transition to NEEDS_INPUT and ring the bell. Default 3.
	IdleSecondsForNeedsInput int `toml:"idle_seconds_for_needs_input"`

	// ListenTailnet — enable HTTP API on the Tailscale interface so other
	// devices can list/attach this host's sessions. Default false.
	ListenTailnet bool `toml:"listen_tailnet"`

	// TailnetPort — port for the tailnet HTTP listener. Default 7474.
	TailnetPort int `toml:"tailnet_port"`
}

type NotesConfig struct {
	// AutoLogSessions — append a session-start line to today's Agent Log
	// when a Claude session starts via ccmux. Default true.
	AutoLogSessions bool `toml:"auto_log_sessions"`
}

// Host is a remote ccmuxd the local TUI knows how to connect to.
type Host struct {
	Name    string `toml:"name"`
	Address string `toml:"address"`
	User    string `toml:"user"`
	Port    int    `toml:"port"`
	Mosh    bool   `toml:"mosh"`
}

// Defaults returns a Config populated with default values.
func Defaults() Config {
	home, _ := os.UserHomeDir()
	return Config{
		Projects: ProjectsConfig{Root: filepath.Join(home, "Projects")},
		Theme:    "catppuccin-mocha",
		Editor:   firstNonEmpty(os.Getenv("VISUAL"), os.Getenv("EDITOR"), "nvim"),
		Sleep: SleepConfig{
			IdleReleaseMinutes:          10,
			DangerousKeepAwakeOnBattery: false,
			LowBatteryCutoff:            20,
		},
		Daemon: DaemonConfig{
			PollIntervalSeconds:      2,
			IdleSecondsForNeedsInput: 3,
			ListenTailnet:            false,
			TailnetPort:              7474,
		},
		Notes: NotesConfig{
			AutoLogSessions: true,
		},
	}
}

// Path returns the canonical config-file path: ~/.config/ccmux/config.toml.
func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "ccmux", "config.toml"), nil
}

// Load reads the config file, applying defaults for any missing fields.
// A missing file is not an error — defaults are returned.
func Load() (Config, error) {
	cfg := Defaults()
	p, err := Path()
	if err != nil {
		return cfg, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("read config %q: %w", p, err)
	}
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return cfg, fmt.Errorf("parse config %q: %w", p, err)
	}
	return cfg, nil
}

// Save writes the config file, creating parent directories as needed.
func Save(cfg Config) error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.Create(p)
	if err != nil {
		return fmt.Errorf("create config %q: %w", p, err)
	}
	defer f.Close()
	enc := toml.NewEncoder(f)
	enc.Indent = "  "
	return enc.Encode(cfg)
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
