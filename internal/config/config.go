// Package config loads and persists user configuration.
// Config lives at ~/.config/ccmux/config.toml.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config is the root user-configurable state.
type Config struct {
	Projects      ProjectsConfig      `toml:"projects"`
	Theme         string              `toml:"theme"` // catppuccin-mocha (default), dracula, nord, gruvbox, tokyo-night
	Editor        string              `toml:"editor"`
	Sleep         SleepConfig         `toml:"sleep"`
	Daemon        DaemonConfig        `toml:"daemon"`
	Notes         NotesConfig         `toml:"notes"`
	Notifications NotificationsConfig `toml:"notifications"`
	Scaffold      ScaffoldConfig      `toml:"scaffold"`
	Sessions      SessionsConfig      `toml:"sessions"`
	Agents        AgentsConfig        `toml:"agents"`
	Subscription  SubscriptionConfig  `toml:"subscription"`
	Tour          TourConfig          `toml:"tour"`
	Hosts         []Host              `toml:"host"`
}

// SessionsConfig holds preferences for the Sessions screen's "new
// session" flow. A bare session is one started from the Sessions
// tab — not tied to any project — that opens DefaultDir and runs
// the default agent (or a plain shell). Useful for ad-hoc work on
// any device (e.g. a quick session on the Mac mini without first
// scaffolding a "project").
//
// The default-agent choice lives on AgentsConfig, not here — it
// applies equally to Projects' new-project form and to Sessions'
// new-bare-session form, so keeping it global is the right level.
type SessionsConfig struct {
	// DefaultDir is the working directory new bare sessions open
	// in. Empty resolves to $HOME on the daemon host. The TUI's
	// new-session form pre-fills this value; the user can override
	// per-session.
	DefaultDir string `toml:"default_dir"`

	// AttachMode controls what happens to OTHER clients when ccmux
	// attaches to a session:
	//
	//   "mirror"    (default) — `tmux attach` without -d. Other
	//               clients stay attached; the session is mirrored
	//               across every device viewing it. Paired with
	//               tmux's `window-size latest` so the window tracks
	//               whichever client is most recently active rather
	//               than shrinking to the smallest one.
	//   "exclusive" — `tmux attach -d`. Attaching detaches every
	//               other client. The window always resizes cleanly
	//               to the attaching terminal. This was ccmux's
	//               behavior before mirror mode existed.
	//
	// Empty is treated as "mirror" (the default), so a config file
	// that predates this field gets the new behavior.
	AttachMode string `toml:"attach_mode"`
}

// DetachOthersOnAttach reports whether an attach should kick other
// clients off the session — true only in "exclusive" mode. Centralized
// so every attach call site (TUI local, TUI remote-ssh, CLI) reads the
// mode through one predicate instead of string-comparing inline.
func (s SessionsConfig) DetachOthersOnAttach() bool {
	return strings.EqualFold(strings.TrimSpace(s.AttachMode), "exclusive")
}

// AgentsConfig holds the cross-app default-agent preference and
// (eventually) per-agent toggles. The motivating use case: a user
// who works primarily in Codex shouldn't have to flip the agent
// picker every time they create a project — set the default once,
// new projects pick it up.
//
// Lives in its own section so future per-agent settings have a
// natural home without crowding Sessions / Projects.
type AgentsConfig struct {
	// Default picks which agent the new-project and new-bare-session
	// forms default to, and which agent the daemon launches when
	// `ccmux shell` / POST /v1/sessions/bare omits the field. Valid
	// values: "claude" / "codex" / "antigravity" (or the legacy
	// alias "gemini" for projects scaffolded before the rebrand),
	// or the explicit string "shell" for a bare $SHELL with no
	// agent. Empty falls back to "claude" so a fresh install gets
	// an agent by default — the multi-agent refactor's intent.
	Default string `toml:"default"`
}

// TourConfig persists whether the user has seen the first-run interactive
// tour. We only set Shown=true after the user explicitly completes or
// skips the tour, so a partial view (window resize, accidental quit)
// re-opens the tour next launch. ShownVersion lets us re-trigger the
// tour after a major version that introduces new screens.
type TourConfig struct {
	Shown        bool   `toml:"shown"`
	ShownVersion string `toml:"shown_version"`
}

type ProjectsConfig struct {
	Root string `toml:"root"` // ~/Projects by default
}

type SleepConfig struct {
	// Mode picks the sleep-prevention aggressiveness:
	//   - "safe" (default) — caffeinate -s on macOS (AC-only by Apple's
	//     own policy; lid-close still puts the laptop to sleep) /
	//     systemd-inhibit on Linux. Safe for batteries.
	//   - "dangerous" — caffeinate -d -i -m -s on macOS so it also
	//     prevents idle sleep on battery. A battery monitor downgrades
	//     to "safe" when the charge crosses LowBatteryCutoff so a
	//     forgotten-on-battery laptop doesn't run itself flat.
	//   - "very_dangerous" — dangerous + `sudo pmset -a disablesleep 1`
	//     (macOS) / mask sleep.target (Linux) so lid-close no longer
	//     puts the system to sleep. Requires passwordless sudo for
	//     pmset/systemctl; reverts on daemon exit.
	Mode string `toml:"mode"`

	// IdleReleaseMinutes — release the keep-awake lock when all sessions
	// have been idle for this long. Default 10.
	IdleReleaseMinutes int `toml:"idle_release_minutes"`

	// DangerousKeepAwakeOnBattery — back-compat flag. If true and Mode
	// is empty, Mode resolves to "dangerous". Prefer setting Mode
	// directly.
	DangerousKeepAwakeOnBattery bool `toml:"dangerous_keep_awake_on_battery"`

	// LowBatteryCutoff — in dangerous mode, auto-downgrade to safe when
	// on battery below this percentage. Default 20.
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

// NotificationsConfig controls how the daemon signals needs_input
// transitions. The audible BEL is the universal fallback — every iOS
// terminal client supports it — and power users with moshi-hook
// installed get richer pushes via that channel on top. The two
// channels are complementary (audible cue at your laptop + push on
// your phone), not duplicates: ccmux always rings the bell when
// Bell=true regardless of whether moshi-hook is paired.
type NotificationsConfig struct {
	// Bell — ring the terminal BEL on needs_input transitions.
	// Default true. Set to false to mute completely (e.g. for a
	// silent office, or when you rely solely on phone pushes).
	Bell bool `toml:"bell"`
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
			Mode:                        "safe",
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
		Notifications: NotificationsConfig{
			Bell: true,
		},
		Scaffold: ScaffoldConfig{
			// All scaffold fields default to "" / nil; internal/scaffold
			// falls back to its baked-in templates when these are empty.
			CreateInitialCommit: true,
		},
		Sessions: SessionsConfig{
			// Empty = resolve to $HOME on the daemon host at session-
			// creation time. We don't bake the resolved $HOME in here
			// because the daemon may live on a different machine
			// (cross-device "new bare session") with a different home.
			DefaultDir: "",
			// Mirror by default: attaching from a second device keeps
			// the first one attached, so the same session can be
			// watched from laptop + phone at once. Users who want the
			// old "attaching kicks everyone else" behavior set this to
			// "exclusive".
			AttachMode: "mirror",
		},
		Agents: AgentsConfig{
			// Default to claude so a fresh install lands new sessions
			// inside an agent. Users who want the old no-agent shell
			// behaviour set this to "shell"; codex / antigravity users
			// override via Settings or the setup wizard.
			Default: "claude",
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
