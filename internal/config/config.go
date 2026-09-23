// Package config loads and persists user configuration.
// Config lives at ~/.config/ccmux/config.toml.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/configfile"
)

// Config is the root user-configurable state.
type Config struct {
	Projects      ProjectsConfig      `toml:"projects"`
	Theme         string              `toml:"theme"`          // catppuccin-mocha (default), dracula, nord, gruvbox, tokyo-night
	Lang          string              `toml:"lang,omitempty"` // UI language code; empty = follow LC_ALL, then LANG
	Editor        string              `toml:"editor"`
	Sleep         SleepConfig         `toml:"sleep"`
	Daemon        DaemonConfig        `toml:"daemon"`
	Notes         NotesConfig         `toml:"notes"`
	Notifications NotificationsConfig `toml:"notifications"`
	Sessions      SessionsConfig      `toml:"sessions"`
	Conversations ConversationsConfig `toml:"conversations"`
	Agents        AgentsConfig        `toml:"agents"`
	Claude        ClaudeConfig        `toml:"claude"`
	Update        UpdateConfig        `toml:"update"`
	Subscription  SubscriptionConfig  `toml:"subscription"`
	Tour          TourConfig          `toml:"tour"`
	Setup         SetupConfig         `toml:"setup"`
	Hosts         []Host              `toml:"host"`
	APNs          APNsConfig          `toml:"apns"`
	FCM           FCMConfig           `toml:"fcm"`
	OpenRouter    OpenRouterConfig    `toml:"openrouter"`
}

// OpenRouterConfig wires ccmux to an OpenRouter account for two things:
// surfacing spend in the usage panel, and (with BaseURL injected into an
// agent's launch) routing OpenAI-compatible agents through OpenRouter's
// model catalog. Off by default — set Enabled + APIKey to turn on the
// spend panel; the key is the same inference key the agents use.
//
// The key lives in config rather than only an env var so the daemon
// (which has no inherited shell env on launchd/systemd) can read it for
// the usage refresh. Treat config.toml as secret-bearing when this is
// set — same as the APNs/FCM credentials above.
type OpenRouterConfig struct {
	Enabled bool   `toml:"enabled"`
	APIKey  string `toml:"api_key"`
	// BaseURL overrides the API root (default https://openrouter.ai/api/v1)
	// for a self-hosted gateway or a proxy. Also used as the value
	// injected into an OpenAI-compatible agent's OPENAI_BASE_URL when
	// the agent is launched with OpenRouter routing.
	BaseURL string `toml:"base_url,omitempty"`
	// RouteAgents lists the agent IDs (e.g. "codex", "opencode", "kilo")
	// ccmux should launch routed through OpenRouter — it injects
	// OPENAI_BASE_URL + OPENAI_API_KEY (the key via the shell's
	// OPENROUTER_API_KEY) into those agents' launch commands. Only
	// agents that honor the OpenAI-compatible env vars make sense here;
	// ccmux doesn't guess, so this is an explicit opt-in. Empty = no
	// routing (the default).
	RouteAgents []string `toml:"route_agents,omitempty"`
}

// APNsConfig configures Apple Push Notifications so the daemon can
// notify paired iPhones when sessions finish or need input. Off by
// default — flip Enabled=true and fill in KeyPath/KeyID/TeamID once
// the Apple Developer account has Push Notifications enabled for the
// mobile app's bundle id.
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
	// false: hide them. The filter covers Claude `sdk-*` runs
	// (`claude -p`, the Agent SDKs, automation wrappers), Codex
	// `codex_exec` runs (`codex exec`) and Codex subagent rollouts
	// (guardian reviews, spawned threads); Antigravity rows have no
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
	// values: "claude" / "codex" / "antigravity" / "cursor" / "pi"
	// / "gemini", or the explicit string "shell" for a bare $SHELL with
	// no agent. Empty falls back to "claude" so a fresh install gets
	// an agent by default — the multi-agent refactor's intent.
	Default string `toml:"default"`

	// Per-agent command selections. Nested under [agents.<id>] so
	// command pinning stays close to the agent it affects without
	// crowding the top-level agent defaults.
	Claude      AgentCommandConfig `toml:"claude"`
	Codex       AgentCommandConfig `toml:"codex"`
	Antigravity AgentCommandConfig `toml:"antigravity"`
	Cursor      AgentCommandConfig `toml:"cursor"`
	Pi          AgentCommandConfig `toml:"pi"`
	Grok        AgentCommandConfig `toml:"grok"`
	Muse        AgentCommandConfig `toml:"muse"`
	Gemini      AgentCommandConfig `toml:"gemini"`
}

// AgentCommandConfig stores an optional explicit executable path for
// an agent. Empty Command preserves the existing "resolve binary on
// PATH" behavior.
type AgentCommandConfig struct {
	Command string `toml:"command,omitempty"`
}

// ClaudeConfig holds Claude-specific runtime preferences distinct from
// Claude Code's own ~/.claude/settings.json. Today this is just the
// default model ccmux passes to claude sessions it launches — pin a
// concrete model (e.g. "claude-opus-4-8") or an alias ("opus") here
// without editing Claude Code's settings.
//
// Why a separate file: ccmux is a per-machine TUI; Claude Code's
// settings.json is global to the user (shared across machines on an
// NFS home dir, sshfs mount, etc). The ccmux default is per-machine
// and only applies to sessions ccmux itself launches — running
// `claude` directly outside ccmux keeps whatever settings.json says.
type ClaudeConfig struct {
	// DefaultModel is passed to `claude --model <value>` when ccmux
	// launches a new Claude session. Accepts a concrete vendor model
	// ID ("claude-opus-4-8"), a short alias ("opus" / "sonnet" /
	// "haiku" / "opusplan"), or "" to inherit Claude Code's own
	// default. Discoverable via the model picker (TUI) or
	// `ccmux agents models` (CLI). An explicit ANTHROPIC_MODEL env
	// var takes precedence — see internal/agent.
	DefaultModel string `toml:"default_model,omitempty"`
}

// AgentCommands converts config's persisted shape into the runtime
// command override shape used by internal/agent. Keeping the conversion
// here prevents launch sites from knowing the TOML layout.
func (c Config) AgentCommands() agent.Commands {
	return agent.Commands{
		Claude:            strings.TrimSpace(c.Agents.Claude.Command),
		Codex:             strings.TrimSpace(c.Agents.Codex.Command),
		Antigravity:       strings.TrimSpace(c.Agents.Antigravity.Command),
		Gemini:            strings.TrimSpace(c.Agents.Gemini.Command),
		Cursor:            strings.TrimSpace(c.Agents.Cursor.Command),
		Pi:                strings.TrimSpace(c.Agents.Pi.Command),
		Grok:              strings.TrimSpace(c.Agents.Grok.Command),
		Muse:              strings.TrimSpace(c.Agents.Muse.Command),
		ClaudeModel:       strings.TrimSpace(c.Claude.DefaultModel),
		OpenRouterAgents:  c.OpenRouter.routeAgentSet(),
		OpenRouterBaseURL: strings.TrimSpace(c.OpenRouter.BaseURL),
	}
}

// routeAgentSet parses RouteAgents into the ID set agent.Commands
// expects. Unknown / malformed IDs are dropped (ParseID rejects them)
// so a typo in config silently no-ops that entry rather than routing
// the wrong agent. Returns nil when nothing is configured.
func (o OpenRouterConfig) routeAgentSet() map[agent.ID]bool {
	if len(o.RouteAgents) == 0 {
		return nil
	}
	set := make(map[agent.ID]bool, len(o.RouteAgents))
	for _, s := range o.RouteAgents {
		if id, ok := agent.ParseID(s); ok {
			set[id] = true
		}
	}
	return set
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

// SetupConfig tracks first-run setup state, which drives the launch-time
// "ccmux isn't set up yet — run setup?" nudge.
type SetupConfig struct {
	// Completed is set true when `ccmux setup` finishes. While it's
	// false, `ccmux` offers to run setup on launch.
	Completed bool `toml:"completed"`
	// NudgeDismissed suppresses the launch nudge after the user declines
	// it once, so an unconfigured machine isn't nagged every launch. Once
	// Completed is true the nudge never fires regardless of this flag.
	NudgeDismissed bool `toml:"nudge_dismissed"`
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

	// IdleReleaseMinutes is reserved and currently unused: nothing
	// releases the keep-awake lock on idleness — the daemon holds it
	// whenever any session is active. The key stays parseable so config
	// files that set `idle_release_minutes` keep loading (and keep the
	// value across saves); it is not written when unset.
	IdleReleaseMinutes int `toml:"idle_release_minutes,omitzero"`

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

	// ExpandFolders — open the Notes folder tree fully expanded instead
	// of collapsed. Default false: folders start collapsed so a project
	// with a deep notes tree shows just the top-level headers; press
	// →/l to expand a folder, ←/h to collapse it. Override for a single
	// run with `ccmux --expand-notes`.
	ExpandFolders bool `toml:"expand_folders"`
}

// NotificationsConfig controls how the daemon signals needs_input
// transitions. The terminal BEL is the universal fallback — every iOS
// terminal client supports it — and macOS hosts can opt into named
// system sounds when terminal bells are muted. Power users with
// moshi-hook installed get richer pushes via that channel on top. The two
// channels are complementary (audible cue at your laptop + push on
// your phone), not duplicates: ccmux always plays a local sound when
// Bell=true regardless of whether moshi-hook is paired.
type NotificationsConfig struct {
	// Bell — play a local audible notification on needs_input transitions.
	// Default true. Set to false to mute completely (e.g. for a
	// silent office, or when you rely solely on phone pushes).
	Bell bool `toml:"bell"`

	// Sound selects the local audible notification backend. Empty and
	// "terminal" use terminal BEL. On macOS, a system sound name such
	// as "Ping", "Glass", or "Tink" plays that .aiff via afplay.
	Sound string `toml:"sound,omitempty"`
}

// SubscriptionConfig declares the user's subscription tier per agent
// so the dashboard can size the right quota bar (e.g. Claude's 5-hour
// message window) and so per-agent screens can read accurate
// entitlements. Set an entry to "" or "api" if you're on API /
// pay-as-you-go for that agent — the dashboard then shows raw token
// totals + estimated dollar cost without a quota bar for that agent.
//
// History: this struct started Claude-only (`tier = "max5x"`). To
// stay compatible with every existing config + tool that reads or
// writes `subscription.tier`, Tier IS the Claude entry — it's not
// duplicated in Tiers. Tiers carries only the *additional* per-agent
// entries (codex, antigravity, cursor, …). TierFor / SetTierFor
// route to the right field transparently so callers don't have to
// know the split.
type SubscriptionConfig struct {
	// Tier is the Claude subscription tier. Top-level TOML key for
	// back-compat — every config written before per-agent tiers
	// landed has `subscription.tier = "..."` here.
	Tier string `toml:"tier,omitempty"`

	// Tiers is the per-non-claude-agent tier map, keyed by canonical
	// agent ID ("codex", "antigravity", "cursor", …). Empty / missing
	// entries read as "api" (pay-as-you-go). The Claude tier
	// deliberately does NOT live here — it stays in Tier so older
	// binaries reading the file still find it.
	Tiers map[string]string `toml:"tiers,omitempty"`
}

// TierFor returns the tier for agentID. Claude reads from the
// top-level `tier` field for back-compat; every other agent reads
// from the `tiers` map. Returns "" when the agent has no recorded
// tier — callers should treat that as "api / unset".
func (s SubscriptionConfig) TierFor(agentID string) string {
	if agentID == "claude" {
		return s.Tier
	}
	if v, ok := s.Tiers[agentID]; ok {
		return v
	}
	return ""
}

// SetTierFor records the tier for agentID. Claude writes to the
// top-level `tier` field; every other agent writes to the `tiers`
// map (allocating it on first write).
func (s *SubscriptionConfig) SetTierFor(agentID, tier string) {
	if agentID == "claude" {
		s.Tier = tier
		return
	}
	if s.Tiers == nil {
		s.Tiers = map[string]string{}
	}
	s.Tiers[agentID] = tier
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
			Bell:  true,
			Sound: "terminal",
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
//
// The write is atomic (encode to memory, temp file + rename via
// configfile.WriteAtomic) so a crash mid-encode can never leave a
// truncated config.toml — the previous os.Create approach truncated in
// place before encoding a single byte. Mode is 0600, not 0644: the file
// carries secrets (API keys such as the OpenRouter key), so it must not
// be world-readable. The rename also replaces any pre-existing
// world-readable file, tightening old installs down to 0600 on the
// first Save.
//
// Keys this version of ccmux doesn't know about (written by a newer
// release, or hand-added) are carried over from the file being
// replaced rather than silently dropped. Comments are not preserved.
func Save(cfg Config) error {
	p, err := Path()
	if err != nil {
		return err
	}
	data, err := encode(cfg)
	if err != nil {
		return err
	}
	if prev, err := os.ReadFile(p); err == nil {
		if merged, ok := carryUnknownKeys(data, prev); ok {
			data = merged
		}
	}
	if err := configfile.WriteAtomic(p, data, 0o600); err != nil {
		return fmt.Errorf("write config %q: %w", p, err)
	}
	return nil
}

func encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	enc.Indent = "  "
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("encode config: %w", err)
	}
	return buf.Bytes(), nil
}

// carryUnknownKeys returns `encoded` (the new file body) with every key
// from `prev` that Config has no field for copied back in. ok=false
// means "nothing to carry" (or prev doesn't parse) and the caller keeps
// `encoded` as-is — so files with only known keys keep the struct's
// field order. Keys nested inside arrays of tables can't be addressed
// by path and are not carried.
func carryUnknownKeys(encoded, prev []byte) ([]byte, bool) {
	var probe Config
	md, err := toml.Decode(string(prev), &probe)
	if err != nil {
		return nil, false
	}
	undecoded := md.Undecoded()
	if len(undecoded) == 0 {
		return nil, false
	}
	var old, merged map[string]any
	if _, err := toml.Decode(string(prev), &old); err != nil {
		return nil, false
	}
	if _, err := toml.Decode(string(encoded), &merged); err != nil {
		return nil, false
	}
	carried := false
	for _, key := range undecoded {
		v, ok := lookupPath(old, key)
		if !ok {
			continue
		}
		if setPathIfAbsent(merged, key, v) {
			carried = true
		}
	}
	if !carried {
		return nil, false
	}
	out, err := encode(merged)
	if err != nil {
		return nil, false
	}
	return out, true
}

func lookupPath(m map[string]any, key toml.Key) (any, bool) {
	var cur any = m
	for _, k := range key {
		tbl, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = tbl[k]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// setPathIfAbsent sets key=v in m, creating intermediate tables, unless
// something already lives at key (e.g. a parent unknown table was
// carried whole on an earlier iteration). Reports whether it wrote.
func setPathIfAbsent(m map[string]any, key toml.Key, v any) bool {
	tbl := m
	for _, k := range key[:len(key)-1] {
		next, ok := tbl[k]
		if !ok {
			child := map[string]any{}
			tbl[k] = child
			tbl = child
			continue
		}
		if tbl, ok = next.(map[string]any); !ok {
			return false
		}
	}
	last := key[len(key)-1]
	if _, exists := tbl[last]; exists {
		return false
	}
	tbl[last] = v
	return true
}

// updateMu serializes Update calls within this process so two
// concurrent read-modify-write cycles can't interleave.
var updateMu sync.Mutex

// Update is the read-modify-write every in-app config change should use:
// it re-reads config.toml from disk, applies fn, and saves. Working from
// the on-disk state (instead of a long-lived in-memory copy) means one
// writer can't clobber another's earlier change with a stale snapshot.
//
// If the file exists but fails to load (e.g. a TOML syntax error from a
// hand edit), Update returns that error and writes nothing — saving
// would otherwise replace the user's whole file with defaults. An error
// from fn also aborts without writing. Returns the saved config.
func Update(fn func(*Config) error) (Config, error) {
	updateMu.Lock()
	defer updateMu.Unlock()
	cfg, err := Load()
	if err != nil {
		return cfg, err
	}
	if err := fn(&cfg); err != nil {
		return cfg, err
	}
	if err := Save(cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
