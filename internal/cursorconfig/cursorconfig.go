// Package cursorconfig is the read-only layer for Cursor's CLI config
// files under ~/.cursor/. ccmux does not write to Cursor's config —
// Cursor owns the schema and its CLI/IDE round-trip it — but we read:
//
//   - hooks.json: Cursor's hook event registry (afterFileEdit,
//     beforeShellExecution, stop, …).
//   - skills-cursor/<name>/SKILL.md: user-defined skills (the same
//     SKILL.md frontmatter Claude's `~/.claude/skills/` uses).
//
// MCP servers in Cursor are per-project (`<project>/.cursor/mcp.json`)
// rather than global, so they are not surfaced here.
package cursorconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Paths returns the canonical file locations Cursor uses on this host.
// Honors $CURSOR_HOME for tests / relocations, otherwise defaults to
// ~/.cursor.
func Paths() (Locations, error) {
	root := os.Getenv("CURSOR_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Locations{}, err
		}
		root = filepath.Join(home, ".cursor")
	}
	return Locations{
		Root:      root,
		Hooks:     filepath.Join(root, "hooks.json"),
		SkillsDir: filepath.Join(root, "skills-cursor"),
	}, nil
}

// Locations is the resolved set of paths.
type Locations struct {
	Root      string
	Hooks     string
	SkillsDir string
}

// HookGroup is one entry under a hook event in Cursor's hooks.json.
// The schema differs from Claude/Codex (event names are camelCase, no
// `timeout` field), but the per-record shape (type+command) is the
// same so the renderer can stay shared.
type HookGroup struct {
	Hooks []Hook `json:"hooks"`
}

// Hook is one runnable hook record. Cursor's hook records carry only
// a `command` field today; we keep `Type` for forward compatibility.
type Hook struct {
	Type    string `json:"type,omitempty"`
	Command string `json:"command"`
}

// HooksFile is the top-level wrapper. Cursor wraps the array-form
// (one record per event) so we re-pack to []HookGroup to match the
// shape Claude / Codex use, easing shared rendering.
type HooksFile struct {
	Hooks   map[string][]HookGroup
	Version int
}

// ReadHooks loads ~/.cursor/hooks.json. Returns an empty HooksFile
// (no error) when the file does not exist.
func ReadHooks() (HooksFile, error) {
	p, err := Paths()
	if err != nil {
		return HooksFile{}, err
	}
	return readHooksAt(p.Hooks)
}

func readHooksAt(path string) (HooksFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return HooksFile{Hooks: map[string][]HookGroup{}}, nil
		}
		return HooksFile{}, err
	}
	var raw struct {
		Hooks   map[string][]Hook `json:"hooks"`
		Version int               `json:"version"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return HooksFile{}, fmt.Errorf("parse %s: %w", path, err)
	}
	out := HooksFile{
		Hooks:   map[string][]HookGroup{},
		Version: raw.Version,
	}
	for event, hooks := range raw.Hooks {
		// Cursor stores one hook per group rather than Claude's array-
		// of-groups-of-hooks; re-pack so the consumer sees the same
		// HookGroup shape.
		groups := make([]HookGroup, 0, len(hooks))
		for _, h := range hooks {
			groups = append(groups, HookGroup{Hooks: []Hook{h}})
		}
		out.Hooks[event] = groups
	}
	return out, nil
}

// Skill is one Cursor skill under ~/.cursor/skills-cursor/<name>/SKILL.md.
// Mirrors claudeconfig.Skill so the Agents browser renderer stays
// shared across agents.
type Skill struct {
	Name        string
	Description string
	Path        string
	Body        string
}

// ListSkills walks ~/.cursor/skills-cursor/ and parses each child
// directory's SKILL.md for the name + description frontmatter. Result
// is sorted by name. Directories without a SKILL.md are skipped.
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
	out := []Skill{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		full := filepath.Join(p.SkillsDir, e.Name(), "SKILL.md")
		body, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		s := Skill{Name: e.Name(), Path: full, Body: string(body)}
		s.Description = extractFrontmatterDescription(string(body))
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// extractFrontmatterDescription pulls the `description:` field out of
// a SKILL.md frontmatter block. The block uses standard YAML-ish
// `---` fences; description may be inline (`description: ...`) or
// folded (`description: >-\n  multi-line`). We collect everything
// until the next top-level key or the closing fence and collapse
// whitespace.
func extractFrontmatterDescription(body string) string {
	const fence = "---"
	body = strings.TrimSpace(body)
	if !strings.HasPrefix(body, fence) {
		return ""
	}
	rest := strings.TrimPrefix(body, fence)
	rest = strings.TrimLeft(rest, "\r\n")
	end := strings.Index(rest, "\n"+fence)
	if end < 0 {
		return ""
	}
	block := rest[:end]
	lines := strings.Split(block, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "description:") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(trimmed, "description:"))
		if value != "" && value != ">-" && value != ">" && value != "|" {
			return value
		}
		// Folded block — collect indented lines until the next top-key.
		var parts []string
		for _, follow := range lines[i+1:] {
			if follow == "" {
				continue
			}
			if !strings.HasPrefix(follow, " ") && !strings.HasPrefix(follow, "\t") {
				break
			}
			parts = append(parts, strings.TrimSpace(follow))
		}
		return strings.Join(parts, " ")
	}
	return ""
}
