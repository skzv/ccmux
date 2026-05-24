package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
)

// testEd25519AuthorizedKey returns a real authorized_keys line backed
// by a freshly-generated ed25519 keypair. Real keys are required since
// validatePairKey rejects anything ssh.ParseAuthorizedKey can't parse.
func testEd25519AuthorizedKey(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
}

// pairTestServer builds the minimal server the pairing handlers touch:
// just a token store and the daemon config they read host/user/port from.
// ListenTailnet=true matches the configuration the pairing CUJ actually
// runs under — handlePairToken refuses to mint a URL pointing at a
// listener that was never started.
func pairTestServer() *server {
	return &server{
		tokens: daemon.NewTokenStore(),
		cfg: config.Config{
			Daemon: config.DaemonConfig{TailnetPort: 7474, SSHUser: "alice", ListenTailnet: true},
		},
	}
}

// TestHandlePairToken_POST — POST /v1/pair-token issues a hex token and a
// ccmux:// deep-link carrying that token, the configured user, and port;
// the issued token is live in the store.
func TestHandlePairToken_POST(t *testing.T) {
	s := pairTestServer()
	rec := httptest.NewRecorder()
	s.handlePairToken(rec, httptest.NewRequest(http.MethodPost, "/v1/pair-token", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var resp daemon.PairTokenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Token) != 32 {
		t.Fatalf("token %q length %d, want 32 hex chars", resp.Token, len(resp.Token))
	}
	if _, err := hex.DecodeString(resp.Token); err != nil {
		t.Fatalf("token is not hex: %v", err)
	}
	for _, want := range []string{
		"ccmux://pair?host=",
		"&user=alice",
		"&port=7474",
		"&token=" + resp.Token,
	} {
		if !strings.Contains(resp.URL, want) {
			t.Errorf("deep-link %q missing %q", resp.URL, want)
		}
	}
	// The issued token must be registered — consumable exactly once.
	if !s.tokens.Consume(resp.Token) {
		t.Error("the issued token was not registered in the store")
	}
}

// TestHandlePairToken_RejectsGET — the endpoint is POST-only.
func TestHandlePairToken_RejectsGET(t *testing.T) {
	s := pairTestServer()
	rec := httptest.NewRecorder()
	s.handlePairToken(rec, httptest.NewRequest(http.MethodGet, "/v1/pair-token", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", rec.Code)
	}
}

// TestHandlePairToken_RefusesWhenTailnetDisabled — minting a pair URL
// when daemon.listen_tailnet=false would hand the phone a URL pointing
// at a port nothing's listening on, with no hint why. Fail loudly
// instead.
func TestHandlePairToken_RefusesWhenTailnetDisabled(t *testing.T) {
	s := pairTestServer()
	s.cfg.Daemon.ListenTailnet = false
	rec := httptest.NewRecorder()
	s.handlePairToken(rec, httptest.NewRequest(http.MethodPost, "/v1/pair-token", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "listen_tailnet") {
		t.Errorf("error body %q should mention the config key to set", rec.Body)
	}
}

// TestHandlePair_InvalidToken — POST /v1/pair with a token that was never
// issued is rejected and writes no key.
func TestHandlePair_InvalidToken(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := pairTestServer()
	body, _ := json.Marshal(daemon.PairRequest{
		Token:     "bogus",
		PublicKey: testEd25519AuthorizedKey(t),
	})
	rec := httptest.NewRecorder()
	s.handlePair(rec, httptest.NewRequest(http.MethodPost, "/v1/pair", bytes.NewReader(body)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for an invalid token", rec.Code)
	}
}

// TestHandlePair_ValidTokenAppendsKey — a valid token pairs successfully:
// the public key is appended to authorized_keys, and the token can't be
// replayed afterward.
func TestHandlePair_ValidTokenAppendsKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	s := pairTestServer()
	tok, err := s.tokens.Create(time.Minute)
	if err != nil {
		t.Fatalf("Create token: %v", err)
	}
	pubKey := testEd25519AuthorizedKey(t)
	reqBody, _ := json.Marshal(daemon.PairRequest{Token: tok, PublicKey: pubKey})

	rec := httptest.NewRecorder()
	s.handlePair(rec, httptest.NewRequest(http.MethodPost, "/v1/pair", bytes.NewReader(reqBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	got, err := os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys"))
	if err != nil {
		t.Fatalf("read authorized_keys: %v", err)
	}
	if !strings.Contains(string(got), pubKey) {
		t.Errorf("authorized_keys does not contain the paired key:\n%s", got)
	}

	// One-time: replaying the same token must now be rejected.
	replay, _ := json.Marshal(daemon.PairRequest{Token: tok, PublicKey: pubKey})
	rec2 := httptest.NewRecorder()
	s.handlePair(rec2, httptest.NewRequest(http.MethodPost, "/v1/pair", bytes.NewReader(replay)))
	if rec2.Code != http.StatusUnauthorized {
		t.Errorf("token replay status = %d, want 401", rec2.Code)
	}
}

// TestHandlePair_RejectsMalformedKey — every malformed public-key shape
// must be rejected with 400 *before* the token is consumed. Otherwise
// a malicious or buggy client burns the user's single-use pair token
// without actually pairing.
func TestHandlePair_RejectsMalformedKey(t *testing.T) {
	validKey := testEd25519AuthorizedKey(t)

	// A real second key, used to build a multi-line attack payload.
	secondKey := testEd25519AuthorizedKey(t)

	cases := []struct {
		name string
		key  string
	}{
		{"empty", ""},
		{"whitespace", "   \n\t  "},
		{"not-a-key", "this is not an ssh key"},
		{"truncated", "ssh-ed25519 AAAATEST"},
		{
			// `command="…"` would otherwise execute on every SSH login.
			"smuggled command option",
			`command="rm -rf /" ` + validKey,
		},
		{
			// `from="…"` would constrain the key to a peer-chosen origin.
			"smuggled from option",
			`from="evil.example.com" ` + validKey,
		},
		{
			// A second line would install a second, attacker-controlled key.
			"multi-line injection",
			validKey + "\n" + secondKey + "\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			s := pairTestServer()
			tok, err := s.tokens.Create(time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := json.Marshal(daemon.PairRequest{Token: tok, PublicKey: tc.key})
			rec := httptest.NewRecorder()
			s.handlePair(rec, httptest.NewRequest(http.MethodPost, "/v1/pair", bytes.NewReader(body)))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body=%s", rec.Code, rec.Body)
			}
			// The token must NOT have been consumed by a rejected request.
			if !s.tokens.Consume(tok) {
				t.Error("token was burned even though pairing was rejected")
			}
		})
	}
}

// TestRoutes_PairTokenIsLocalOnly — the tailnet mux registers only
// routes(), not localOnlyRoutes(). /v1/pair-token must therefore return
// 404 on the tailnet listener — a peer can't issue pair tokens for
// itself.
func TestRoutes_PairTokenIsLocalOnly(t *testing.T) {
	s := pairTestServer()

	localMux := http.NewServeMux()
	s.routes(localMux)
	s.localOnlyRoutes(localMux)

	tailnetMux := http.NewServeMux()
	s.routes(tailnetMux)

	// Local mux: POST /v1/pair-token succeeds (200).
	rec := httptest.NewRecorder()
	localMux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/pair-token", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("local /v1/pair-token = %d, want 200", rec.Code)
	}

	// Tailnet mux: POST /v1/pair-token must 404 — the route isn't there.
	rec = httptest.NewRecorder()
	tailnetMux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/pair-token", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("tailnet /v1/pair-token = %d, want 404 (route must not exist)", rec.Code)
	}

	// Sanity: /v1/pair (the consume-token endpoint) IS on both — the
	// mobile app needs to reach it.
	rec = httptest.NewRecorder()
	tailnetMux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/pair", bytes.NewReader([]byte("{}"))))
	if rec.Code == http.StatusNotFound {
		t.Error("tailnet /v1/pair is unexpectedly missing")
	}
}
