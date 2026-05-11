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
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
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
	Model    string                 `json:"model,omitempty"`
	Theme    string                 `json:"theme,omitempty"`
	Hooks    map[string][]HookGroup `json:"hooks,omitempty"`
	MCPServers map[string]MCPServer `json:"mcpServers,omitempty"`
	Permissions Permissions         `json:"permissions,omitempty"`

	// Extra holds every key not modelled above so writes don't drop
	// them. populated by readWithExtras and merged back on write.
	Extra map[string]json.RawMessage `json:"-"`
}

// HookGroup is one entry in settings.hooks.<lifecycle>.
type HookGroup struct {
	Hooks []Hook `json:"hooks"`
	// Other fields (e.g. "matchers") preserved as raw.
	Extra map[string]json.RawMessage `json:"-"`
}

// Hook is one runnable hook record.
type Hook struct {
	Type    string `json:"type"`            // "command"
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
	Async   bool   `json:"async,omitempty"`
}

// MCPServer is one MCP server entry. Schema is whatever Claude Code
// recognizes — we store as RawMessage so additions/changes don't break
// us. Knowable fields can be parsed by callers as needed.
type MCPServer struct {
	Type    string            `json:"type,omitempty"` // "stdio", "http", "sse"
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Extra   map[string]json.RawMessage `json:"-"`
}

// Permissions is the allow/deny pair Claude Code uses to skip
// confirmation on listed tool patterns.
type Permissions struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

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
	for key, val := range raw {
		switch key {
		case "model":
			_ = json.Unmarshal(val, &s.Model)
		case "theme":
			_ = json.Unmarshal(val, &s.Theme)
		case "hooks":
			_ = json.Unmarshal(val, &s.Hooks)
		case "mcpServers":
			_ = json.Unmarshal(val, &s.MCPServers)
		case "permissions":
			_ = json.Unmarshal(val, &s.Permissions)
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
	// the user's editor will re-stable-sort on save anyway).
	out := map[string]any{}
	for k, v := range s.Extra {
		var any any
		_ = json.Unmarshal(v, &any)
		out[k] = any
	}
	if s.Model != "" {
		out["model"] = s.Model
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
	if len(s.Permissions.Allow) > 0 || len(s.Permissions.Deny) > 0 {
		out["permissions"] = s.Permissions
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return backup, err
	}
	if err := os.WriteFile(p.Settings, data, 0o644); err != nil {
		return backup, err
	}
	return backup, nil
}

// backupFile copies `src` to <backupDir>/<basename>.<unix-ms>. Idempotent
// on missing src (no-op, no backup file created).
func backupFile(src, backupDir string) (string, error) {
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return "", nil
	}
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", err
	}
	ts := time.Now().Format("20060102-150405")
	dst := filepath.Join(backupDir, filepath.Base(src)+"."+ts)
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return dst, err
	}
	return dst, nil
}

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
//   1. ANTHROPIC_MODEL env var
//   2. settings.json `model`
//   3. "(built-in default)"
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
func KnownModels() []ModelOption {
	return []ModelOption{
		{Alias: "opus", Label: "Opus 4.7 (claude-opus-4-7)", Desc: "Most capable; best for complex tasks"},
		{Alias: "sonnet", Label: "Sonnet 4.6 (claude-sonnet-4-6)", Desc: "Balanced cost/quality default"},
		{Alias: "haiku", Label: "Haiku 4.5 (claude-haiku-4-5)", Desc: "Fast, cheap; routine tasks"},
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

// Command is one ~/.claude/commands/*.md entry.
type Command struct {
	Name        string    // basename without .md
	Path        string    // absolute path
	Description string    // first non-empty line under the H1, when present
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
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		full := filepath.Join(p.CommandsDir, e.Name())
		info, _ := e.Info()
		out = append(out, Command{
			Name:        strings.TrimSuffix(e.Name(), ".md"),
			Path:        full,
			Description: firstDescriptionLine(full),
			Modified:    info.ModTime(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
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
