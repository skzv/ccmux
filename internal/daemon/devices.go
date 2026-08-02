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

// Provider identifies which push gateway a registered device token
// belongs to. The empty/legacy value defaults to APNs when read so
// older devices.json files written before this field existed migrate
// transparently.
const (
	ProviderAPNs = "apns"
	ProviderFCM  = "fcm"
)

// DeviceRegistration is one mobile client's push binding: the SSH
// public key it paired with, plus the most recent device token,
// environment (APNs only), and which provider routes the push. The
// daemon's dispatcher walks all registrations on a needs-input /
// completion event and pushes through the matching gateway.
type DeviceRegistration struct {
	PublicKeyHash string    `json:"public_key_hash"`
	Token         string    `json:"token"`
	Provider      string    `json:"provider,omitempty"` // "apns" | "fcm"; empty reads as "apns"
	Environment   string    `json:"environment"`        // "development" | "production" (APNs only)
	UpdatedAt     time.Time `json:"updated_at"`
}

// ResolvedProvider returns Provider with the empty/legacy value mapped
// to ProviderAPNs so callers can switch on a non-empty value without
// special-casing migrated records.
func (r DeviceRegistration) ResolvedProvider() string {
	if r.Provider == "" {
		return ProviderAPNs
	}
	return r.Provider
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
		if uerr := json.Unmarshal(raw, &list); uerr != nil {
			// The file exists but doesn't parse (truncated, hand-edited,
			// version skew). DO NOT silently fall through to an empty
			// in-memory store: the first Register/Remove flush would
			// os.Rename a fresh file over this one and permanently
			// destroy the device bindings that might still be
			// recoverable. Move the bad file aside first so it's
			// preserved, then start fresh. If we can't even move it,
			// fail loudly rather than risk clobbering it — every device
			// path already guards a nil store, so push just stays off.
			bak := path + ".corrupt"
			if rerr := os.Rename(path, bak); rerr != nil {
				return nil, fmt.Errorf("device store %s is corrupt (%v) and could not be set aside: %w", path, uerr, rerr)
			}
			// s.byID is already the empty map — return the fresh store.
			return s, nil
		}
		for _, r := range list {
			s.byID[r.PublicKeyHash] = r
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
// store with junk that then gets pushed to APNs. Kept for legacy
// iOS callers that predate the multi-provider device store;
// internally delegates to RegisterWithProvider.
func (s *DeviceStore) Register(publicKey, token, env string) error {
	return s.RegisterWithProvider(publicKey, token, ProviderAPNs, env)
}

// RegisterWithProvider adds or refreshes one device's push binding,
// recording which gateway (APNs / FCM) is responsible. APNs records
// require Environment to be set to development|production; FCM
// records ignore environment but require a non-empty token.
func (s *DeviceStore) RegisterWithProvider(publicKey, token, provider, env string) error {
	if strings.TrimSpace(publicKey) == "" {
		return errors.New("devicestore: public key required")
	}
	if strings.TrimSpace(token) == "" {
		return errors.New("devicestore: token required")
	}
	if provider == "" {
		provider = ProviderAPNs
	}
	switch provider {
	case ProviderAPNs:
		if env != "development" && env != "production" {
			return fmt.Errorf("devicestore: env must be development|production for apns, got %q", env)
		}
	case ProviderFCM:
		// FCM has no analogue to the APNs sandbox/production split —
		// the same token works in any environment. Reject any non-
		// empty env so a misconfigured client can't accidentally pin
		// itself to a value the dispatcher would have to ignore later.
		if env != "" {
			return fmt.Errorf("devicestore: env must be empty for fcm, got %q", env)
		}
	default:
		return fmt.Errorf("devicestore: unknown provider %q", provider)
	}
	hash := HashPublicKey(publicKey)
	s.mu.Lock()
	s.byID[hash] = DeviceRegistration{
		PublicKeyHash: hash,
		Token:         token,
		Provider:      provider,
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
	// Use os.CreateTemp for a unique suffix per call — two concurrent
	// Registers racing on a single ".tmp" name would otherwise hit
	// "rename: no such file or directory" when one finished before the
	// other's WriteFile sequenced through.
	tmp, err := os.CreateTemp(filepath.Dir(s.path), "devices-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleaned := false
	defer func() {
		if !cleaned {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return err
	}
	cleaned = true
	return nil
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
