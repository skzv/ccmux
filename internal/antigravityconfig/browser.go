package antigravityconfig

import (
	"encoding/json"
	"sort"
)

// MCPServer mirrors codexconfig.MCPServer (and the docs of the Gemini
// CLI's mcpServers key). Antigravity inherits the Gemini CLI's
// settings.json shape: `mcpServers: { name: {command, args, url, env}
// }`. ccmux reads the typed view here, leaves writes to the user
// (settings.json is hand-edited for now).
type MCPServer struct {
	Name    string
	Type    string // "stdio" or "http"/"sse"
	Command string
	Args    []string
	Env     map[string]string
	URL     string
}

// ListMCPServers returns the configured MCP servers sorted by name.
// Servers are decoded from the `mcpServers` field of settings.json.
// Type is inferred from URL/command when omitted.
func ListMCPServers() ([]MCPServer, error) {
	s, err := ReadSettings()
	if err != nil {
		return nil, err
	}
	raw, ok := s.Extra["mcpServers"]
	if !ok || len(raw) == 0 {
		return nil, nil
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, err
	}
	out := make([]MCPServer, 0, len(entries))
	for name, rawEntry := range entries {
		var entry struct {
			Type    string            `json:"type,omitempty"`
			Command string            `json:"command,omitempty"`
			Args    []string          `json:"args,omitempty"`
			Env     map[string]string `json:"env,omitempty"`
			URL     string            `json:"url,omitempty"`
		}
		if err := json.Unmarshal(rawEntry, &entry); err != nil {
			continue
		}
		srv := MCPServer{
			Name:    name,
			Type:    entry.Type,
			Command: entry.Command,
			Args:    entry.Args,
			Env:     entry.Env,
			URL:     entry.URL,
		}
		if srv.Type == "" {
			if srv.URL != "" {
				srv.Type = "http"
			} else if srv.Command != "" {
				srv.Type = "stdio"
			}
		}
		out = append(out, srv)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
