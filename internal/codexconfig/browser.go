package codexconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// HookGroup mirrors claudeconfig.HookGroup. Codex's hooks.json file
// uses the same shape Claude does (lifecycle name → array of groups
// each carrying an array of `{type,command,timeout?}` records), so
// the type and field names match deliberately to keep the Agents
// browser shared.
type HookGroup struct {
	Hooks []Hook `json:"hooks"`
}

// Hook is one runnable hook record. `type` is "command" in every
// Codex hook seen so far; we keep the field so an unfamiliar type
// surfaces in the preview without crashing.
type Hook struct {
	Type          string `json:"type"`
	Command       string `json:"command"`
	Timeout       int    `json:"timeout,omitempty"`
	StatusMessage string `json:"statusMessage,omitempty"`
}

// HooksFile is the top-level wrapper Codex writes around the per-
// lifecycle hook arrays.
type HooksFile struct {
	Hooks map[string][]HookGroup `json:"hooks"`
}

// ReadHooks loads ~/.codex/hooks.json. Returns an empty HooksFile (no
// error) when the file does not exist so an empty Codex install
// renders the empty-state placeholder instead of a parse error.
func ReadHooks() (HooksFile, error) {
	p, err := Paths()
	if err != nil {
		return HooksFile{}, err
	}
	return readHooksAt(filepath.Join(p.Root, "hooks.json"))
}

func readHooksAt(path string) (HooksFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return HooksFile{Hooks: map[string][]HookGroup{}}, nil
		}
		return HooksFile{}, err
	}
	var f HooksFile
	if err := json.Unmarshal(b, &f); err != nil {
		return HooksFile{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if f.Hooks == nil {
		f.Hooks = map[string][]HookGroup{}
	}
	return f, nil
}

// MCPServer is a single Codex MCP server entry. Codex's TOML schema
// (`[mcp_servers.<name>]`) carries the same fields Claude's JSON
// MCPServer carries, so the renderer can stay shared.
type MCPServer struct {
	Name    string
	Type    string // "stdio" (command-based) or "http"/"sse" (URL-based)
	Command string
	Args    []string
	Env     map[string]string
	URL     string
}

// ListMCPServers returns the configured MCP servers sorted by name.
// Servers are decoded from the `[mcp_servers.<name>]` blocks in
// config.toml. Type is inferred when omitted: a `url` field means
// http, a `command` field means stdio.
func ListMCPServers() ([]MCPServer, error) {
	p, err := Paths()
	if err != nil {
		return nil, err
	}
	return listMCPServersAt(p.Config)
}

func listMCPServersAt(path string) ([]MCPServer, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	raw := map[string]any{}
	if _, err := toml.Decode(string(b), &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	servers, _ := raw["mcp_servers"].(map[string]any)
	if servers == nil {
		return nil, nil
	}
	out := make([]MCPServer, 0, len(servers))
	for name, v := range servers {
		entry, _ := v.(map[string]any)
		if entry == nil {
			continue
		}
		s := MCPServer{Name: name}
		if t, ok := entry["type"].(string); ok {
			s.Type = t
		}
		if c, ok := entry["command"].(string); ok {
			s.Command = c
		}
		if u, ok := entry["url"].(string); ok {
			s.URL = u
		}
		if args, ok := entry["args"].([]any); ok {
			for _, a := range args {
				if str, ok := a.(string); ok {
					s.Args = append(s.Args, str)
				}
			}
		}
		if env, ok := entry["env"].(map[string]any); ok {
			s.Env = map[string]string{}
			for k, v := range env {
				if str, ok := v.(string); ok {
					s.Env[k] = str
				}
			}
		}
		if s.Type == "" {
			if s.URL != "" {
				s.Type = "http"
			} else if s.Command != "" {
				s.Type = "stdio"
			}
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Prompt is one user-defined slash command under ~/.codex/prompts/.
// Codex resolves `/<filename>` (without `.md`) to the prompt body at
// runtime; ccmux surfaces the same name + the file's markdown body.
type Prompt struct {
	Name string // file basename without `.md`
	Path string
	Body string
}

// ListPrompts walks ~/.codex/prompts/*.md and returns prompts sorted
// by name. Non-`.md` files are ignored.
func ListPrompts() ([]Prompt, error) {
	p, err := Paths()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(p.Root, "prompts")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := []Prompt{}
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".md") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		body, _ := os.ReadFile(full)
		out = append(out, Prompt{
			Name: strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())),
			Path: full,
			Body: string(body),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Rule is one Codex rule file under ~/.codex/rules/. Codex treats
// these as always-on prompt prefixes when matched. Body is the raw
// text — ccmux doesn't interpret rule directives, just renders.
type Rule struct {
	Name string // file basename including extension
	Path string
	Body string
}

// ListRules walks ~/.codex/rules/ and returns every regular file
// sorted by name. Extensions are not filtered (Codex accepts both
// `.rules` and `.md`), so the caller sees what's actually on disk.
func ListRules() ([]Rule, error) {
	p, err := Paths()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(p.Root, "rules")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := []Rule{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		full := filepath.Join(dir, e.Name())
		body, _ := os.ReadFile(full)
		out = append(out, Rule{Name: e.Name(), Path: full, Body: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
