package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DeviceRegistration is one mobile client's APNs binding: the SSH
// public key it paired with, plus the most recent APNs device token
// and environment. The daemon's APNs sender walks all registrations
// on a needs-input / completion event and pushes to each.
type DeviceRegistration struct {
	PublicKeyHash string    `json:"public_key_hash"`
	Token         string    `json:"token"`
	Environment   string    `json:"environment"` // "development" | "production"
	UpdatedAt     time.Time `json:"updated_at"`
}

// DeviceStore is a tiny JSON-backed registry of mobile clients. One
// entry per paired public key — newer tokens overwrite older ones for
// the same key, since iOS treats the token as the durable address
// even when it rolls.
type DeviceStore struct {
	mu   sync.Mutex
	path string
	byID map[string]DeviceRegistration
}

// DefaultDeviceStorePath returns ~/.local/state/ccmux/devices.json,
// next to the daemon's socket. Mirrors the path convention the rest
// of the daemon uses for per-host state.
func DefaultDeviceStorePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "ccmux", "devices.json"), nil
}

// OpenDeviceStore loads (or creates) the store at path.
func OpenDeviceStore(path string) (*DeviceStore, error) {
	s := &DeviceStore{path: path, byID: map[string]DeviceRegistration{}}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		var list []DeviceRegistration
		if json.Unmarshal(raw, &list) == nil {
			for _, r := range list {
				s.byID[r.PublicKeyHash] = r
			}
		}
	case errors.Is(err, os.ErrNotExist):
		// fresh store
	default:
		return nil, err
	}
	return s, nil
}

// Register adds or refreshes one device's APNs token. Empty token or
// env is rejected so a malformed mobile request can't corrupt the
// store with junk that then gets pushed to APNs.
func (s *DeviceStore) Register(publicKey, token, env string) error {
	if strings.TrimSpace(publicKey) == "" {
		return errors.New("devicestore: public key required")
	}
	if strings.TrimSpace(token) == "" {
		return errors.New("devicestore: token required")
	}
	if env != "development" && env != "production" {
		return fmt.Errorf("devicestore: env must be development|production, got %q", env)
	}
	hash := HashPublicKey(publicKey)
	s.mu.Lock()
	s.byID[hash] = DeviceRegistration{
		PublicKeyHash: hash,
		Token:         token,
		Environment:   env,
		UpdatedAt:     time.Now(),
	}
	s.mu.Unlock()
	return s.flush()
}

// All returns a snapshot of every registration. Cheap copy — the
// daemon's APNs path iterates this on each event, so we avoid
// exposing the internal map.
func (s *DeviceStore) All() []DeviceRegistration {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]DeviceRegistration, 0, len(s.byID))
	for _, r := range s.byID {
		out = append(out, r)
	}
	return out
}

// Lookup returns the registration for a specific public key, if any.
// Used by the test-push endpoint to target just the requesting device.
func (s *DeviceStore) Lookup(publicKey string) (DeviceRegistration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.byID[HashPublicKey(publicKey)]
	return r, ok
}

// Remove drops a registration — used when a phone unpairs or when
// APNs returns a "device no longer reachable" error.
func (s *DeviceStore) Remove(publicKey string) error {
	hash := HashPublicKey(publicKey)
	s.mu.Lock()
	delete(s.byID, hash)
	s.mu.Unlock()
	return s.flush()
}

func (s *DeviceStore) flush() error {
	s.mu.Lock()
	list := make([]DeviceRegistration, 0, len(s.byID))
	for _, r := range s.byID {
		list = append(list, r)
	}
	s.mu.Unlock()
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	// Write-then-rename so a crash mid-write can't truncate the file.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// HashPublicKey turns an SSH authorized_keys-style public key into a
// short stable identifier. The full key isn't stored alongside the
// APNs token — only its hash — so a leak of devices.json doesn't
// also leak the keypair.
func HashPublicKey(publicKey string) string {
	// Normalize: trim trailing comments / whitespace and take the
	// base64 body, so semantically-equal keys hash identically even
	// if the optional comment differs across pairings.
	fields := strings.Fields(strings.TrimSpace(publicKey))
	body := publicKey
	if len(fields) >= 2 {
		body = fields[0] + " " + fields[1]
	}
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}
