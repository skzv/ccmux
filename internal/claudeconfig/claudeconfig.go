// Package claudeconfig is the read/write layer for Claude Code's own
// configuration files under ~/.claude/. It powers the TUI's "Claude"
// screen — model picker, CLAUDE.md viewer, slash commands, MCP servers,
// hooks, permissions.
//
// Design principles:
//   - Always back up before writing. ~/.claude/backups/<file>.<timestamp>
//     gets a copy so accidental writes are reversible.
//   - Preserve unknown JSON fields. Claude Code adds new settings
//     between releases; we marshal through a map so anything we don't
//     model survives a round-trip.
//   - Never read auth secrets (api_key, credentials.*). We only touch
//     settings.json, CLAUDE.md, commands/, skills/, MCP-related fields.
package claudeconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/skzv/ccmux/internal/configfile"
)

// Paths returns the canonical file locations Claude Code uses on this
// host. Honors the $CLAUDE_CONFIG_DIR env var when set, otherwise
// defaults to ~/.claude.
func Paths() (Locations, error) {
	root := os.Getenv("CLAUDE_CONFIG_DIR")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Locations{}, err
		}
		root = filepath.Join(home, ".claude")
	}
	return Locations{
		Root:           root,
		Settings:       filepath.Join(root, "settings.json"),
		SettingsLocal:  filepath.Join(root, "settings.local.json"),
		GlobalCLAUDEMd: filepath.Join(root, "CLAUDE.md"),
		CommandsDir:    filepath.Join(root, "commands"),
		SkillsDir:      filepath.Join(root, "skills"),
		BackupsDir:     filepath.Join(root, "backups"),
	}, nil
}

// Locations is the resolved set of paths.
type Locations struct {
	Root           string
	Settings       string
	SettingsLocal  string
	GlobalCLAUDEMd string
	CommandsDir    string
	SkillsDir      string
	BackupsDir     string
}

// Settings is a typed view over the most-commonly-edited fields of
// ~/.claude/settings.json. The Extra map preserves everything else
// across a round-trip — important because Claude Code adds settings
// often and we don't want our writes to drop unknown keys.
type Settings struct {
	Model                 string                 `json:"model,omitempty"`
	EffortLevel           string                 `json:"effortLevel,omitempty"`
	AlwaysThinkingEnabled bool                   `json:"alwaysThinkingEnabled,omitempty"`
	Theme                 string                 `json:"theme,omitempty"`
	Hooks                 map[string][]HookGroup `json:"hooks,omitempty"`
	MCPServers            map[string]MCPServer   `json:"mcpServers,omitempty"`
	Permissions           Permissions            `json:"permissions,omitempty"`

	// Extra holds every key not modelled above so writes don't drop
	// them. populated by readWithExtras and merged back on write.
	Extra map[string]json.RawMessage `json:"-"`
}

// HookGroup is one entry in settings.hooks.<lifecycle>.
type HookGroup struct {
	Hooks []Hook `json:"hooks"`
	// Other fields (notably "matcher", which scopes a hook to specific
	// tools like Bash) are preserved verbatim through Extra. Without
	// the custom (Un)MarshalJSON below, the standard library would
	// silently drop them — turning a tool-scoped hook into an
	// all-tools hook on any ccmux settings write.
	Extra map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON splits the modelled `hooks` array from every other key
// (e.g. `matcher`), stashing the rest in Extra so they survive a
// write. Pointer receiver: encoding/json calls this even for slice /
// map elements (`[]HookGroup`, `map[string][]HookGroup`) because those
// elements are addressable.
func (h *HookGroup) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*h = HookGroup{Extra: map[string]json.RawMessage{}}
	for k, v := range raw {
		switch k {
		case "hooks":
			if err := json.Unmarshal(v, &h.Hooks); err != nil {
				return err
			}
		default:
			h.Extra[k] = v
		}
	}
	return nil
}

// MarshalJSON re-emits the modelled `hooks` array alongside every
// preserved Extra key. The known field is written last so it always
// wins over a stray same-named Extra entry. Extra values stay as
// json.RawMessage so large integers aren't mangled through float64.
func (h HookGroup) MarshalJSON() ([]byte, error) {
	out := make(map[string]any, len(h.Extra)+1)
	for k, v := range h.Extra {
		out[k] = v
	}
	// `hooks` has no omitempty in the original tag, so always emit it
	// (preserving the prior struct-marshal behavior, incl. null for nil).
	out["hooks"] = h.Hooks
	return json.Marshal(out)
}

// Hook is one runnable hook record. Unknown per-hook keys are preserved
// through Extra — same pattern as HookGroup/MCPServer — so a settings
// write can't strip fields Claude Code added after this struct was
// modelled.
type Hook struct {
	Type    string `json:"type"` // "command"
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
	Async   bool   `json:"async,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON routes the modelled fields and stashes the rest in
// Extra. Pointer receiver works for `[]Hook` elements because they are
// addressable.
func (h *Hook) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*h = Hook{Extra: map[string]json.RawMessage{}}
	for k, v := range raw {
		switch k {
		case "type":
			if err := json.Unmarshal(v, &h.Type); err != nil {
				return err
			}
		case "command":
			if err := json.Unmarshal(v, &h.Command); err != nil {
				return err
			}
		case "timeout":
			if err := json.Unmarshal(v, &h.Timeout); err != nil {
				return err
			}
		case "async":
			if err := json.Unmarshal(v, &h.Async); err != nil {
				return err
			}
		default:
			h.Extra[k] = v
		}
	}
	return nil
}

// MarshalJSON re-emits the modelled fields (honoring the original
// tags: type/command always, timeout/async omitempty) plus every
// preserved Extra key, verbatim.
func (h Hook) MarshalJSON() ([]byte, error) {
	out := make(map[string]any, len(h.Extra)+4)
	for k, v := range h.Extra {
		out[k] = v
	}
	out["type"] = h.Type
	out["command"] = h.Command
	if h.Timeout != 0 {
		out["timeout"] = h.Timeout
	}
	if h.Async {
		out["async"] = h.Async
	}
	return json.Marshal(out)
}

// MCPServer is one MCP server entry. Schema is whatever Claude Code
// recognizes — we model the common fields and preserve everything
// else (notably `headers`, which on http/sse servers commonly holds
// an Authorization bearer token, plus `disabled`, `timeout`, …)
// through Extra. Without the custom (Un)MarshalJSON below, any ccmux
// settings write would silently strip those fields and break the
// server's auth.
type MCPServer struct {
	Type    string                     `json:"type,omitempty"` // "stdio", "http", "sse"
	Command string                     `json:"command,omitempty"`
	Args    []string                   `json:"args,omitempty"`
	Env     map[string]string          `json:"env,omitempty"`
	URL     string                     `json:"url,omitempty"`
	Extra   map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON routes the modelled fields and stashes the rest in
// Extra. Pointer receiver works for `map[string]MCPServer` values
// because encoding/json unmarshals each into an addressable temp.
func (m *MCPServer) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*m = MCPServer{Extra: map[string]json.RawMessage{}}
	for k, v := range raw {
		switch k {
		case "type":
			if err := json.Unmarshal(v, &m.Type); err != nil {
				return err
			}
		case "command":
			if err := json.Unmarshal(v, &m.Command); err != nil {
				return err
			}
		case "args":
			if err := json.Unmarshal(v, &m.Args); err != nil {
				return err
			}
		case "env":
			if err := json.Unmarshal(v, &m.Env); err != nil {
				return err
			}
		case "url":
			if err := json.Unmarshal(v, &m.URL); err != nil {
				return err
			}
		default:
			m.Extra[k] = v
		}
	}
	return nil
}

// MarshalJSON re-emits the modelled fields (honoring the original
// omitempty semantics) plus every preserved Extra key. Extra values
// stay as json.RawMessage so numbers round-trip verbatim.
func (m MCPServer) MarshalJSON() ([]byte, error) {
	out := make(map[string]any, len(m.Extra)+5)
	for k, v := range m.Extra {
		out[k] = v
	}
	if m.Type != "" {
		out["type"] = m.Type
	}
	if m.Command != "" {
		out["command"] = m.Command
	}
	if len(m.Args) > 0 {
		out["args"] = m.Args
	}
	if len(m.Env) > 0 {
		out["env"] = m.Env
	}
	if m.URL != "" {
		out["url"] = m.URL
	}
	return json.Marshal(out)
}

// Permissions is the allow/deny pair Claude Code uses to skip
// confirmation on listed tool patterns. `defaultMode` controls how
// Claude treats tools that match neither list: "default" (prompt) is
// the safe default, "bypassPermissions" is the persistent "YOLO mode"
// equivalent of the CLI's `--dangerously-skip-permissions` flag.
type Permissions struct {
	Allow       []string `json:"allow,omitempty"`
	Deny        []string `json:"deny,omitempty"`
	DefaultMode string   `json:"defaultMode,omitempty"`
	// Extra preserves the permissions keys we don't model (`ask`,
	// `additionalDirectories`, `disableBypassPermissionsMode`, and
	// whatever Claude Code adds next) so a settings write can't
	// silently drop them. Same pattern as HookGroup/MCPServer.
	Extra map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON routes the modelled allow/deny/defaultMode fields and
// stashes every other key in Extra so it survives a write.
func (p *Permissions) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*p = Permissions{Extra: map[string]json.RawMessage{}}
	for k, v := range raw {
		switch k {
		case "allow":
			if err := json.Unmarshal(v, &p.Allow); err != nil {
				return err
			}
		case "deny":
			if err := json.Unmarshal(v, &p.Deny); err != nil {
				return err
			}
		case "defaultMode":
			if err := json.Unmarshal(v, &p.DefaultMode); err != nil {
				return err
			}
		default:
			p.Extra[k] = v
		}
	}
	return nil
}

// MarshalJSON re-emits the modelled fields (honoring the original
// omitempty semantics) plus every preserved Extra key. Extra values are
// kept as json.RawMessage so numbers round-trip verbatim.
func (p Permissions) MarshalJSON() ([]byte, error) {
	out := make(map[string]any, len(p.Extra)+3)
	for k, v := range p.Extra {
		out[k] = v
	}
	if len(p.Allow) > 0 {
		out["allow"] = p.Allow
	}
	if len(p.Deny) > 0 {
		out["deny"] = p.Deny
	}
	if p.DefaultMode != "" {
		out["defaultMode"] = p.DefaultMode
	}
	return json.Marshal(out)
}

// isZero reports whether writing this Permissions would produce an
// empty object — used by WriteSettings to decide whether to emit the
// `permissions` key at all.
func (p Permissions) isZero() bool {
	return len(p.Allow) == 0 && len(p.Deny) == 0 && p.DefaultMode == "" && len(p.Extra) == 0
}

// YoloModeValue is what we write into Permissions.DefaultMode to enable
// the persistent "yes to everything" mode. Centralized so the TUI,
// CLI, and tests all agree on the literal.
const YoloModeValue = "bypassPermissions"

// ReadSettings returns the typed Settings, with all unknown keys
// preserved in Extra. Missing file returns a zero Settings with no
// error.
func ReadSettings() (*Settings, error) {
	p, err := Paths()
	if err != nil {
		return nil, err
	}
	return readSettingsAt(p.Settings)
}

func readSettingsAt(path string) (*Settings, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Settings{Extra: map[string]json.RawMessage{}}, nil
		}
		return nil, err
	}
	// Round-trip through a map to separate known from unknown fields.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	var s Settings
	s.Extra = map[string]json.RawMessage{}
	// A known key that fails to unmarshal into its typed field (Claude
	// Code changed the schema out from under us) is preserved verbatim
	// in Extra instead of being dropped — otherwise the whole section
	// would silently vanish on the next write, breaking this package's
	// preservation contract. The typed field is reset to zero so the
	// write path emits only the raw Extra copy, never a half-parsed
	// struct on top of it.
	for key, val := range raw {
		switch key {
		case "model":
			if json.Unmarshal(val, &s.Model) != nil {
				s.Model = ""
				s.Extra[key] = val
			}
		case "effortLevel":
			if json.Unmarshal(val, &s.EffortLevel) != nil {
				s.EffortLevel = ""
				s.Extra[key] = val
			}
		case "alwaysThinkingEnabled":
			if json.Unmarshal(val, &s.AlwaysThinkingEnabled) != nil {
				s.AlwaysThinkingEnabled = false
				s.Extra[key] = val
			}
		case "theme":
			if json.Unmarshal(val, &s.Theme) != nil {
				s.Theme = ""
				s.Extra[key] = val
			}
		case "hooks":
			if json.Unmarshal(val, &s.Hooks) != nil {
				s.Hooks = nil
				s.Extra[key] = val
			}
		case "mcpServers":
			if json.Unmarshal(val, &s.MCPServers) != nil {
				s.MCPServers = nil
				s.Extra[key] = val
			}
		case "permissions":
			if json.Unmarshal(val, &s.Permissions) != nil {
				s.Permissions = Permissions{}
				s.Extra[key] = val
			}
		default:
			s.Extra[key] = val
		}
	}
	return &s, nil
}

// WriteSettings writes the typed Settings back to disk, backing up the
// previous file first. Returns the path to the backup so the UI can
// surface a rollback action.
func WriteSettings(s *Settings) (backup string, err error) {
	p, err := Paths()
	if err != nil {
		return "", err
	}
	if backup, err = backupFile(p.Settings, p.BackupsDir); err != nil {
		return "", err
	}
	// Re-marshal: merge typed fields + Extra map into one ordered JSON
	// object (best-effort; encoding/json doesn't preserve order, but
	// the user's editor will re-stable-sort on save anyway). Extra
	// values are kept as json.RawMessage — re-emitted verbatim — so a
	// large integer like 1698765432109876543 doesn't round-trip through
	// float64 and come back mangled.
	out := map[string]any{}
	for k, v := range s.Extra {
		out[k] = v
	}
	if s.Model != "" {
		out["model"] = s.Model
	}
	if s.EffortLevel != "" {
		out["effortLevel"] = s.EffortLevel
	}
	if s.AlwaysThinkingEnabled {
		out["alwaysThinkingEnabled"] = true
	}
	if s.Theme != "" {
		out["theme"] = s.Theme
	}
	if len(s.Hooks) > 0 {
		out["hooks"] = s.Hooks
	}
	if len(s.MCPServers) > 0 {
		out["mcpServers"] = s.MCPServers
	}
	// isZero (not just the modelled fields) so a permissions object
	// holding ONLY unmodelled keys still survives the write.
	if !s.Permissions.isZero() {
		out["permissions"] = s.Permissions
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return backup, err
	}
	// Write-then-rename so a crash mid-write can't truncate settings.json
	// and lose the round-trip-preserved Extra fields.
	if err := configfile.WriteAtomic(p.Settings, data, 0o644); err != nil {
		return backup, err
	}
	return backup, nil
}

// backupFile is a thin wrapper around the shared configfile.Backup
// helper. Kept so the rest of this package doesn't need to import
// configfile directly.
func backupFile(src, backupDir string) (string, error) {
	return configfile.Backup(src, backupDir)
}

// maxBackupsPerFile is re-exported so tests in this package can pin
// the cap against the shared helper.
const maxBackupsPerFile = configfile.MaxBackupsPerFile

// SetModel updates only the model field, preserving everything else.
// Returns the backup path. `model` may be a vendor model ID
// ("claude-opus-4-7"), a short alias ("opus", "sonnet", "haiku",
// "opusplan"), or "" to clear the override.
func SetModel(model string) (string, error) {
	s, err := ReadSettings()
	if err != nil {
		return "", err
	}
	s.Model = strings.TrimSpace(model)
	return WriteSettings(s)
}

// EffectiveModel returns what model Claude Code will actually use,
// applying the documented precedence:
//  1. ANTHROPIC_MODEL env var
//  2. settings.json `model`
//  3. "(built-in default)"
func EffectiveModel() (value, source string) {
	if v := strings.TrimSpace(os.Getenv("ANTHROPIC_MODEL")); v != "" {
		return v, "$ANTHROPIC_MODEL"
	}
	s, err := ReadSettings()
	if err == nil && s.Model != "" {
		return s.Model, "settings.json"
	}
	return "(default)", "Claude Code default"
}

// KnownModels are the headline aliases the model picker offers.
// Custom IDs can still be set via the typed-input fallback.
// Labels are version-agnostic on purpose — the aliases auto-track
// Anthropic's current binding (opus → whatever the latest Opus is), so
// pinning a version number in the label here just goes stale. The
// model picker shows specific versioned IDs (claude-opus-4-8, …) from
// the live catalog separately; these alias rows are the "always latest"
// option.
func KnownModels() []ModelOption {
	return []ModelOption{
		{Alias: "opus", Label: "opus", Desc: "Latest Opus — most capable; complex tasks"},
		{Alias: "sonnet", Label: "sonnet", Desc: "Latest Sonnet — balanced cost/quality"},
		{Alias: "haiku", Label: "haiku", Desc: "Latest Haiku — fast, cheap; routine tasks"},
		{Alias: "opusplan", Label: "opusplan", Desc: "Pro/Max plan auto-mix (Opus + Sonnet)"},
		{Alias: "", Label: "Inherit / no override", Desc: "Use whatever Claude Code defaults to"},
	}
}

// ModelOption is one row in the picker.
type ModelOption struct {
	Alias string // value written to settings.json
	Label string
	Desc  string
}

// SetEffortLevel updates only the effortLevel field, preserving everything
// else. Returns the backup path. `level` may be "low", "medium", "high",
// "xhigh", "max", or "" to clear the override. Claude Code's docs describe
// "max" as primarily a CLI-flag value (`claude --effort max`); whether it
// persists when written to settings.json depends on the installed Claude
// Code version, so we write whatever the caller asks for and let Claude
// Code decide.
func SetEffortLevel(level string) (string, error) {
	s, err := ReadSettings()
	if err != nil {
		return "", err
	}
	s.EffortLevel = strings.TrimSpace(level)
	return WriteSettings(s)
}

// SetAlwaysThinking toggles the alwaysThinkingEnabled boolean. When false,
// the key is omitted from settings.json entirely so we don't litter the
// file with explicit `false`s the user didn't ask for.
func SetAlwaysThinking(enabled bool) (string, error) {
	s, err := ReadSettings()
	if err != nil {
		return "", err
	}
	s.AlwaysThinkingEnabled = enabled
	return WriteSettings(s)
}

// EffectiveEffortLevel returns the persisted reasoning-effort default and
// where it came from. There's no documented env-var override, so the
// precedence is just settings.json → Claude Code default. The CLI's
// `--effort` flag is a per-invocation override and not visible here.
func EffectiveEffortLevel() (value, source string) {
	s, err := ReadSettings()
	if err == nil && s.EffortLevel != "" {
		return s.EffortLevel, "settings.json"
	}
	return "(default)", "Claude Code default"
}

// KnownEffortLevels are the rows the effort picker offers. The order
// is intentionally high-to-low so "max effort" is the first option a
// user lands on when they open the picker.
func KnownEffortLevels() []EffortOption {
	return []EffortOption{
		{Value: "max", Label: "max", Desc: "Maximum effort (verify persistence with your Claude Code version)"},
		{Value: "xhigh", Label: "xhigh", Desc: "Very high reasoning budget"},
		{Value: "high", Label: "high", Desc: "Deeper reasoning; slower responses"},
		{Value: "medium", Label: "medium", Desc: "Balanced (Claude Code default)"},
		{Value: "low", Label: "low", Desc: "Fast; minimal reasoning"},
		{Value: "", Label: "Inherit / no override", Desc: "Use whatever Claude Code defaults to"},
	}
}

// EffortOption is one row in the effort picker.
type EffortOption struct {
	Value string // value written to settings.json (empty clears)
	Label string
	Desc  string
}

// SetYoloMode flips the persistent "skip permission prompts" toggle by
// writing permissions.defaultMode = "bypassPermissions" (when enabled)
// or clearing it (when disabled). Other fields in Permissions are left
// alone. Returns the backup path.
//
// Why a setting rather than a per-invocation flag: the CLI's
// `--dangerously-skip-permissions` only covers that one launch; users
// who want it on every time end up redefining their shell alias.
// Persisting through settings.json means ccmux's "yolo" toggle survives
// reattach and matches Claude Code's documented permanent option.
func SetYoloMode(enabled bool) (string, error) {
	s, err := ReadSettings()
	if err != nil {
		return "", err
	}
	if enabled {
		s.Permissions.DefaultMode = YoloModeValue
	} else if s.Permissions.DefaultMode == YoloModeValue {
		// Only clear the field if it was actually our value; preserve
		// any other defaultMode (e.g. "acceptEdits") the user set by
		// hand.
		s.Permissions.DefaultMode = ""
	}
	return WriteSettings(s)
}

// EffectiveYoloMode returns whether YOLO mode is currently persisted in
// settings.json. Source string mirrors the other Effective* helpers and
// is shown in the TUI's status line so users can see where the value
// came from. Note that the CLI's `--dangerously-skip-permissions` flag
// is a per-invocation override and not visible here.
func EffectiveYoloMode() (enabled bool, source string) {
	s, err := ReadSettings()
	if err != nil {
		return false, "Claude Code default"
	}
	if s.Permissions.DefaultMode == YoloModeValue {
		return true, "settings.json"
	}
	return false, "Claude Code default"
}

// Command is one ~/.claude/commands/*.md entry.
type Command struct {
	Name        string // basename without .md
	Path        string // absolute path
	Description string // first non-empty line under the H1, when present
	Modified    time.Time
}

// ListCommands returns the user's installed slash-command aliases,
// alphabetically.
func ListCommands() ([]Command, error) {
	p, err := Paths()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(p.CommandsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Command
	for _, e := range entries {
		if c, ok := commandFromEntry(p.CommandsDir, e); ok {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// commandFromEntry converts one commands/ directory entry into a
// Command. ok=false for subdirectories, non-.md files, and entries
// whose file vanished between ReadDir and Info — that race used to
// dereference a nil FileInfo and panic.
func commandFromEntry(dir string, e fs.DirEntry) (Command, bool) {
	if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
		return Command{}, false
	}
	info, err := e.Info()
	if err != nil || info == nil {
		return Command{}, false
	}
	full := filepath.Join(dir, e.Name())
	return Command{
		Name:        strings.TrimSuffix(e.Name(), ".md"),
		Path:        full,
		Description: firstDescriptionLine(full),
		Modified:    info.ModTime(),
	}, true
}

// firstDescriptionLine peeks at a command markdown file and returns
// the first meaningful description line (the first non-blank line that
// isn't the H1 title). Returns "" on any read error.
func firstDescriptionLine(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	skipHeader := true
	for _, line := range strings.Split(string(b), "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" {
			continue
		}
		if skipHeader && strings.HasPrefix(trim, "#") {
			skipHeader = false
			continue
		}
		if len(trim) > 100 {
			trim = trim[:100] + "…"
		}
		return trim
	}
	return ""
}

// Skill is one ~/.claude/skills/<name>/SKILL.md entry.
type Skill struct {
	Name        string
	Path        string // path to SKILL.md
	Description string
}

// ListSkills returns installed skills.
func ListSkills() ([]Skill, error) {
	p, err := Paths()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(p.SkillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillFile := filepath.Join(p.SkillsDir, e.Name(), "SKILL.md")
		if _, err := os.Stat(skillFile); err != nil {
			continue
		}
		out = append(out, Skill{
			Name:        e.Name(),
			Path:        skillFile,
			Description: firstDescriptionLine(skillFile),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Run is a tiny wrapper around exec for callers that want the Claude
// CLI's view of effective config (currently unused — kept as a hook
// for the future when /status becomes available non-interactively).
func Run(ctx context.Context, args ...string) (string, error) {
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(c, "claude", args...)
	out, err := cmd.Output()
	return string(out), err
}
