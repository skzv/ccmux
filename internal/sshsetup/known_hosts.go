package sshsetup

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RemoveKnownHostEntries removes every line in ~/.ssh/known_hosts
// whose host pattern matches `host` (optionally `[host]:port` for
// non-default ports). Returns the number of lines removed.
//
// This is the Go-native equivalent of `ssh-keygen -R <host>` — used
// by the wizard's host-key-mismatch recovery flow so the user can
// resolve a key conflict without dropping to a shell.
//
// We do NOT touch hashed-hostname entries (lines beginning with
// `|1|`). openssh hashes hostnames when HashKnownHosts is enabled;
// reversing the hash requires the plaintext host AND a per-entry
// salt, which is more code than is worth here. ssh-keygen -R is the
// fallback for those. Practically, ccmux's own appendKnownHost
// writes plain entries, so this covers our own writes 100%.
func RemoveKnownHostEntries(host string, port int) (removed int, err error) {
	if strings.TrimSpace(host) == "" {
		return 0, errors.New("sshsetup: host is required")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return 0, err
	}
	path := filepath.Join(home, ".ssh", "known_hosts")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil // nothing to remove from a missing file
		}
		return 0, err
	}
	patterns := candidatePatterns(host, port)
	var kept bytes.Buffer
	scanner := bufio.NewScanner(bytes.NewReader(data))
	// Big buffer — known_hosts entries can be > 64 KiB when full of
	// long base64 chunks; default scan budget would split them.
	scanner.Buffer(make([]byte, 0, 1<<16), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if shouldDropLine(line, patterns) {
			removed++
			continue
		}
		kept.WriteString(line)
		kept.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	if removed == 0 {
		return 0, nil
	}
	// Write through a temp file + rename so a crash mid-write
	// doesn't leave the user without any known_hosts at all.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, kept.Bytes(), 0o644); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	return removed, nil
}

// candidatePatterns returns the EXACT host string a known_hosts
// entry for `host:port` should carry. openssh writes the bracketed
// form `[host]:port` for non-22 ports and the bare `host` for
// port 22 — the two patterns don't overlap, so a port=2222 caller
// must only delete the `[host]:2222` entry and leave a separate
// `host` entry (which is for port 22) untouched.
func candidatePatterns(host string, port int) []string {
	host = strings.TrimSpace(host)
	if port == 0 || port == 22 {
		return []string{host}
	}
	return []string{fmt.Sprintf("[%s]:%d", host, port)}
}

// shouldDropLine reports whether a known_hosts line matches any of
// our patterns. A line's first whitespace-separated field is a
// comma-separated list of host patterns; we drop the line if ANY
// pattern matches one of our candidates. Comments and blank lines
// are kept as-is.
func shouldDropLine(line string, patterns []string) bool {
	t := strings.TrimSpace(line)
	if t == "" || strings.HasPrefix(t, "#") {
		return false
	}
	// Hashed entries (`|1|salt|hash`) are intentionally untouched —
	// we can't tell what host they match without the salt + plain
	// hostname, and ccmux's writes never produce them.
	if strings.HasPrefix(t, "|1|") {
		return false
	}
	fields := strings.Fields(t)
	if len(fields) < 2 {
		return false
	}
	for _, pat := range strings.Split(fields[0], ",") {
		pat = strings.TrimSpace(pat)
		for _, want := range patterns {
			if pat == want {
				return true
			}
		}
	}
	return false
}
