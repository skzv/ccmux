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
	"github.com/skzv/ccmux/internal/agent"
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
	Sessions      SessionsConfig      `toml:"sessions"`
	Conversations ConversationsConfig `toml:"conversations"`
	Agents        AgentsConfig        `toml:"agents"`
	Update        UpdateConfig        `toml:"update"`
	Subscription  SubscriptionConfig  `toml:"subscription"`
	Tour          TourConfig          `toml:"tour"`
	Hosts         []Host              `toml:"host"`
	APNs          APNsConfig          `toml:"apns"`
	FCM           FCMConfig           `toml:"fcm"`
}

// APNsConfig configures Apple Push Notifications so the daemon can
// notify paired iPhones when sessions finish or need input. Off by
// default — flip Enabled=true and fill in KeyPath/KeyID/TeamID once
// the Apple Developer account has Push Notifications enabled for the
// iOS app's bundle id.
type APNsConfig struct {
	Enabled     bool   `toml:"enabled"`
	KeyPath     string `toml:"key_path"`    // path to AuthKey_XXXXXXXXXX.p8
	KeyID       string `toml:"key_id"`      // 10-char key id from Apple Developer
	TeamID      string `toml:"team_id"`     // 10-char team id
	Topic       string `toml:"topic"`       // iOS bundle id, e.g. "dev.skz.ccmux"
	Environment string `toml:"environment"` // optional override; usually omitted
}

// FCMConfig configures Firebase Cloud Messaging so the daemon can
// notify paired Android devices when sessions finish or need input.
// Parallel to APNsConfig — off by default; flip Enabled=true and
// fill in CredentialsPath + ProjectID once a Firebase service account
// JSON is available on disk.
type FCMConfig struct {
	Enabled         bool   `toml:"enabled"`
	CredentialsPath string `toml:"credentials_path"` // path to firebase service-account JSON
	ProjectID       string `toml:"project_id"`       // Firebase project id, e.g. "ccmux-mobile"
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

// ConversationsConfig controls the past-conversations list shown in
// the TUI and printed by `ccmux list-conversations`. The list pulls
// from every agent's on-disk transcripts (Claude / Codex / Antigravity),
// so it accumulates indefinitely; these knobs are how the user keeps
// it useful as automation noise piles up.
type ConversationsConfig struct {
	// ShowHeadless includes headless agent runs in the list. Default
	// false: hide them. The filter covers Claude `sdk-cli` runs
	// (`claude -p`, the SDK, automation wrappers) and Codex
	// `codex_exec` runs (`codex exec`); Antigravity rows have no
	// headless tag and are always shown. Headless transcripts
	// routinely dwarf interactive ones for users who wire agents into
	// scripts. Set true to see everything, or toggle live in the
	// Conversations screen with H. CLI mirror: `--include-headless`
	// on `ccmux list-conversations`.
	ShowHeadless bool `toml:"show_headless"`
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
	// values: "claude" / "codex" / "antigravity" / "cursor" (or the legacy
	// alias "gemini" for projects scaffolded before the rebrand),
	// or the explicit string "shell" for a bare $SHELL with no
	// agent. Empty falls back to "claude" so a fresh install gets
	// an agent by default — the multi-agent refactor's intent.
	Default string `toml:"default"`

	// Per-agent command selections. Nested under [agents.<id>] so
	// command pinning stays close to the agent it affects without
	// crowding the top-level agent defaults.
	Claude      AgentCommandConfig `toml:"claude"`
	Codex       AgentCommandConfig `toml:"codex"`
	Antigravity AgentCommandConfig `toml:"antigravity"`
	Cursor      AgentCommandConfig `toml:"cursor"`
}

// AgentCommandConfig stores an optional explicit executable path for
// an agent. Empty Command preserves the existing "resolve binary on
// PATH" behavior.
type AgentCommandConfig struct {
	Command string `toml:"command,omitempty"`
}

// AgentCommands converts config's persisted shape into the runtime
// command override shape used by internal/agent. Keeping the conversion
// here prevents launch sites from knowing the TOML layout.
func (c Config) AgentCommands() agent.Commands {
	return agent.Commands{
		Claude:      strings.TrimSpace(c.Agents.Claude.Command),
		Codex:       strings.TrimSpace(c.Agents.Codex.Command),
		Antigravity: strings.TrimSpace(c.Agents.Antigravity.Command),
		Cursor:      strings.TrimSpace(c.Agents.Cursor.Command),
	}
}

// UpdateConfig holds the auto-update preference. ccmux installs from a
// git checkout (`git clone` + `make install`), so "update" means
// pulling + rebuilding — see `ccmux update`.
type UpdateConfig struct {
	// AutoCheck, when true (the default), makes the TUI check on
	// launch whether the local checkout is behind its upstream and
	// surface a "update available" banner on the dashboard. It is a
	// CHECK-AND-NOTIFY toggle only: ccmux never pulls, rebuilds, or
	// restarts anything on its own — the user still runs `ccmux
	// update` when they're ready. The check is a background `git
	// fetch`, so a slow network or an offline machine just means no
	// banner, never a blocked startup.
	AutoCheck bool `toml:"auto_check"`
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

	// SSHUser is the username embedded in the ccmux:// pairing deep-link.
	// Defaults to $USER at runtime if empty.
	SSHUser string `toml:"ssh_user"`
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
	// Port is the ccmuxd HTTP listener port — what daemon.RemoteClient
	// dials to read /v1/sessions, /v1/projects, etc. Defaults to 7474
	// (the ccmuxd convention). NOT the SSH port — see SSHPort below.
	Port int `toml:"port"`
	// SSHPort is the port on which the remote's sshd is listening,
	// used by ssh/mosh and by the SSH setup wizard. Defaults to 22
	// when zero. Distinct from Port so a host running ccmuxd on
	// 7474 AND sshd on 2222 can express both — that's exactly the
	// case ccmux supports for users behind ISP-blocked port 22.
	SSHPort int  `toml:"ssh_port"`
	Mosh    bool `toml:"mosh"`
}

// EffectiveSSHPort returns the SSH port for this host, falling back
// to the openssh default of 22 when SSHPort is unset. Use this at
// every attach/probe/install site so the "default to 22" logic is
// in one place and a future global default change is one line.
func (h Host) EffectiveSSHPort() int {
	if h.SSHPort == 0 {
		return 22
	}
	return h.SSHPort
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
		Update: UpdateConfig{
			// Check-and-notify on by default — surfacing "an update
			// exists" is low-cost and low-risk (a background git
			// fetch). Nothing is rebuilt without the user running
			// `ccmux update`.
			AutoCheck: true,
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
