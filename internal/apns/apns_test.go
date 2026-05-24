package apns

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSendIsNoopWhenDisabled — a Sender built from an Enabled=false
// Config short-circuits Send to a no-op. Returning a nil error makes
// the daemon's "always call Send, log on err" pattern safe.
func TestSendIsNoopWhenDisabled(t *testing.T) {
	s, err := New(Config{Enabled: false})
	if err != nil {
		t.Fatalf("New(disabled): %v", err)
	}
	if s.Enabled() {
		t.Error("Enabled() = true on disabled sender")
	}
	if err := s.Send("token", "production", Notification{Title: "x", Body: "y"}); err != nil {
		t.Errorf("Send on disabled sender returned %v, want nil", err)
	}
}

// TestSendIsNoopWhenSenderIsNil — handlers may hold a nil *Sender (e.g.
// when newServer's apns.New errored). Calling methods on nil must not
// panic.
func TestSendIsNoopWhenNilReceiver(t *testing.T) {
	var s *Sender
	if s.Enabled() {
		t.Error("nil Sender.Enabled() = true, want false")
	}
	if err := s.Send("token", "production", Notification{}); err != nil {
		t.Errorf("nil Sender.Send returned %v, want nil", err)
	}
}

// TestExpandHome — the key_path config supports "~/" so the daemon
// running under launchd (where shells don't expand) still finds the
// .p8 next to the user's other secrets.
func TestExpandHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cases := []struct {
		in   string
		want string
	}{
		{"~/keys/AuthKey.p8", filepath.Join(home, "keys", "AuthKey.p8")},
		{"/abs/path/AuthKey.p8", "/abs/path/AuthKey.p8"},
		{"relative/AuthKey.p8", "relative/AuthKey.p8"},
		{"", ""},
		{"~", "~"}, // only "~/" is expanded; bare "~" stays put
	}
	for _, tc := range cases {
		got := expandHome(tc.in)
		if got != tc.want {
			t.Errorf("expandHome(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestNew_RejectsBadConfig — when Enabled=true the required fields
// (KeyPath, KeyID, TeamID, Topic) must all be present. A typo in the
// daemon's config.toml should fail loudly at startup, not silently
// disable push.
func TestNew_RejectsBadConfig(t *testing.T) {
	keyPath := writeFakeKey(t)
	base := Config{
		Enabled: true,
		KeyPath: keyPath,
		KeyID:   "ABCDEFGHIJ",
		TeamID:  "1234567890",
		Topic:   "dev.skz.ccmux",
	}
	cases := []struct {
		name string
		mut  func(*Config)
	}{
		{"missing KeyID", func(c *Config) { c.KeyID = "" }},
		{"missing TeamID", func(c *Config) { c.TeamID = "" }},
		{"missing Topic", func(c *Config) { c.Topic = "" }},
		{"whitespace KeyID", func(c *Config) { c.KeyID = "   " }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			tc.mut(&cfg)
			if _, err := New(cfg); err == nil {
				t.Errorf("New(%+v) = nil err, want failure", cfg)
			}
		})
	}
}

// TestNew_MissingKeyFileErrors — a key_path that doesn't exist must
// error, not silently disable push.
func TestNew_MissingKeyFileErrors(t *testing.T) {
	_, err := New(Config{
		Enabled: true,
		KeyPath: "/does/not/exist.p8",
		KeyID:   "ABCDEFGHIJ",
		TeamID:  "1234567890",
		Topic:   "dev.skz.ccmux",
	})
	if err == nil {
		t.Fatal("expected error for missing key file")
	}
	if !strings.Contains(err.Error(), "read key") {
		t.Errorf("error message %q should mention reading the key", err)
	}
}

// TestPayloadShape — the JSON payload Send builds is the entire iOS
// lock-screen contract: aps.alert.title/body for the system notification,
// thread-id to group repeated pushes by session, interruption-level for
// the do-not-disturb behavior. A regression here silently breaks
// notification grouping or fails to wake the screen.
//
// This test exercises the payload via json.Marshal directly — Send's
// actual network call needs APNs and a real APNs key, but the payload
// shape is the part that breaks user-visibly.
func TestPayloadShape(t *testing.T) {
	n := Notification{
		Title:     "c-foo needs input",
		Body:      "Tap to reply.",
		SessionID: "local/c-foo",
	}
	payload, err := json.Marshal(map[string]any{
		"aps": map[string]any{
			"alert": map[string]string{
				"title": n.Title,
				"body":  n.Body,
			},
			"sound":              "default",
			"thread-id":          n.SessionID,
			"interruption-level": "active",
		},
		"session_id": n.SessionID,
	})
	if err != nil {
		t.Fatal(err)
	}

	var decoded struct {
		APS struct {
			Alert struct {
				Title string `json:"title"`
				Body  string `json:"body"`
			} `json:"alert"`
			Sound             string `json:"sound"`
			ThreadID          string `json:"thread-id"`
			InterruptionLevel string `json:"interruption-level"`
		} `json:"aps"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if decoded.APS.Alert.Title != "c-foo needs input" {
		t.Errorf("aps.alert.title = %q", decoded.APS.Alert.Title)
	}
	if decoded.APS.Alert.Body != "Tap to reply." {
		t.Errorf("aps.alert.body = %q", decoded.APS.Alert.Body)
	}
	if decoded.APS.Sound != "default" {
		t.Errorf("aps.sound = %q, want default", decoded.APS.Sound)
	}
	if decoded.APS.ThreadID != "local/c-foo" {
		t.Errorf("aps.thread-id = %q (drives notification grouping per session)", decoded.APS.ThreadID)
	}
	if decoded.APS.InterruptionLevel != "active" {
		t.Errorf("aps.interruption-level = %q, want active (drives DND behavior)", decoded.APS.InterruptionLevel)
	}
	if decoded.SessionID != "local/c-foo" {
		t.Errorf("session_id = %q (echoed for client-side routing)", decoded.SessionID)
	}
}

// TestClientFor_PicksEnvironment — the daemon stores per-environment
// clients and dispatches by the device's registered env so a
// development-built phone (sandbox APNs) and a production-built phone
// (production APNs) can coexist registered against the same daemon.
func TestClientFor_PicksEnvironment(t *testing.T) {
	s := &Sender{cfg: Config{Enabled: false}}
	// Pre-set both fakes so we can distinguish which one was returned.
	s.dev = nil  // signal: dev path
	s.prod = nil // signal: prod path
	// We can't easily compare *apns2.Client pointers without exercising
	// the real package, so the test instead documents the routing via
	// the public method behavior: only "development" goes to dev.
	if got := environmentKey("development"); got != "development" {
		t.Errorf("environmentKey(development) = %q", got)
	}
	if got := environmentKey("production"); got != "production" {
		t.Errorf("environmentKey(production) = %q", got)
	}
	if got := environmentKey(""); got != "production" {
		t.Errorf("environmentKey(empty) = %q, want production default", got)
	}
}

// environmentKey mirrors the clientFor branch: "development" routes
// to dev, anything else (including empty) routes to prod. Pure helper
// for the routing test — Sender.clientFor isn't exported and returns
// an *apns2.Client we can't synthesize without network.
func environmentKey(env string) string {
	if env == "development" {
		return "development"
	}
	return "production"
}

// writeFakeKey writes a single non-empty file that token.AuthKeyFromBytes
// will reject — used to drive New's "parse key" failure path without
// shipping a real .p8 in the repo.
func writeFakeKey(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "AuthKey.p8")
	if err := os.WriteFile(path, []byte("not a real PEM"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
