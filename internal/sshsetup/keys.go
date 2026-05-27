package sshsetup

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// LocalKey describes the key we'll install on the remote. We pin both
// paths and the rendered public-key line so callers can show "we're
// going to install this key" in the UI without re-deriving anything.
type LocalKey struct {
	PrivatePath string // ~/.ssh/id_ed25519 (or whatever we picked / generated)
	PublicPath  string // PrivatePath + ".pub"
	PublicLine  string // single line ready to append to authorized_keys
}

// candidateKey is one ~/.ssh/<name> we consider reusing. Ordered by
// preference inside discoverLocalKey — modern algorithms first.
type candidateKey struct {
	priv string
	pub  string
}

// discoverLocalKey walks ~/.ssh looking for an existing key pair we
// can reuse. ed25519 is preferred (modern, smallest, fastest);
// rsa is the fallback because it's still ubiquitous on older
// remotes. We do NOT touch ecdsa or dsa — ecdsa is fine but rare in
// the wild for personal devs, dsa is deprecated by openssh.
func discoverLocalKey() (LocalKey, bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return LocalKey{}, false, err
	}
	dir := filepath.Join(home, ".ssh")
	candidates := []candidateKey{
		{filepath.Join(dir, "id_ed25519"), filepath.Join(dir, "id_ed25519.pub")},
		{filepath.Join(dir, "id_rsa"), filepath.Join(dir, "id_rsa.pub")},
	}
	for _, c := range candidates {
		if !fileExists(c.priv) || !fileExists(c.pub) {
			continue
		}
		line, err := readPublicKeyLine(c.pub)
		if err != nil {
			continue
		}
		return LocalKey{
			PrivatePath: c.priv,
			PublicPath:  c.pub,
			PublicLine:  line,
		}, true, nil
	}
	return LocalKey{}, false, nil
}

// EnsureLocalKey returns an installable LocalKey. If the user already
// has ~/.ssh/id_ed25519 or ~/.ssh/id_rsa, the existing one is used —
// we don't generate redundant keys, and we don't touch a key that's
// already wired up to ssh-agent or `ssh` config.
//
// If neither exists, we generate a fresh passphrase-less ed25519 at
// ~/.ssh/id_ed25519. No passphrase keeps the attach flow zero-prompt
// post-setup, which is the whole point of this package — adding an
// agent-unlock step would defeat the goal. The local file is chmod
// 600 and lives in the user's HOME, where the OS is already the
// access boundary.
func EnsureLocalKey() (LocalKey, error) {
	if lk, found, err := discoverLocalKey(); err != nil {
		return LocalKey{}, err
	} else if found {
		return lk, nil
	}
	return generateEd25519Key()
}

// generateEd25519Key creates ~/.ssh/id_ed25519 + .pub. We use the
// openssh-encoded PEM private key (header "OPENSSH PRIVATE KEY") so
// the file is loadable by both ssh-keygen and our Go client. The
// public-key line is the standard `ssh-ed25519 BASE64 comment` form
// every authorized_keys file expects.
func generateEd25519Key() (LocalKey, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return LocalKey{}, err
	}
	dir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return LocalKey{}, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return LocalKey{}, fmt.Errorf("generate ed25519: %w", err)
	}
	// MarshalPrivateKey writes the OpenSSH-format PEM that ssh
	// expects (vs. PKCS#8 which it doesn't natively read).
	pemBlock, err := ssh.MarshalPrivateKey(priv, "ccmux-managed")
	if err != nil {
		return LocalKey{}, fmt.Errorf("marshal private: %w", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return LocalKey{}, fmt.Errorf("wrap public: %w", err)
	}
	pubLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " ccmux@" + hostnameOrUnknown()

	privPath := filepath.Join(dir, "id_ed25519")
	pubPath := privPath + ".pub"
	// O_EXCL guards against an unexpected pre-existing file (race
	// against another tool). If it appeared between discoverLocalKey
	// and now, fail loud rather than overwrite.
	pf, err := os.OpenFile(privPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return LocalKey{}, fmt.Errorf("create %s: %w", privPath, err)
	}
	if err := pem.Encode(pf, pemBlock); err != nil {
		_ = pf.Close()
		_ = os.Remove(privPath)
		return LocalKey{}, fmt.Errorf("write %s: %w", privPath, err)
	}
	if err := pf.Close(); err != nil {
		return LocalKey{}, err
	}
	if err := os.WriteFile(pubPath, []byte(pubLine+"\n"), 0o644); err != nil {
		_ = os.Remove(privPath)
		return LocalKey{}, fmt.Errorf("write %s: %w", pubPath, err)
	}
	return LocalKey{PrivatePath: privPath, PublicPath: pubPath, PublicLine: pubLine}, nil
}

// readPublicKeyLine reads a single-line .pub file and returns the
// content with trailing whitespace trimmed. Errors out if the file is
// empty or doesn't parse as an ssh authorized-keys line.
func readPublicKeyLine(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(data))
	if line == "" {
		return "", errors.New("public key file is empty")
	}
	// Sanity-parse via ssh.ParseAuthorizedKey so we fail before
	// touching the remote with garbage.
	if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line)); err != nil {
		return "", fmt.Errorf("invalid authorized-key line: %w", err)
	}
	return line, nil
}

func fileExists(path string) bool {
	if _, err := os.Stat(path); err == nil {
		return true
	}
	return false
}

func hostnameOrUnknown() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "ccmux"
}
