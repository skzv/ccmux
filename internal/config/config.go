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
	Projects     ProjectsConfig     `toml:"projects"`
	Theme        string             `toml:"theme"` // catppuccin-mocha (default), dracula, nord, gruvbox, tokyo-night
	Editor       string             `toml:"editor"`
	Sleep        SleepConfig        `toml:"sleep"`
	Daemon       DaemonConfig       `toml:"daemon"`
	Notes        NotesConfig        `toml:"notes"`
	Scaffold     ScaffoldConfig     `toml:"scaffold"`
	Subscription SubscriptionConfig `toml:"subscription"`
	Hosts        []Host             `toml:"host"`
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

// ScaffoldConfig customizes what a "new project" creates and what Claude
// hears as its first message. Empty fields fall back to the baked-in
// defaults in internal/scaffold, so the user can override one piece
// without redefining the whole thing.
type ScaffoldConfig struct {
	// Dirs is the relative directory tree created in every new project.
	// Example: ["src", "tests", "docs/01_Specs", "docs/02_Architecture",
	// "docs/03_Agent_Logs"].
	Dirs []string `toml:"dirs"`

	// GitignoreBody is written verbatim into .gitignore when scaffolding
	// a new project (only when .gitignore doesn't already exist).
	GitignoreBody string `toml:"gitignore_body"`

	// InitialPrompt is the message ccmux sends to Claude after the
	// session boots. Supports {{name}} and {{description}} substitutions.
	// Multi-line OK. The default is in scaffold.DefaultInitialPrompt.
	InitialPrompt string `toml:"initial_prompt"`

	// CreateInitialCommit — run `git init && git add . && git commit`
	// after scaffolding. Default true.
	CreateInitialCommit bool `toml:"create_initial_commit"`
}

// SubscriptionConfig declares which Claude subscription tier the user is
// on so the dashboard can show "X of Y messages used in the 5h window."
// Set to "" or "api" if you're on API/pay-as-you-go rather than a
// subscription — the dashboard then shows raw token totals + estimated
// dollar cost without a quota bar.
type SubscriptionConfig struct {
	// Tier: "api" | "pro" | "max5x" | "max20x". Default "api".
	Tier string `toml:"tier"`
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
		Scaffold: ScaffoldConfig{
			// All scaffold fields default to "" / nil; internal/scaffold
			// falls back to its baked-in templates when these are empty.
			CreateInitialCommit: true,
		},
		Subscription: SubscriptionConfig{Tier: "api"},
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
