// Package fcm sends ccmux push notifications to paired Android devices.
//
// This package is dormant by default — Sender.Send is a no-op until
// the daemon's [fcm] config provides a credentials file and project
// id, AND an Android mobile client (FCM is the Android push transport)
// ships a build with Firebase Messaging enabled. The shape mirrors
// internal/apns so the daemon's dispatcher can route per device-record
// provider without special-casing either gateway.
//
// When wiring the real FCM v1 sender in a follow-up:
//
//   - import "firebase.google.com/go/v4/messaging" (and its parent
//     "firebase.google.com/go/v4")
//   - load credentials with option.WithCredentialsFile(cfg.CredentialsPath)
//   - cache one *messaging.Client per Sender (it's goroutine-safe)
//   - map our Notification struct to a *messaging.Message with the
//     "data" map carrying kind/session/host so the Android client's
//     FirebaseMessagingService can deep-link into the session detail
package fcm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Config carries everything the daemon needs to talk to FCM. All
// fields are required when Enabled=true; Enabled=false short-circuits
// Send to a no-op without ever touching disk or network.
type Config struct {
	Enabled         bool
	CredentialsPath string // absolute path to Firebase service-account JSON
	ProjectID       string // Firebase project id, e.g. "ccmux-mobile"
}

// Sender is a thin handle around the FCM client. The real client
// lives behind an opaque field that's nil while the package is
// dormant; the dispatcher checks Enabled() before calling Send so
// no-op sends don't materialize a goroutine.
type Sender struct {
	cfg Config

	mu sync.Mutex
	// client *messaging.Client  // populated by the follow-up PR
}

// Notification is the daemon-side view of one push: a title/body
// plus the session id, which the Android client uses to thread
// together repeated notifications for the same session. Identical
// shape to apns.Notification so the dispatcher can build one struct
// and route it to either gateway.
type Notification struct {
	Title     string
	Body      string
	SessionID string

	// Kind is the structured event type ("needs_input" |
	// "active_to_idle") that the Android FirebaseMessagingService
	// reads from the data payload to deep-link correctly. APNs
	// carries the same value via aps.category; FCM carries it in
	// the data map.
	Kind string

	// Host is the daemon's hostname, so the Android notification can
	// disambiguate "session foo on mini" vs "session foo on laptop".
	Host string
}

// New builds a Sender. Errors only when Enabled=true and the
// credentials file can't be read; a disabled config returns a Sender
// whose Send is a no-op so callers don't have to nil-check. The
// follow-up PR will replace the stub-load with real client init.
func New(cfg Config) (*Sender, error) {
	if !cfg.Enabled {
		return &Sender{cfg: cfg}, nil
	}
	credPath := expandHome(cfg.CredentialsPath)
	if _, err := os.Stat(credPath); err != nil {
		return nil, fmt.Errorf("fcm: read credentials %q: %w", credPath, err)
	}
	if strings.TrimSpace(cfg.ProjectID) == "" {
		return nil, errors.New("fcm: ProjectID required when Enabled=true")
	}
	// TODO(android-companion-app PR2): initialize *messaging.Client here.
	// For now, validating that credentials are readable + project id
	// is non-empty is enough to surface "FCM enabled" in the logs and
	// route records through this sender (whose Send is still a no-op).
	return &Sender{cfg: cfg}, nil
}

// Enabled reports whether this Sender is wired to a real FCM client.
// Mirrors apns.Sender.Enabled. The dispatcher uses this to skip the
// FCM branch when the daemon's config didn't opt in.
func (s *Sender) Enabled() bool {
	if s == nil {
		return false
	}
	return s.cfg.Enabled
}

// Send delivers one notification to the supplied FCM device token.
// Returns nil when the package is dormant so callers can compose
// dispatch loops the same way they do for APNs. The follow-up PR
// fills in the real FCM v1 send path.
func (s *Sender) Send(deviceToken string, n Notification) error {
	if s == nil || !s.cfg.Enabled {
		return nil
	}
	if strings.TrimSpace(deviceToken) == "" {
		return errors.New("fcm: empty device token")
	}
	// TODO(android-companion-app PR2): wire the real send. For now,
	// returning nil lets the daemon's dispatcher treat the FCM branch
	// as well-formed without any side effects, which is what the
	// "dormant" config implies.
	return nil
}

// expandHome resolves a leading "~/" the same way apns.expandHome
// does. Keeping a local copy avoids cross-package coupling between
// two gateways that should be drop-in replacements.
func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
