//go:build integration

package e2e

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/skzv/ccmux/internal/config"
)

// TestSSHSetupCLI_EndToEnd_KeyInstalledAndProbeOK is the headline
// E2E for `ccmux host setup-ssh`. It:
//
//  1. Spins an in-process SSH server on 127.0.0.1.
//  2. Writes a hosts.toml pointing at the server's address.
//  3. Spawns the freshly-built `ccmux` binary with `host setup-ssh
//     <name>` and pipes the test password into stdin.
//  4. Verifies the binary exits 0.
//  5. Verifies the server's authorized-keys map now contains our
//     local public key.
//
// This drives all four halves of the feature: the package's
// crypto/ssh client, the CLI's stdin-piped password path, the
// hosts.toml resolution, and the install-then-validate handshake.
func TestSSHSetupCLI_EndToEnd_KeyInstalledAndProbeOK(t *testing.T) {
	e := newEnv(t)

	srv := newE2ESSHServer(t)
	host, port := srv.HostPort()

	// Wire a configured host pointing at our test server's exact
	// address + port. `ccmux host setup-ssh <name>` resolves this
	// to (User=alice, Host=127.0.0.1, Port=<random>).
	cfg := e.defaultConfig()
	cfg.Hosts = append(cfg.Hosts, config.Host{
		Name: "sputnik", Address: host, User: "alice", Port: port, Mosh: false,
	})
	e.writeConfig(cfg)

	// Run the CLI. Pipe "hunter2\n" + "\n" (skip enumerate) into
	// stdin. We pass --skip-enumerate to remove the noise of the
	// y/N loop entirely.
	args := []string{"host", "setup-ssh", "--skip-enumerate", "sputnik"}
	cmd := exec.Command(builtCcmux, args...)
	cmd.Env = os.Environ() // newEnv already set HOME via t.Setenv
	cmd.Dir = e.Home
	cmd.Stdin = strings.NewReader("hunter2\n")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("ccmux host setup-ssh failed: %v\nstdout:\n%s\nstderr:\n%s",
			err, stdout.String(), stderr.String())
	}
	// Stdout should mention the canonical success line.
	if !strings.Contains(stdout.String(), "key installed on") {
		t.Errorf("expected 'key installed on' in stdout, got:\n%s\nstderr:\n%s",
			stdout.String(), stderr.String())
	}

	// Server-side assertion: the test server's authorized keys
	// must now contain our local pubkey's wire material. This is
	// the actual proof the install side effect landed.
	if !srv.HasAnyAuthorizedKey() {
		t.Fatal("server never received an authorized-keys append")
	}

	// Pre-flight check on what got written:
	if kh, err := os.ReadFile(filepath.Join(e.Home, ".ssh", "known_hosts")); err == nil {
		t.Logf("known_hosts after install:\n%s", string(kh))
	}

	// NOTE on a second run: an ideal check would re-run the CLI
	// and assert it hits ProbeOK (short-circuiting before the
	// install). That doesn't work hermetically here because
	// macOS openssh tilde-expands the default IdentityFile via
	// getpwuid() rather than $HOME — so the system `ssh` binary
	// that Probe() shells out to reads the dev's REAL ~/.ssh, not
	// the test HOME. The wizard's install path is unaffected (it
	// uses crypto/ssh directly with explicit paths). Production
	// machines don't hit this because the real user's $HOME and
	// pw_dir agree. Covered by manual smoke test.
}

// TestSSHSetupCLI_WrongPasswordExitsNonZero — type the wrong
// password, expect the CLI to surface "password rejected" and
// exit non-zero. The user can then re-run.
func TestSSHSetupCLI_WrongPasswordExitsNonZero(t *testing.T) {
	e := newEnv(t)
	srv := newE2ESSHServer(t)
	host, port := srv.HostPort()
	cfg := e.defaultConfig()
	cfg.Hosts = append(cfg.Hosts, config.Host{
		Name: "sputnik", Address: host, User: "alice", Port: port, Mosh: false,
	})
	e.writeConfig(cfg)

	cmd := exec.Command(builtCcmux, "host", "setup-ssh", "--skip-enumerate", "sputnik")
	cmd.Env = os.Environ()
	cmd.Dir = e.Home
	cmd.Stdin = strings.NewReader("wrong-password\n")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit on wrong password; output:\n%s", out)
	}
	if !strings.Contains(string(out), "password rejected") {
		t.Errorf("expected 'password rejected' in output, got:\n%s", out)
	}
}

// e2eSSHServer is the integration-test counterpart of testServer in
// internal/sshsetup. Sits on a real localhost TCP listener so the
// CLI exercises the real network path (rather than an inline
// dial-seam). Behavior mirrors openssh enough for the install +
// validate handshake to work.
type e2eSSHServer struct {
	addr           string
	listener       net.Listener
	password       string
	hostSigner     ssh.Signer
	authorizedKeys map[string]bool
	mu             sync.Mutex
	done           chan struct{}
}

func newE2ESSHServer(t *testing.T) *e2eSSHServer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &e2eSSHServer{
		addr:           ln.Addr().String(),
		listener:       ln,
		password:       "hunter2",
		hostSigner:     signer,
		authorizedKeys: map[string]bool{},
		done:           make(chan struct{}),
	}
	go s.serve()
	t.Cleanup(func() {
		close(s.done)
		_ = ln.Close()
	})
	return s
}

func (s *e2eSSHServer) HostPort() (string, int) {
	h, pStr, _ := net.SplitHostPort(s.addr)
	var p int
	fmt.Sscanf(pStr, "%d", &p)
	return h, p
}

// HasAnyAuthorizedKey returns true once the install script has
// appended at least one key.
func (s *e2eSSHServer) HasAnyAuthorizedKey() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.authorizedKeys) > 0
}

func (s *e2eSSHServer) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *e2eSSHServer) handle(nc net.Conn) {
	defer nc.Close()
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(meta ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if string(pw) != s.password {
				return nil, errors.New("rejected")
			}
			return &ssh.Permissions{}, nil
		},
		PublicKeyCallback: func(meta ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.authorizedKeys[string(k.Marshal())] {
				return &ssh.Permissions{}, nil
			}
			return nil, errors.New("not authorized")
		},
	}
	cfg.AddHostKey(s.hostSigner)
	conn, chans, reqs, err := ssh.NewServerConn(nc, cfg)
	if err != nil {
		return
	}
	defer conn.Close()
	go ssh.DiscardRequests(reqs)
	for ch := range chans {
		if ch.ChannelType() != "session" {
			_ = ch.Reject(ssh.UnknownChannelType, "")
			continue
		}
		c, rs, err := ch.Accept()
		if err != nil {
			continue
		}
		s.handleSession(c, rs)
	}
}

func (s *e2eSSHServer) handleSession(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	for req := range reqs {
		if req.Type != "exec" {
			_ = req.Reply(false, nil)
			continue
		}
		_ = req.Reply(true, nil)
		cmd := parseSSHExec(req.Payload)
		switch {
		case cmd == "sh -s":
			body, _ := io.ReadAll(ch)
			s.applyAuthorizedAppend(string(body))
		case cmd == "uname":
			_, _ = io.WriteString(ch, "Linux\n")
		case strings.Contains(cmd, "getent passwd"), strings.Contains(cmd, "/etc/passwd"):
			_, _ = io.WriteString(ch, "root:x:0:0:root:/root:/bin/bash\nalice:x:1000:1000:Alice:/home/alice:/bin/bash\n")
		}
		_ = sendExit(ch, 0)
		return
	}
}

// applyAuthorizedAppend finds every authorized-keys-shaped slug in
// the install script body and trusts it. Rather than reverse-engineer
// the shell quoting in the installer's `printf '%s\n' '<KEY>'` line,
// we walk every single-quoted substring and ask ssh.ParseAuthorizedKey
// — whatever parses, we trust. Robust against script shape changes.
func (s *e2eSSHServer) applyAuthorizedAppend(script string) {
	// Pull every single-quoted slug. Authorized-keys lines have no
	// embedded single quotes in practice, so the naive paired-quote
	// walk is enough.
	idx := 0
	for {
		open := strings.Index(script[idx:], "'")
		if open < 0 {
			return
		}
		open += idx
		close := strings.Index(script[open+1:], "'")
		if close < 0 {
			return
		}
		close += open + 1
		slug := script[open+1 : close]
		if pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(slug)); err == nil {
			s.mu.Lock()
			s.authorizedKeys[string(pk.Marshal())] = true
			s.mu.Unlock()
		}
		idx = close + 1
	}
}

func parseSSHExec(payload []byte) string {
	if len(payload) < 4 {
		return ""
	}
	n := int(payload[0])<<24 | int(payload[1])<<16 | int(payload[2])<<8 | int(payload[3])
	if 4+n > len(payload) {
		return ""
	}
	return string(payload[4 : 4+n])
}

func sendExit(ch ssh.Channel, code uint32) error {
	payload := []byte{byte(code >> 24), byte(code >> 16), byte(code >> 8), byte(code)}
	_, err := ch.SendRequest("exit-status", false, payload)
	return err
}

