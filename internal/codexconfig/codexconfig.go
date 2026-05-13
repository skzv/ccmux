// Package codexconfig is the read/write layer for the OpenAI Codex CLI
// config file at ~/.codex/config.toml. Mirrors claudeconfig's design
// (typed view + Extra map + always-backup-on-write) but the on-disk
// format is TOML rather than JSON.
//
// Why we own a config writer at all: ccmux exposes per-agent toggles —
// the "YOLO" approval/sandbox shortcut and the reasoning-effort picker
// — that need to survive between sessions. The simple path is to write
// the same fields Codex itself reads, so a ccmux toggle is
// indistinguishable from the user editing the file by hand.
//
// Design principles (same trio as claudeconfig):
//   - Always back up before writing. ~/.codex/backups/<file>.<timestamp>.
//   - Preserve unknown keys. Codex grows new keys between releases; we
//     decode into a map[string]any and merge known typed fields back
//     in so anything we don't model survives a round-trip.
//   - Never touch credentials. We only read/write config.toml. Tokens
//     live in auth.json which we leave alone.
package codexconfig

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Paths returns the canonical file locations Codex uses on this host.
// Honors $CODEX_HOME (the env var Codex itself respects), otherwise
// defaults to ~/.codex.
func Paths() (Locations, error) {
	root := os.Getenv("CODEX_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Locations{}, err
		}
		root = filepath.Join(home, ".codex")
	}
	return Locations{
		Root:       root,
		Config:     filepath.Join(root, "config.toml"),
		BackupsDir: filepath.Join(root, "backups"),
	}, nil
}

// Locations is the resolved set of paths Codex cares about for the
// surface ccmux edits.
type Locations struct {
	Root       string
	Config     string
	BackupsDir string
}

// Settings is a typed view over the fields ccmux currently edits in
// Codex's config.toml. Extra holds every other top-level key so writes
// don't drop them. Codex itself defines many more keys (profiles, model
// providers, MCP servers, etc.); we don't model them but we don't lose
// them either.
type Settings struct {
	Model                string
	ModelReasoningEffort string // "minimal" | "low" | "medium" | "high"
	ApprovalPolicy       string // "untrusted" | "on-failure" | "on-request" | "never"
	SandboxMode          string // "read-only" | "workspace-write" | "danger-full-access"

	// Extra preserves everything else. Values are the raw decoded
	// shape (map[string]any, []any, primitives) so re-encoding stays
	// faithful to a fresh decode.
	Extra map[string]any
}

// YoloApprovalPolicy and YoloSandboxMode are the values Codex itself
// documents as the most-permissive combo. We write both together when
// YOLO is on so the user actually gets "no prompts + full access" the
// way the upstream `--full-auto` shortcut does.
const (
	YoloApprovalPolicy = "never"
	YoloSandboxMode    = "danger-full-access"
)

// ReadSettings returns the typed Settings, with unknown keys preserved
// in Extra. Missing file returns a zero Settings with no error.
func ReadSettings() (*Settings, error) {
	p, err := Paths()
	if err != nil {
		return nil, err
	}
	return readSettingsAt(p.Config)
}

func readSettingsAt(path string) (*Settings, error) {
	raw := map[string]any{}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Settings{Extra: map[string]any{}}, nil
		}
		return nil, err
	}
	if _, err := toml.Decode(string(b), &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	s := &Settings{Extra: map[string]any{}}
	for key, val := range raw {
		switch key {
		case "model":
			s.Model, _ = val.(string)
		case "model_reasoning_effort":
			s.ModelReasoningEffort, _ = val.(string)
		case "approval_policy":
			s.ApprovalPolicy, _ = val.(string)
		case "sandbox_mode":
			s.SandboxMode, _ = val.(string)
		default:
			s.Extra[key] = val
		}
	}
	return s, nil
}

// WriteSettings writes Settings back to disk, backing up the previous
// file first. Returns the path to the backup so the UI can offer a
// rollback action. Empty string-valued fields are omitted from the
// output so we don't litter the file with explicit empty values the
// user didn't ask for.
func WriteSettings(s *Settings) (backup string, err error) {
	p, err := Paths()
	if err != nil {
		return "", err
	}
	if backup, err = backupFile(p.Config, p.BackupsDir); err != nil {
		return "", err
	}
	out := map[string]any{}
	for k, v := range s.Extra {
		out[k] = v
	}
	if s.Model != "" {
		out["model"] = s.Model
	}
	if s.ModelReasoningEffort != "" {
		out["model_reasoning_effort"] = s.ModelReasoningEffort
	}
	if s.ApprovalPolicy != "" {
		out["approval_policy"] = s.ApprovalPolicy
	}
	if s.SandboxMode != "" {
		out["sandbox_mode"] = s.SandboxMode
	}
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	enc.Indent = "  "
	if err := enc.Encode(out); err != nil {
		return backup, err
	}
	if err := os.MkdirAll(filepath.Dir(p.Config), 0o755); err != nil {
		return backup, err
	}
	if err := os.WriteFile(p.Config, buf.Bytes(), 0o644); err != nil {
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

// SetEffortLevel updates only model_reasoning_effort. Pass "" to clear
// the override.
func SetEffortLevel(level string) (string, error) {
	s, err := ReadSettings()
	if err != nil {
		return "", err
	}
	s.ModelReasoningEffort = strings.TrimSpace(level)
	return WriteSettings(s)
}

// EffectiveEffortLevel mirrors claudeconfig.EffectiveEffortLevel: returns
// the persisted reasoning-effort default and where it came from.
func EffectiveEffortLevel() (value, source string) {
	s, err := ReadSettings()
	if err == nil && s.ModelReasoningEffort != "" {
		return s.ModelReasoningEffort, "config.toml"
	}
	return "(default)", "Codex default"
}

// KnownEffortLevels lists the reasoning-effort values Codex documents.
// Ordered high-to-low so the picker lands users on "high" first.
func KnownEffortLevels() []EffortOption {
	return []EffortOption{
		{Value: "high", Label: "high", Desc: "Deeper reasoning; slower responses"},
		{Value: "medium", Label: "medium", Desc: "Balanced (Codex default)"},
		{Value: "low", Label: "low", Desc: "Fast; minimal reasoning"},
		{Value: "minimal", Label: "minimal", Desc: "Fastest; almost no reasoning"},
		{Value: "", Label: "Inherit / no override", Desc: "Use whatever Codex defaults to"},
	}
}

// EffortOption is one row in the effort picker.
type EffortOption struct {
	Value string
	Label string
	Desc  string
}

// SetYoloMode flips Codex's persistent "yes to everything" combo. When
// enabled, sets approval_policy = "never" AND sandbox_mode =
// "danger-full-access" — the same pair Codex's documented `--full-auto`
// alias toggles per-invocation. When disabled, we only clear the two
// fields if they currently hold our YOLO values; any other policy the
// user set by hand is preserved.
func SetYoloMode(enabled bool) (string, error) {
	s, err := ReadSettings()
	if err != nil {
		return "", err
	}
	if enabled {
		s.ApprovalPolicy = YoloApprovalPolicy
		s.SandboxMode = YoloSandboxMode
	} else {
		if s.ApprovalPolicy == YoloApprovalPolicy {
			s.ApprovalPolicy = ""
		}
		if s.SandboxMode == YoloSandboxMode {
			s.SandboxMode = ""
		}
	}
	return WriteSettings(s)
}

// EffectiveYoloMode reports whether the YOLO combo is fully persisted
// (both approval_policy and sandbox_mode set to their YOLO values).
// "Partially configured" (one set, the other not) reads as off so the
// TUI doesn't lie about the safety posture.
func EffectiveYoloMode() (enabled bool, source string) {
	s, err := ReadSettings()
	if err != nil {
		return false, "Codex default"
	}
	if s.ApprovalPolicy == YoloApprovalPolicy && s.SandboxMode == YoloSandboxMode {
		return true, "config.toml"
	}
	return false, "Codex default"
}
