package tui

import (
	"strings"

	"github.com/skzv/ccmux/internal/project"
)

// Project info can render before the host-health refresh arrives.
func projectDisplayHost(p project.Project) string {
	if p.HostKey != "" {
		return p.Host + " (" + strings.TrimPrefix(p.HostKey, "tailnet://") + ")"
	}
	return projectHost(p)
}

// Discovered keys cannot collide with configured host names, which do
// not contain colons or slashes, or with the reserved local identity.
func discoveredHostKey(addr string) string { return "tailnet://" + addr }

func (h hostStatus) key() string {
	if h.Local {
		return "local"
	}
	if h.Key != "" {
		return h.Key
	}
	return h.Name
}

// hostLabel resolves a routing key for messages and device pickers.
// Keep the address visible when names collide so a confirmation names
// the actual machine the user selected.
func hostLabel(key string, hosts []hostStatus) string {
	for _, h := range hosts {
		if h.key() != key {
			continue
		}
		if h.Local {
			return "local"
		}
		if h.Name == "local" {
			return h.Name + " (" + h.Address + ")"
		}
		for _, other := range hosts {
			if other.Name == h.Name && other.key() != key {
				return h.Name + " (" + h.Address + ")"
			}
		}
		return h.Name
	}
	return strings.TrimPrefix(key, "tailnet://")
}
