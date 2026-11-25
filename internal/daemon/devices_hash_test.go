package daemon

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestHashPublicKey_IgnoresPreKeyOptionsAndComments(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
	want := HashPublicKey(base)
	for _, variant := range []string{
		base + " phone@ios",
		`from="100.64.0.1" ` + base,
		`no-pty,from="1" ` + base + " comment",
	} {
		if got := HashPublicKey(variant); got != want {
			t.Errorf("HashPublicKey(%q) = %s, want %s", variant, got, want)
		}
	}
}
