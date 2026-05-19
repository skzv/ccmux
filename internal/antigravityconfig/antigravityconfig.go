// Package antigravityconfig is the read/write layer for the Google
// Antigravity CLI config file at ~/.gemini/antigravity-cli/settings.json
// (the rebrand of the Gemini CLI's ~/.gemini/settings.json). Mirrors
// claudeconfig's shape (typed view + Extra map + always-backup-on-write);
// the file happens to be JSON like Claude's, which keeps this package
// short.
//
// Why we own a writer: ccmux exposes per-agent "YOLO" and
// reasoning-effort toggles that need to persist across sessions. The
// simplest path is to set the same fields the CLI reads at startup so a
// ccmux toggle is indistinguishable from the user editing the file.
//
// Note: as of agy 1.0.0 the persisted settings.json schema is small
// (colorScheme, enableTelemetry, trustedWorkspaces). The `yolo` and
// `reasoningEffort` fields below mirror what the predecessor Gemini CLI
// honored; whether agy 1.0.0 picks them up is its concern — we write
// the value and let the CLI decide, mirroring how claudeconfig handles
// effortLevel.
//
// Design principles (same trio as claudeconfig):
//   - Always back up before writing.
//     ~/.gemini/antigravity-cli/backups/<file>.<ts>.
//   - Preserve unknown keys. The settings shape evolves; the Extra
//     map carries everything we don't model through a round-trip.
//   - Never touch credentials. We only read/write settings.json.
package antigravityconfig

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Paths returns the canonical file locations the Antigravity CLI uses
// on this host. Honors $ANTIGRAVITY_HOME for tests / relocations,
// otherwise defaults to ~/.gemini/antigravity-cli.
func Paths() (Locations, error) {
	root := os.Getenv("ANTIGRAVITY_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Locations{}, err
		}
		root = filepath.Join(home, ".gemini", "antigravity-cli")
	}
	return Locations{
		Root:       root,
		Settings:   filepath.Join(root, "settings.json"),
		BackupsDir: filepath.Join(root, "backups"),
	}, nil
}

// Locations is the resolved set of paths.
type Locations struct {
	Root       string
	Settings   string
	BackupsDir string
}

// Settings is a typed view over the fields ccmux currently edits.
// Extra carries the rest. JSON tags match the keys the CLI's settings
// file is documented to use; whether a given version of agy honors
// `yolo` / `reasoningEffort` persistently is its concern — we write
// the value and let the CLI decide, mirroring how claudeconfig handles
// effortLevel.
type Settings struct {
	Model           string `json:"model,omitempty"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
	Yolo            bool   `json:"yolo,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// ReadSettings returns the typed Settings; unknown keys land in Extra.
// Missing file returns a zero Settings with no error.
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
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	s := &Settings{Extra: map[string]json.RawMessage{}}
	for key, val := range raw {
		switch key {
		case "model":
			_ = json.Unmarshal(val, &s.Model)
		case "reasoningEffort":
			_ = json.Unmarshal(val, &s.ReasoningEffort)
		case "yolo":
			_ = json.Unmarshal(val, &s.Yolo)
		default:
			s.Extra[key] = val
		}
	}
	return s, nil
}

// WriteSettings writes Settings back to disk, backing up the previous
// file first. Returns the path to the backup so the UI can offer
// rollback. Empty / zero-valued fields are omitted so we don't litter
// the file with explicit defaults the user didn't ask for.
func WriteSettings(s *Settings) (backup string, err error) {
	p, err := Paths()
	if err != nil {
		return "", err
	}
	if backup, err = backupFile(p.Settings, p.BackupsDir); err != nil {
		return "", err
	}
	out := map[string]any{}
	for k, v := range s.Extra {
		var any any
		_ = json.Unmarshal(v, &any)
		out[k] = any
	}
	if s.Model != "" {
		out["model"] = s.Model
	}
	if s.ReasoningEffort != "" {
		out["reasoningEffort"] = s.ReasoningEffort
	}
	if s.Yolo {
		out["yolo"] = true
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return backup, err
	}
	if err := os.MkdirAll(filepath.Dir(p.Settings), 0o755); err != nil {
		return backup, err
	}
	if err := os.WriteFile(p.Settings, data, 0o644); err != nil {
		return backup, err
	}
	return backup, nil
}

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

// SetEffortLevel updates only reasoningEffort. Pass "" to clear.
func SetEffortLevel(level string) (string, error) {
	s, err := ReadSettings()
	if err != nil {
		return "", err
	}
	s.ReasoningEffort = strings.TrimSpace(level)
	return WriteSettings(s)
}

// EffectiveEffortLevel mirrors claudeconfig.EffectiveEffortLevel.
func EffectiveEffortLevel() (value, source string) {
	s, err := ReadSettings()
	if err == nil && s.ReasoningEffort != "" {
		return s.ReasoningEffort, "settings.json"
	}
	return "(default)", "Antigravity CLI default"
}

// KnownEffortLevels are the levels we offer in the picker. The CLI
// uses thinking-budget terminology under the hood, but for parity with
// the other agents we expose the same low/medium/high vocabulary —
// the value we write is what gets persisted, regardless of how the
// CLI interprets it.
func KnownEffortLevels() []EffortOption {
	return []EffortOption{
		{Value: "high", Label: "high", Desc: "Deeper reasoning; slower responses"},
		{Value: "medium", Label: "medium", Desc: "Balanced"},
		{Value: "low", Label: "low", Desc: "Fast; minimal reasoning"},
		{Value: "", Label: "Inherit / no override", Desc: "Use whatever Antigravity CLI defaults to"},
	}
}

// EffortOption is one row in the effort picker.
type EffortOption struct {
	Value string
	Label string
	Desc  string
}

// SetYoloMode flips the persistent yolo flag. Disable only writes
// false → omits the key (omitempty), so we never litter the file with
// an explicit `false` the user didn't add.
func SetYoloMode(enabled bool) (string, error) {
	s, err := ReadSettings()
	if err != nil {
		return "", err
	}
	s.Yolo = enabled
	return WriteSettings(s)
}

// EffectiveYoloMode reports whether yolo is currently persisted.
func EffectiveYoloMode() (enabled bool, source string) {
	s, err := ReadSettings()
	if err == nil && s.Yolo {
		return true, "settings.json"
	}
	return false, "Antigravity CLI default"
}
