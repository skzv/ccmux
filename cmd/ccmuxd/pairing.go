package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/apns"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/fcm"
)

func (s *server) handlePairToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Pairing produces a URL the phone POSTs back to over the tailnet.
	// If the tailnet listener was never started, the minted URL points
	// at a port nothing is listening on — the phone gets a confusing
	// "connection refused" with no hint that daemon.listen_tailnet is
	// off. Fail loudly here instead.
	if !s.cfg.Daemon.ListenTailnet {
		http.Error(w,
			"tailnet listener disabled (set daemon.listen_tailnet=true in config.toml and restart ccmuxd)",
			http.StatusServiceUnavailable)
		return
	}
	token, err := s.tokens.Create(5 * time.Minute)
	if err != nil {
		http.Error(w, "generate token: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// Build the ccmux:// deep-link URL.
	host, _ := os.Hostname()
	tailIP, _ := tailscaleAddr(r.Context(), s.cfg.Daemon.TailnetPort)
	// Use MagicDNS hostname if available, otherwise tailscale IP.
	pairHost := host
	if tailIP != "" {
		// tailIP is "IP:port"; strip the port
		if idx := strings.LastIndex(tailIP, ":"); idx >= 0 {
			pairHost = tailIP[:idx]
		}
	}
	sshUser := s.cfg.Daemon.SSHUser
	if sshUser == "" {
		sshUser, _ = os.LookupEnv("USER")
	}
	pairURL := fmt.Sprintf("ccmux://pair?host=%s&user=%s&port=%d&token=%s",
		pairHost, sshUser, s.cfg.Daemon.TailnetPort, token)
	writeJSON(w, daemon.PairTokenResponse{Token: token, URL: pairURL})
}

func (s *server) handlePair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req daemon.PairRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)).Decode(&req); err != nil {
		http.Error(w, "decode: "+err.Error(), http.StatusBadRequest)
		return
	}
	// Validate the public key BEFORE consuming the token so a malformed
	// key doesn't burn the user's single-use pair token. Returns a
	// canonical single-line authorized_keys entry (any pre-key options
	// the client tried to smuggle in are stripped during re-serialize).
	authLine, err := validatePairKey(req.PublicKey)
	if err != nil {
		http.Error(w, "invalid public key: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !s.tokens.Consume(req.Token) {
		http.Error(w, "invalid or expired token", http.StatusUnauthorized)
		return
	}
	if err := appendAuthorizedKey(authLine); err != nil {
		http.Error(w, "write authorized_keys: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// Optional APNs registration carried with the pair — lets push
	// work from first pair without a second round trip. Failures
	// (no device store, bad env) log and continue; pairing itself
	// has already succeeded.
	if s.devices != nil && req.DeviceToken != "" {
		if err := s.devices.Register(authLine, req.DeviceToken, req.APNsEnv); err != nil {
			log.Printf("ccmuxd: pair-time device register: %v", err)
		}
	}
	host, _ := os.Hostname()
	writeJSON(w, daemon.PairResponse{Hostname: host, Version: version})
}

// handleRegisterDevice updates an APNs device token on an already-
// paired host. The client supplies the public key it paired with so
// the daemon can scope the update to the right paired identity.
func (s *server) handleRegisterDevice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.devices == nil {
		http.Error(w, "device registry unavailable", http.StatusServiceUnavailable)
		return
	}
	var req daemon.RegisterDeviceRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)).Decode(&req); err != nil {
		http.Error(w, "decode: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.devices.RegisterWithProvider(req.PublicKey, req.Token, req.Provider, req.Env); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleTestPush sends a verification push to the device registered
// for the requesting public key. Returns 204 even when APNs is
// disabled (the request is well-formed; the user just hasn't flipped
// the switch yet) so the mobile UI can give an honest "sent"
// confirmation. The detailed status appears in ccmuxd's log.
func (s *server) handleTestPush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.devices == nil {
		http.Error(w, "device registry unavailable", http.StatusServiceUnavailable)
		return
	}
	var body struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)).Decode(&body); err != nil || body.PublicKey == "" {
		http.Error(w, "public_key required", http.StatusBadRequest)
		return
	}
	reg, ok := s.devices.Lookup(body.PublicKey)
	if !ok {
		http.Error(w, "no device registered for this key", http.StatusNotFound)
		return
	}
	if !s.apnsSender.Enabled() {
		log.Printf("ccmuxd: test push requested but APNs disabled")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.sendAPNsAsync("test-push", reg.Token, reg.Environment, apns.Notification{
		Title:     "ccmux test push",
		Body:      "If you see this, push notifications are working.",
		SessionID: "ccmux-test",
	})
	w.WriteHeader(http.StatusNoContent)
}

// sendAPNsAsync dispatches one push on a bounded worker pool. If the
// pool is saturated (16 concurrent sends — usually means APNs is
// stalled or down) the call drops the notification and logs, rather
// than spawning an unbounded goroutine that may never return.
func (s *server) sendAPNsAsync(label, token, env string, n apns.Notification) {
	select {
	case s.apnsSlots <- struct{}{}:
	default:
		log.Printf("ccmuxd: APNs %s (%s): dropped — sender saturated", label, n.SessionID)
		return
	}
	go func() {
		defer func() { <-s.apnsSlots }()
		if err := s.apnsSender.Send(token, env, n); err != nil {
			log.Printf("ccmuxd: APNs %s (%s): %v", label, n.SessionID, err)
		}
	}()
}

// maybePushForStateTransition fires a push when a tracked session
// enters a state the user should know about: needs_input (Y/N from
// the agent) or active → idle (the agent finished its response and
// is waiting for the next prompt). Routes per-device through the
// matching gateway (APNs for iOS, FCM for Android). No-op when both
// gateways are disabled or no devices are registered.
func (s *server) maybePushForStateTransition(sessionName string, prev, next agent.State) {
	if s.devices == nil || (!s.apnsSender.Enabled() && !s.fcmSender.Enabled()) {
		return
	}
	var title, body, kind string
	switch {
	case next == agent.StateNeedsInput:
		title = sessionName + " needs input"
		body = "Tap to reply."
		kind = "needs_input"
	case next == agent.StateIdle && prev == agent.StateActive:
		title = sessionName + " finished"
		body = "Your agent is waiting for the next prompt."
		kind = "active_to_idle"
	default:
		return
	}
	hostname, _ := os.Hostname()
	apnsNotif := apns.Notification{
		Title:     title,
		Body:      body,
		SessionID: "local/" + sessionName,
	}
	fcmNotif := fcm.Notification{
		Title:     title,
		Body:      body,
		SessionID: "local/" + sessionName,
		Kind:      kind,
		Host:      hostname,
	}
	for _, reg := range s.devices.All() {
		switch reg.ResolvedProvider() {
		case daemon.ProviderAPNs:
			if s.apnsSender.Enabled() {
				s.sendAPNsAsync("push", reg.Token, reg.Environment, apnsNotif)
			}
		case daemon.ProviderFCM:
			if s.fcmSender.Enabled() {
				s.sendFCMAsync("push", reg.Token, fcmNotif)
			}
		}
	}
}

// sendFCMAsync dispatches one FCM push on a bounded worker pool,
// mirroring sendAPNsAsync. While the fcm package is dormant Send is
// a no-op, but the bounded pool already exists so the eventual
// real-sender PR plugs in without changing this dispatcher.
func (s *server) sendFCMAsync(label, token string, n fcm.Notification) {
	select {
	case s.fcmSlots <- struct{}{}:
	default:
		log.Printf("ccmuxd: FCM %s (%s): dropped — sender saturated", label, n.SessionID)
		return
	}
	go func() {
		defer func() { <-s.fcmSlots }()
		if err := s.fcmSender.Send(token, n); err != nil {
			log.Printf("ccmuxd: FCM %s (%s): %v", label, n.SessionID, err)
		}
	}()
}

// validatePairKey parses the wire-format public key into a canonical
// authorized_keys line. Rejects:
//   - empty / whitespace-only input
//   - anything that isn't a parseable SSH public key
//   - extra data on the line (pre-key options like `command="rm -rf /"`,
//     `from="…"`, `no-pty`, etc.) — these would otherwise let a paired
//     peer install an unconstrained backdoor as a side effect of pairing
//   - extra lines (a peer that smuggles `<valid-key>\n<malicious-key>\n`)
//
// Re-serialize via MarshalAuthorizedKey to produce a canonical
// `<type> <base64>` line, dropping any comment and any options the
// caller may have prepended.
func validatePairKey(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("empty")
	}
	pub, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(s))
	if err != nil {
		return "", err
	}
	if len(options) > 0 {
		return "", errors.New("pre-key options are not allowed")
	}
	if strings.TrimSpace(string(rest)) != "" {
		return "", errors.New("multi-line input is not allowed")
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub))), nil
}

func appendAuthorizedKey(pubKey string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return err
	}
	authKeys := filepath.Join(sshDir, "authorized_keys")
	f, err := os.OpenFile(authKeys, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	key := strings.TrimSpace(pubKey) + "\n"
	_, err = f.WriteString(key)
	return err
}
