package sshsetup

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// testServer is an in-process SSH server we run on 127.0.0.1 so the
// installer / enumerator tests have a real-protocol target without
// requiring sshd, root, or a network. Mirrors enough of openssh's
// behavior that the production code paths exercise their real
// branches:
//
//   - Password auth (configurable password)
//   - Public-key auth (against the keys we tell it to trust)
//   - Sessions that run "sh -s" with stdin → captures the script for
//     the dedup / chmod assertions
//   - One-shot commands (uname, dscl/getent fake-out)
//
// The server is single-tenant: one Accept loop, one stack of remote
// state per test. Tests pass `t.Cleanup` so Close runs after PASS or
// FAIL.
type testServer struct {
	addr             string
	listener         net.Listener
	hostKey          ssh.Signer
	password         string
	authorizedKeys   map[string]bool // wire-marshaled key → trust
	authorizedKeysMu sync.Mutex
	authedSessions   []*sessionLog
	authedMu         sync.Mutex
	uname            string
	dsclOutput       string
	getentOutput     string
	authFailUntil    int // first N password attempts return failure (for retry tests)
	authFailMu       sync.Mutex
	// disableTrustUpdate — when true, applyAuthorizedAppend is a
	// no-op. Used by the validation-failure test to simulate a
	// remote that accepts the write but doesn't honor the key
	// (e.g. authorized_keys path overridden by AuthorizedKeysCommand).
	disableTrustUpdate bool
}

// sessionLog records every command run during a client's lifetime
// so tests can assert the exact remote-shell side effects.
type sessionLog struct {
	cmd     string
	stdin   string
	exit    int
	user    string
	method  string
	startAt time.Time
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	s := &testServer{
		password:       "hunter2",
		authorizedKeys: map[string]bool{},
		uname:          "Linux",
		getentOutput: `root:x:0:0:root:/root:/bin/bash
alice:x:1000:1000:Alice:/home/alice:/bin/bash
bob:x:1001:1001:Bob:/home/bob:/bin/zsh
disabled:x:1002:1002::/home/disabled:/usr/sbin/nologin
`,
	}
	// Generate a fresh host key per server so tests are hermetic.
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	s.hostKey = signer

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.listener = ln
	s.addr = ln.Addr().String()
	go s.serveLoop(t)
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

// Host and Port split the listener's address for Target construction.
func (s *testServer) Host() string {
	host, _, _ := net.SplitHostPort(s.addr)
	return host
}
func (s *testServer) Port() int {
	_, port, _ := net.SplitHostPort(s.addr)
	var n int
	fmt.Sscanf(port, "%d", &n)
	return n
}

// AuthorizeKey adds a public key (in authorized-keys text form) to
// the server's trust list. Used by validation paths that need to
// pass auth twice.
func (s *testServer) AuthorizeKey(pubLine string) {
	pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pubLine))
	if err != nil {
		panic("test: invalid pubLine: " + err.Error())
	}
	s.authorizedKeysMu.Lock()
	defer s.authorizedKeysMu.Unlock()
	s.authorizedKeys[string(pk.Marshal())] = true
}

// authorizedKeyMaterial returns the set of base64 chunks the server
// will accept. Tests use it to assert that the remote-side append
// actually ran (we sniff what the server saw + what's in our trust
// map after the run).
func (s *testServer) authorizedKeyMaterial() []string {
	s.authorizedKeysMu.Lock()
	defer s.authorizedKeysMu.Unlock()
	out := make([]string, 0, len(s.authorizedKeys))
	for raw := range s.authorizedKeys {
		// We stored the wire encoding; render the middle base64
		// chunk back for assertion clarity.
		pk, err := ssh.ParsePublicKey([]byte(raw))
		if err != nil {
			continue
		}
		line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pk)))
		out = append(out, middleField(line))
	}
	return out
}

// Sessions returns the per-session log for assertions.
func (s *testServer) Sessions() []*sessionLog {
	s.authedMu.Lock()
	defer s.authedMu.Unlock()
	out := make([]*sessionLog, len(s.authedSessions))
	copy(out, s.authedSessions)
	return out
}

// SetAuthFailUntil makes the first n password attempts fail, then
// succeed. Lets retry tests exercise the wrong-password loop.
func (s *testServer) SetAuthFailUntil(n int) {
	s.authFailMu.Lock()
	defer s.authFailMu.Unlock()
	s.authFailUntil = n
}

func (s *testServer) serveLoop(t *testing.T) {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handle(t, conn)
	}
}

func (s *testServer) handle(t *testing.T, nc net.Conn) {
	defer nc.Close()
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(meta ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			s.authFailMu.Lock()
			fail := s.authFailUntil > 0
			if fail {
				s.authFailUntil--
			}
			s.authFailMu.Unlock()
			if fail {
				return nil, errors.New("test: forced fail")
			}
			if string(pw) != s.password {
				return nil, errors.New("password rejected")
			}
			return &ssh.Permissions{Extensions: map[string]string{"auth-method": "password"}}, nil
		},
		PublicKeyCallback: func(meta ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			s.authorizedKeysMu.Lock()
			defer s.authorizedKeysMu.Unlock()
			if s.authorizedKeys[string(k.Marshal())] {
				return &ssh.Permissions{Extensions: map[string]string{"auth-method": "publickey"}}, nil
			}
			return nil, errors.New("key not authorized")
		},
	}
	cfg.AddHostKey(s.hostKey)
	sc, chans, reqs, err := ssh.NewServerConn(nc, cfg)
	if err != nil {
		// Failed handshake — normal for the auth-fail path.
		return
	}
	defer sc.Close()
	method := ""
	if sc.Permissions != nil {
		method = sc.Permissions.Extensions["auth-method"]
	}

	go ssh.DiscardRequests(reqs)
	for ch := range chans {
		if ch.ChannelType() != "session" {
			_ = ch.Reject(ssh.UnknownChannelType, "")
			continue
		}
		channel, requests, err := ch.Accept()
		if err != nil {
			continue
		}
		go s.handleSession(channel, requests, sc.User(), method)
	}
}

func (s *testServer) handleSession(ch ssh.Channel, reqs <-chan *ssh.Request, user, method string) {
	defer ch.Close()
	log := &sessionLog{user: user, method: method, startAt: time.Now()}
	for req := range reqs {
		switch req.Type {
		case "exec":
			cmd := parseExecPayload(req.Payload)
			log.cmd = cmd
			_ = req.Reply(true, nil)
			// Run the exec synchronously so we don't close the
			// channel before the response is on the wire — the
			// client only sees exit-status if it arrives BEFORE
			// the channel EOF.
			s.runExec(ch, log, cmd)
			s.authedMu.Lock()
			s.authedSessions = append(s.authedSessions, log)
			s.authedMu.Unlock()
			return
		default:
			_ = req.Reply(false, nil)
		}
	}
}

// runExec is the tiny shell-like simulator. We don't run a real
// shell because that would couple the test to the dev's environment
// and to a /tmp side effect. Instead we recognize the specific
// commands the installer + enumerator emit and respond verbatim.
func (s *testServer) runExec(ch ssh.Channel, log *sessionLog, cmd string) {
	// `sh -s` is the installer's append path; we read the script
	// off stdin and emulate its effect (parse for pubkey, add to
	// authorized_keys map). Anything else is a one-shot command
	// like `uname`, `dscl ...`, `getent ...`.
	switch {
	case cmd == "sh -s":
		body, _ := io.ReadAll(ch)
		log.stdin = string(body)
		s.applyAuthorizedAppend(string(body))
		_ = sendExitStatus(ch, 0)
	case cmd == "uname":
		_, _ = io.WriteString(ch, s.uname+"\n")
		_ = sendExitStatus(ch, 0)
	case strings.HasPrefix(cmd, "dscl"):
		_, _ = io.WriteString(ch, s.dsclOutput)
		_ = sendExitStatus(ch, 0)
	case strings.Contains(cmd, "getent passwd"), strings.Contains(cmd, "/etc/passwd"):
		_, _ = io.WriteString(ch, s.getentOutput)
		_ = sendExitStatus(ch, 0)
	case cmd == "exit":
		_ = sendExitStatus(ch, 0)
	default:
		_ = sendExitStatus(ch, 0)
	}
}

// applyAuthorizedAppend pulls every authorized-keys-shaped line out
// of the installer's script body and adds it to the server's trust
// map. Mirrors what a real remote shell would do: we don't try to
// replay the literal script, we just observe the side effect the
// installer intended. If disableTrustUpdate is set, the side effect
// is suppressed so validation tests can drive the "write succeeded
// but key not honored" branch.
func (s *testServer) applyAuthorizedAppend(script string) {
	if s.disableTrustUpdate {
		return
	}
	for _, line := range strings.Split(script, "\n") {
		// The installer's script uses printf '%s\n' '<pubkey>' >>
		// authorized_keys. The shell-quoted form lives inside
		// single quotes; pull the first quoted slug per line.
		if !strings.Contains(line, "printf") {
			continue
		}
		start := strings.Index(line, "'")
		if start < 0 {
			continue
		}
		// Find the matching close-quote — naive but enough for our
		// test scripts where the pubkey itself has no single quote.
		rest := line[start+1:]
		end := strings.Index(rest, "'")
		if end < 0 {
			continue
		}
		// Walk forward through `'"'"'` escape sequences if any
		// were emitted (our test pubkeys have no quotes so this
		// branch is unreached, but it's defensive against future
		// pubkey shapes).
		quoted := rest[:end]
		// quoted is now the inner key blob; the *last* quoted slug
		// in the line is the actual key, since the script does
		// printf '%s\n' '<key>'.
		if last := strings.LastIndex(line, "'"); last > start {
			between := line[strings.Index(line, "'")+1 : last]
			// The format is `printf '%s\n' 'KEY'` so between is
			// `%s\n' 'KEY` — split on `' '` to recover KEY.
			parts := strings.Split(between, "' '")
			if len(parts) == 2 {
				quoted = parts[1]
			}
		}
		pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(quoted))
		if err != nil {
			continue
		}
		s.authorizedKeysMu.Lock()
		s.authorizedKeys[string(pk.Marshal())] = true
		s.authorizedKeysMu.Unlock()
	}
}

// HostKeyLine returns a known_hosts line for this server. Tests use
// it to pre-seed known_hosts so we can also drive the mismatch path
// without poking the real ~/.ssh.
func (s *testServer) HostKeyLine() string {
	pk := s.hostKey.PublicKey()
	return fmt.Sprintf("[%s]:%d %s %s",
		s.Host(), s.Port(),
		pk.Type(),
		base64Of(pk.Marshal()),
	)
}

// dialFromTest returns an ssh.Client dialed at the test server using
// the supplied config. Skips the TOFU known_hosts step so the
// install path's hkCB is exercised separately.
func (s *testServer) dialFromTest(cfg *ssh.ClientConfig) (*ssh.Client, error) {
	cfg.HostKeyCallback = ssh.InsecureIgnoreHostKey()
	return ssh.Dial("tcp", s.addr, cfg)
}

// withTempHome redirects HOME to a fresh temp dir for the duration
// of t and returns the path. The tests rely on EnsureLocalKey
// reading the same HOME, so this MUST be called before any helper
// that calls UserHomeDir under the hood. macOS sockaddr_un caps at
// 104 bytes so we deliberately use /tmp here, not t.TempDir() which
// lives under /var/folders.
func withTempHome(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ccmux-sshsetup-home-")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// generateAndPlant generates a real ed25519 / rsa-shaped key pair
// and writes it to $home/.ssh/<name>. Returns the planted LocalKey
// for test assertions. RSA tests still use ed25519 material under
// the rsa filename — EnsureLocalKey only cares about the file
// name + that the line parses, so this is fine for reuse-precedence
// tests without pulling in a slow RSA generator.
func generateAndPlant(t *testing.T, home, name string) LocalKey {
	t.Helper()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "test")
	if err != nil {
		t.Fatal(err)
	}
	privPath := filepath.Join(sshDir, name)
	pubPath := privPath + ".pub"
	pf, err := os.OpenFile(privPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := pem.Encode(pf, block); err != nil {
		t.Fatal(err)
	}
	if err := pf.Close(); err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	pubLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " planted@test"
	if err := os.WriteFile(pubPath, []byte(pubLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return LocalKey{PrivatePath: privPath, PublicPath: pubPath, PublicLine: pubLine}
}

// parseExecPayload extracts the command from an "exec" ssh request
// payload. The wire format is `uint32 len | <len bytes of command>`.
func parseExecPayload(payload []byte) string {
	if len(payload) < 4 {
		return ""
	}
	n := int(payload[0])<<24 | int(payload[1])<<16 | int(payload[2])<<8 | int(payload[3])
	if 4+n > len(payload) {
		return ""
	}
	return string(payload[4 : 4+n])
}

// sendExitStatus emits the `exit-status` SSH request the client
// reads to determine the remote exit code. ssh.Session.Wait blocks
// until it sees this.
func sendExitStatus(ch ssh.Channel, code uint32) error {
	payload := []byte{
		byte(code >> 24), byte(code >> 16), byte(code >> 8), byte(code),
	}
	_, err := ch.SendRequest("exit-status", false, payload)
	return err
}

// base64Of is a tiny helper to render binary key material as the
// authorized-keys / known_hosts base64 chunk.
func base64Of(b []byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	// Use encoding/base64 in the real path; this helper only feeds
	// HostKeyLine which is for human-readable test logs.
	return encB64(b, alphabet)
}

// encB64 is a thin wrapper that avoids importing encoding/base64 in
// the test file's top-level imports for the same reason — keeps the
// surface minimal. It's the std-lib implementation inlined.
func encB64(b []byte, alpha string) string {
	enc := make([]byte, 0, ((len(b)+2)/3)*4)
	for i := 0; i < len(b); i += 3 {
		var n uint32
		left := len(b) - i
		switch {
		case left >= 3:
			n = uint32(b[i])<<16 | uint32(b[i+1])<<8 | uint32(b[i+2])
			enc = append(enc, alpha[(n>>18)&63], alpha[(n>>12)&63], alpha[(n>>6)&63], alpha[n&63])
		case left == 2:
			n = uint32(b[i])<<16 | uint32(b[i+1])<<8
			enc = append(enc, alpha[(n>>18)&63], alpha[(n>>12)&63], alpha[(n>>6)&63], '=')
		case left == 1:
			n = uint32(b[i]) << 16
			enc = append(enc, alpha[(n>>18)&63], alpha[(n>>12)&63], '=', '=')
		}
	}
	return string(enc)
}
